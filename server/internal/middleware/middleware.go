package middleware

import (
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
	"github.com/infinite-canvas/server/internal/model"
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
func MediaAuth(secret []byte) gin.HandlerFunc {
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
		c.Set("user_id", claims.Sub)
		c.Next()
	}
}

// VerifyExistingUser 加载当前用户并拒绝已封禁或已注销的账号。
// 第一期签发的 access token 在封禁后 15 分钟内仍有效，这里按库里的最新状态拦截，
// 撤销 refresh token 负责让会话在那之后彻底失效。
func RequireActiveUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, err := uuid.Parse(c.GetString("user_id"))
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		var user model.User
		if err := db.First(&user, "id = ?", uid).Error; err != nil {
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
	"POST /api/auth/logout":  true,
	"POST /api/auth/refresh": true,
	"POST /api/me/password":  true,
}

// RequirePasswordChanged 拦截 must_change_password 的用户，返回 403 PASSWORD_CHANGE_REQUIRED。
//
// 必须挂在 Auth（以及 RequireActiveUser）之后：只读上下文里的 user_id，标志每请求从库里读，
// 用户改密成功后下一个请求立即放行，不需要等 access token 过期。
// 未挂 Auth 的路由不会命中（user_id 为空即按未授权拒绝），fail closed。
func RequirePasswordChanged(db *gorm.DB) gin.HandlerFunc {
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
		var row struct{ MustChangePassword bool }
		err = db.Model(&model.User{}).Select("must_change_password").Where("id = ?", uid).Take(&row).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				slog.Error("读取强制改密标记失败", "err", err, "user_id", uid)
			}
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		if row.MustChangePassword {
			errs.Abort(c, errs.ErrPasswordChangeRequired)
			return
		}
		c.Next()
	}
}
