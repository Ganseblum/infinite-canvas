package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/service"
)

// Limiter 进程内滑动窗口限流（单实例够用，多实例时换 Redis）。
type Limiter struct {
	mu       sync.Mutex
	window   time.Duration
	limit    int
	requests map[string][]time.Time
}

func NewLimiter(window time.Duration, limit int) *Limiter {
	return &Limiter{window: window, limit: limit, requests: make(map[string][]time.Time)}
}

// Allow 判断 key 在窗口内是否未超限；超限返回应等待的秒数。
func (l *Limiter) Allow(key string, now time.Time) (ok bool, retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	history := l.requests[key]
	kept := history[:0]
	for _, t := range history {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.requests[key] = kept
		oldest := kept[0]
		retryAfter = int(l.window - now.Sub(oldest))
		if retryAfter < 1 {
			retryAfter = 1
		}
		return false, retryAfter
	}
	kept = append(kept, now)
	l.requests[key] = kept
	return true, 0
}

// Reset 清除指定 key 的计数，用于登录成功后清零连续失败次数。
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.requests, key)
}

// IsBlocked 只检查不记录，用于登录前判断账号是否已被锁定。
func (l *Limiter) IsBlocked(key string, now time.Time) (blocked bool, retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	count := 0
	for _, t := range l.requests[key] {
		if t.After(cutoff) {
			count++
		}
	}
	if count >= l.limit {
		oldest := l.requests[key][0]
		retryAfter = int(l.window - now.Sub(oldest))
		if retryAfter < 1 {
			retryAfter = 1
		}
		return true, retryAfter
	}
	return false, 0
}

// RateLimit 按 key 维度限流；failOnKey 为 true 时同时用失败计数（用于登录锁定）。
func RateLimit(limiter *Limiter, keyFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFunc(c)
		if ok, retry := limiter.Allow(key, time.Now()); !ok {
			errs.Abort(c, errs.WithRetryAfter(errs.ErrRateLimited, retry))
			return
		}
		c.Next()
	}
}

// authenticateBearer 解析 Authorization 头里的 access token，成功时返回 claims。
// 失败时按既有约定返回 TOKEN_EXPIRED 或 UNAUTHORIZED，由调用方决定是否中止。
func authenticateBearer(header string, secret []byte) (*auth.AccessClaims, *errs.AppError) {
	if !strings.HasPrefix(header, "Bearer ") {
		return nil, errs.ErrUnauthorized
	}
	tokenStr := strings.TrimPrefix(header, "Bearer ")
	claims, err := auth.ParseAccessToken(tokenStr, secret)
	if err != nil {
		if err == auth.ErrTokenExpired {
			return nil, errs.ErrTokenExpired
		}
		return nil, errs.ErrUnauthorized
	}
	return claims, nil
}

// Auth 鉴权中间件：解析 Bearer JWT。
func Auth(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, appErr := authenticateBearer(c.GetHeader("Authorization"), secret)
		if appErr != nil {
			errs.Abort(c, appErr)
			return
		}
		c.Set("user_id", claims.Sub)
		c.Set("user_role", claims.Role)
		c.Next()
	}
}

// MediaAuth 是媒体接口的专用鉴权：
//
//	GET / HEAD 接受 Bearer 或 ic_media cookie 二者之一，两者都带时以 Bearer 为准；
//	PUT / DELETE 只接受 Bearer，不认 cookie。
//
// cookie 是专为 <img src> 准备的只读凭据，写操作认 cookie 等于给跨站请求开写入口。
// idn 用于媒体令牌版本校验：安全事件递增 media_token_version 后旧 cookie 立即失效（差异清单 #9）。
func MediaAuth(secret []byte, idn *identity.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		readMethod := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead
		header := c.GetHeader("Authorization")

		if header != "" {
			claims, appErr := authenticateBearer(header, secret)
			if appErr != nil {
				errs.Abort(c, appErr)
				return
			}
			c.Set("user_id", claims.Sub)
			c.Set("user_role", claims.Role)
			c.Next()
			return
		}
		if !readMethod {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		cookie, err := c.Cookie(auth.MediaCookieName)
		if err != nil || cookie == "" {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		claims, err := auth.ParseMediaToken(cookie, secret)
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		// 媒体令牌版本校验（差异清单 #9）：改密/重置/封禁递增版本后旧令牌失效。
		uid, err := uuid.Parse(claims.Sub)
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		ver, err := idn.MediaTokenVersion(c.Request.Context(), uid)
		if err != nil || ver != claims.Ver {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		c.Set("user_id", claims.Sub)
		c.Next()
	}
}

// RequireActiveUser 加载当前用户并拒绝已封禁（disabled）的账号。
// 注销冷静期（pending_deletion）允许登录后的读操作，生成、下单等写操作
// 由 RequireNotPendingDeletion 按路由口径拦截。
// 第一期签发的 access token 在封禁后 15 分钟内仍有效，这里按库里的最新状态拦截，
// 撤销 refresh token 负责让会话在那之后彻底失效。
func RequireActiveUser(idn *identity.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, err := uuid.Parse(c.GetString("user_id"))
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		user, err := idn.GetByID(c.Request.Context(), uid)
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		if user.Status == "disabled" {
			errs.Abort(c, errs.ErrAccountDisabled)
			return
		}
		c.Set("user_status", user.Status)
		c.Next()
	}
}

// RequireNotPendingDeletion 拦截注销冷静期内的写操作（生成、下单）。
func RequireNotPendingDeletion() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("user_status") == "pending_deletion" {
			errs.Abort(c, errs.ErrAccountPendingDeletion)
			return
		}
		c.Next()
	}
}

// passwordChangeAllowlist 是强制改密期间仍然放行的路由：登出与刷新用于维持或结束会话，
// 改密接口本身是唯一的出口，其余接口（含 /api/me、管理后台与媒体读）一律拦截。
var passwordChangeAllowlist = map[string]bool{
	"POST /api/v1/auth/logout":  true,
	"POST /api/v1/auth/refresh": true,
	"POST /api/v1/me/password":  true,
}

// RequirePasswordChanged 拦截 must_change_password 的用户，返回 403 PASSWORD_CHANGE_REQUIRED。
//
// 必须挂在 Auth（以及 RequireActiveUser）之后：只读上下文里的 user_id，标志每请求从库里读，
// 用户改密成功后下一个请求立即放行，不需要等 access token 过期。
// 未挂 Auth 的路由不会命中（user_id 为空即按未授权拒绝），fail closed。
func RequirePasswordChanged(idn *identity.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if passwordChangeAllowlist[c.Request.Method+" "+c.FullPath()] {
			c.Next()
			return
		}
		uid, err := uuid.Parse(c.GetString("user_id"))
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		user, err := idn.GetByID(c.Request.Context(), uid)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				slog.Error("读取强制改密标记失败", "err", err, "user_id", uid)
			}
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		if user.MustChangePassword {
			errs.Abort(c, errs.ErrPasswordChangeRequired)
			return
		}
		c.Next()
	}
}

// errMaintenanceMode 维护模式写拦截的统一响应；错误码表在 errs 包，这里不复用码表单例。
var errMaintenanceMode = errs.New(503, "MAINTENANCE_MODE", "站点维护中，写入操作暂不可用，请稍后再试")

// MaintenanceGate 维护模式写拦截（差异清单 #117）：站点设置开启维护模式后，
// 非管理员的写请求（POST/PUT/PATCH/DELETE）返回 503 MAINTENANCE_MODE；
// GET/HEAD/OPTIONS 放行，/api/auth/*、/api/oidc/*（token 是 POST，OIDC 流程不能被拦断）
// 与 /api/admin/* 放行（健康检查不在 /api 组，天然不受影响）。
//
// 挂载在 /api 组级、Auth 之前：匿名与普通用户的写请求在这里被挡下。管理员判定按 RBAC
// 现状每请求查库（platform_users.role_key 非空即有后台角色），只在「维护中 + 写请求 + 非豁免路径」时才查。
func MaintenanceGate(settings *service.SiteSettingService, idn *identity.Service, secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			c.Next()
			return
		}
		path := c.Request.URL.Path
		// 支付回调是渠道服务器的服务端调用，维护期也必须照常到账，
		// 否则渠道会反复重试而订单长期 pending。
		if strings.HasPrefix(path, "/api/v1/auth/") || strings.HasPrefix(path, "/api/admin/") ||
			strings.HasPrefix(path, "/api/v1/payments/webhook/") || strings.HasPrefix(path, "/api/v1/oidc/") {
			c.Next()
			return
		}
		if !settings.Bool(service.SettingMaintenanceMode, false) {
			c.Next()
			return
		}
		if hasBackendRole(c, idn, secret) {
			c.Next()
			return
		}
		errs.Abort(c, errMaintenanceMode)
	}
}

// hasBackendRole 判定请求者是否持有后台角色。组级中间件先于 Auth 执行，这里直接
// 解析 Bearer 拿用户 id，再按库里的 role_key 判定，不读 access token 里的角色投影。
func hasBackendRole(c *gin.Context, idn *identity.Service, secret []byte) bool {
	claims, appErr := authenticateBearer(c.GetHeader("Authorization"), secret)
	if appErr != nil {
		return false
	}
	uid, err := uuid.Parse(claims.Sub)
	if err != nil {
		return false
	}
	user, err := idn.GetByID(context.Background(), uid)
	if err != nil {
		return false
	}
	return user.RoleKey != nil && *user.RoleKey != ""
}
