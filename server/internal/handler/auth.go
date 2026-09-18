package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/mail"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/platform/membership"
	"github.com/infinite-canvas/server/internal/service"
)

const (
	RefreshCookieName = "ic_refresh"
	RefreshCookiePath = "/api/auth"
)

var (
	emailRe    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)
)

type AuthHandler struct {
	db           *gorm.DB
	identity     *identity.Service
	billing      *billing.Service
	membership   *membership.Service
	cfg          *config.Config
	mail         *mail.Mailer
	failLim      *middleware.Limiter // 账号连续失败锁定
	mailEmailLim *middleware.Limiter // 同邮箱发信限流（3 次/小时，verify 与 forgot 共用）
	settings     *service.SiteSettingService
}

// SetSettings 注入站点设置：开放注册开关可在管理后台实时切换。
func (h *AuthHandler) SetSettings(settings *service.SiteSettingService) {
	h.settings = settings
}

// registrationEnabled 优先取站点设置，未注入时回落到环境变量。
func (h *AuthHandler) registrationEnabled() bool {
	if h.settings != nil {
		return h.settings.Bool(service.SettingRegistrationEnabled, h.cfg.RegistrationEnabled)
	}
	return h.cfg.RegistrationEnabled
}

func NewAuthHandler(db *gorm.DB, cfg *config.Config, mailer *mail.Mailer) *AuthHandler {
	return &AuthHandler{
		db:           db,
		identity:     identity.NewService(db),
		billing:      billing.NewService(db, model.ProductCanvas),
		membership:   membership.NewService(db),
		cfg:          cfg,
		mail:         mailer,
		failLim:      middleware.NewLimiter(15*time.Minute, 5),
		mailEmailLim: middleware.NewLimiter(time.Hour, 3),
	}
}

func (h *AuthHandler) setRefreshCookie(c *gin.Context, plain string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    plain,
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.RefreshTokenTTL.Seconds()),
	})
}

func (h *AuthHandler) clearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// setMediaCookie 下发只读媒体 cookie（ic_media），与 refresh token 同周期。
// 带上签发时用户的媒体令牌版本，改密等安全事件递增版本后旧 cookie 失效。
func (h *AuthHandler) setMediaCookie(c *gin.Context, user *model.PlatformUser) {
	token, err := auth.IssueMediaToken(user.ID, []byte(h.cfg.JWTSecret), user.MediaTokenVersion)
	if err != nil {
		slog.Error("签发媒体令牌失败", "err", err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.MediaCookieName,
		Value:    token,
		Path:     auth.MediaCookiePath,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.MediaTokenTTL.Seconds()),
	})
}

func (h *AuthHandler) clearMediaCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.MediaCookieName,
		Value:    "",
		Path:     auth.MediaCookiePath,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// clearSessionCookies 清掉 refresh 与媒体两枚 cookie，各自的 Path 必须带回原值。
func (h *AuthHandler) clearSessionCookies(c *gin.Context) {
	h.clearRefreshCookie(c)
	h.clearMediaCookie(c)
}

type registerReq struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	fields := map[string]string{}
	if !emailRe.MatchString(strings.TrimSpace(req.Email)) {
		fields["email"] = "邮箱格式不正确"
	}
	if !usernameRe.MatchString(req.Username) {
		fields["username"] = "用户名需为 3-32 位字母、数字、下划线或连字符"
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		fields["password"] = "密码长度需在 8-72 位之间"
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	if !h.registrationEnabled() {
		errs.Abort(c, errs.ErrRegDisabled)
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		slog.Error("密码哈希失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	user := model.PlatformUser{
		ID:           uuid.New(),
		Email:        email,
		Username:     req.Username,
		PasswordHash: hash,
		DisplayName:  req.Username,
		Role:         "user",
		Status:       "active",
	}

	var verifyToken string
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		// 平台账本行与免费配额随注册一起建，后续任何读余额/配额的地方都不需要处理「行不存在」。
		if err := h.billing.EnsureAccount(tx, user.ID); err != nil {
			return err
		}
		if err := h.membership.SyncQuotaWithin(tx, user.ID, time.Now()); err != nil {
			return err
		}
		var err error
		verifyToken, err = createEmailToken(tx, user.ID, "verify_email")
		return err
	})
	if err != nil {
		if isUniqueViolation(err, "email") {
			errs.Abort(c, errs.ErrEmailTaken)
			return
		}
		if isUniqueViolation(err, "username") {
			errs.Abort(c, errs.ErrUsernameTaken)
			return
		}
		slog.Error("注册失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	// 事务提交后用同一明文发送验证邮件，不再生成第二个令牌
	h.mail.Send(h.mail.VerifyEmailBody(user.Email, verifyToken))

	// 注册即视为登录
	accessToken := h.issueSession(c, &user)
	c.JSON(http.StatusCreated, sessionPayload(&user, h.planFor(&user), accessToken))
}

// createEmailToken 生成邮件令牌并写入库，返回只在下发瞬间存在的明文。
// 重置密码令牌 1 小时有效，验证邮件 24 小时（总规划口径，按 purpose 区分）。
func createEmailToken(tx *gorm.DB, userID uuid.UUID, purpose string) (string, error) {
	plain, tokenHash, err := auth.NewEmailToken()
	if err != nil {
		return "", err
	}
	ttl := auth.EmailTokenTTL
	if purpose == "reset_password" {
		ttl = auth.ResetPasswordTokenTTL
	}
	et := model.EmailToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: tokenHash,
		Purpose:   purpose,
		ExpiresAt: time.Now().Add(ttl),
	}
	if err := tx.Create(&et).Error; err != nil {
		return "", err
	}
	return plain, nil
}

func (h *AuthHandler) sendVerificationEmail(email string, userID uuid.UUID) {
	plain, err := createEmailToken(h.db, userID, "verify_email")
	if err != nil {
		slog.Error("写入验证令牌失败", "err", err)
		return
	}
	h.mail.Send(h.mail.VerifyEmailBody(email, plain))
}

type loginReq struct {
	Account  string `json:"account"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	accountKey := strings.ToLower(strings.TrimSpace(req.Account))
	// 账号锁定：连续 5 次失败后锁 15 分钟
	if blocked, retry := h.failLim.IsBlocked("fail:"+accountKey, time.Now()); blocked {
		errs.Abort(c, errs.WithRetryAfter(errs.ErrRateLimited, retry))
		return
	}
	var user *model.PlatformUser
	var err error
	if strings.Contains(req.Account, "@") {
		user, err = h.identity.GetByEmail(c.Request.Context(), accountKey)
	} else {
		user, err = h.identity.GetByUsername(c.Request.Context(), req.Account)
	}
	if err != nil {
		// 用户不存在与密码错误不区分，避免账号枚举
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.failLim.Allow("fail:"+accountKey, time.Now())
			time.Sleep(200 * time.Millisecond)
			errs.Abort(c, errs.ErrInvalidCreds)
			return
		}
		slog.Error("登录查询失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	// 注销冷静期（pending_deletion）允许登录浏览与导出，生成/下单由 RequireNotPendingDeletion 拦截；
	// 只有 disabled（封禁）拒绝登录，否则用户申请注销后无法撤销。
	if user.Status == "disabled" {
		errs.Abort(c, errs.ErrAccountDisabled)
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		h.failLim.Allow("fail:"+accountKey, time.Now())
		errs.Abort(c, errs.ErrInvalidCreds)
		return
	}
	// 登录成功清除该账号的连续失败计数
	h.failLim.Reset("fail:" + accountKey)
	h.db.Model(user).Update("last_login_at", time.Now())
	accessToken := h.issueSession(c, user)
	c.JSON(http.StatusOK, sessionPayload(user, h.planFor(user), accessToken))
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	cookie, err := c.Cookie(RefreshCookieName)
	if err != nil || cookie == "" {
		h.clearSessionCookies(c)
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	tokenHash := auth.HashToken(cookie)
	rt, err := h.identity.GetSessionByHash(c.Request.Context(), tokenHash)
	if err != nil {
		// 已撤销令牌被复用 → 撤销该用户全部令牌
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.clearSessionCookies(c)
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		slog.Error("刷新令牌查询失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if rt.RevokedAt != nil {
		// 复用检测：已撤销令牌再次使用，撤销该用户全部令牌并要求重新登录
		h.revokeAllUserTokens(rt.UserID)
		h.clearSessionCookies(c)
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if time.Now().After(rt.ExpiresAt) {
		h.identity.RevokeSessionIfActive(c.Request.Context(), rt.ID, time.Now())
		h.clearSessionCookies(c)
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	user, err := h.identity.GetByID(c.Request.Context(), rt.UserID)
	if err != nil {
		h.clearSessionCookies(c)
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	// 与登录同口径：只拦 disabled，冷静期账号可刷新以维持浏览与导出。
	if user.Status == "disabled" {
		h.clearSessionCookies(c)
		errs.Abort(c, errs.ErrAccountDisabled)
		return
	}
	// 轮换：原子撤销旧令牌，保证并发刷新只有一个请求成功
	now := time.Now()
	rotated, err := h.identity.RevokeSessionIfActive(c.Request.Context(), rt.ID, now)
	if err != nil {
		slog.Error("撤销刷新令牌失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if !rotated {
		// 并发轮换竞争失败：令牌已被胜者轮换。不能按复用处理，
		// 否则会撤销胜者刚签发的新令牌，并清掉胜者写入的新 cookie。
		// 真正的复用由上面的 RevokedAt != nil 分支处理。
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	h.db.Model(user).Update("last_login_at", now)
	accessToken := h.issueSession(c, user)
	c.JSON(http.StatusOK, sessionPayload(user, h.planFor(user), accessToken))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	cookie, err := c.Cookie(RefreshCookieName)
	if err == nil && cookie != "" {
		h.identity.RevokeSessionByHash(c.Request.Context(), auth.HashToken(cookie), time.Now())
	}
	h.clearSessionCookies(c)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) revokeAllUserTokens(userID uuid.UUID) {
	ctx := context.Background()
	// 复用检测属安全事件：媒体令牌版本一并递增（差异清单 #9）。
	if err := h.identity.BumpMediaTokenVersion(ctx, userID); err != nil {
		slog.Error("递增媒体令牌版本失败", "err", err)
	}
	if err := h.identity.RevokeSessions(ctx, userID, time.Now()); err != nil {
		slog.Error("撤销用户全部会话失败", "err", err)
	}
}

// issueSession 签发 access token + refresh token，写库并下发 cookie，返回 access token。
func (h *AuthHandler) issueSession(c *gin.Context, user *model.PlatformUser) string {
	accessToken, err := auth.IssueAccessToken(user, []byte(h.cfg.JWTSecret))
	if err != nil {
		slog.Error("签发 access token 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return ""
	}
	plain, tokenHash, err := auth.NewRefreshToken()
	if err != nil {
		slog.Error("生成 refresh token 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return ""
	}
	rt := model.Session{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(auth.RefreshTokenTTL),
		UserAgent: c.Request.UserAgent(),
		IP:        middleware.ClientIP(c),
	}
	if err := h.identity.CreateSession(c.Request.Context(), &rt); err != nil {
		slog.Error("写入 refresh token 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return ""
	}
	h.setRefreshCookie(c, plain)
	h.setMediaCookie(c, user)
	c.Set("user_id", user.ID.String())
	c.Set("user_role", user.Role)
	return accessToken
}

// planFor 返回注册/登录响应里的 plan 展示。会话负载只展示免费档定义（形状不变），
// 真实档位与权益由 /api/me 的 membership 域派生。
func (h *AuthHandler) planFor(user *model.PlatformUser) model.MembershipPlan {
	plan, err := h.membership.PlanDef(context.Background(), "free")
	if err != nil {
		return model.MembershipPlan{ID: "free", Name: "免费"}
	}
	return plan
}

func sessionPayload(user *model.PlatformUser, plan model.MembershipPlan, accessToken string) gin.H {
	return gin.H{
		"user":        userPayload(user),
		"accessToken": accessToken,
		// 顶层再放一份，登录页直接取顶层字段；user 对象里的是同一份事实。
		"mustChangePassword": user.MustChangePassword,
		"plan": gin.H{
			"id":   plan.ID,
			"name": plan.Name,
		},
	}
}

func userPayload(user *model.PlatformUser) gin.H {
	payload := gin.H{
		"id":                 user.ID.String(),
		"email":              user.Email,
		"username":           user.Username,
		"displayName":        user.DisplayName,
		"avatarUrl":          user.AvatarURL,
		"role":               user.Role,
		"emailVerified":      user.EmailVerifiedAt != nil,
		"mustChangePassword": user.MustChangePassword,
	}
	return payload
}

// isDuplicateKey / isUniqueViolation 统一委托 errs 实现：
// MySQL 按 1062 错误码并从报文取索引名映射列，SQLite 解析报错文案里的列名；
// 不再拿报错文本去匹配「重复值」（用户名含 email 字样时会把 USERNAME_TAKEN 误判成 EMAIL_TAKEN）。
func isDuplicateKey(err error) bool {
	return errs.IsDuplicateKey(err)
}

func isUniqueViolation(err error, column string) bool {
	return errs.UniqueViolationColumn(err) == column
}

// VerifyEmailSend 重发验证邮件（需登录）。
func (h *AuthHandler) VerifyEmailSend(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if user.EmailVerifiedAt != nil {
		c.Status(http.StatusNoContent)
		return
	}
	// 同邮箱维度限流（IP 维度已由路由中间件处理）
	if ok, retry := h.mailEmailLim.Allow(auth.HashEmail(user.Email), time.Now()); !ok {
		errs.Abort(c, errs.WithRetryAfter(errs.ErrRateLimited, retry))
		return
	}
	h.sendVerificationEmail(user.Email, user.ID)
	c.Status(http.StatusNoContent)
}

type verifyEmailReq struct {
	Token string `json:"token"`
}

func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req verifyEmailReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	tokenHash := auth.HashToken(req.Token)
	var et model.EmailToken
	if err := h.db.Where("token_hash = ? AND purpose = ?", tokenHash, "verify_email").First(&et).Error; err != nil {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	if et.UsedAt != nil || time.Now().After(et.ExpiresAt) {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	now := time.Now()
	// 条件更新保证「一次性」：并发使用同一令牌时只有一个请求能把 used_at 置位
	// （写法与 refresh 轮换一致），避免先读后写的竞态让令牌被消费两次。
	res := h.db.Model(&model.EmailToken{}).
		Where("id = ? AND used_at IS NULL", et.ID).
		Update("used_at", now)
	if res.Error != nil {
		slog.Error("验证邮箱失败", "err", res.Error)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected == 0 {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	err := h.db.Model(&model.PlatformUser{}).Where("id = ?", et.UserID).Update("email_verified_at", now).Error
	if err != nil {
		slog.Error("验证邮箱失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var user model.PlatformUser
	h.db.First(&user, "id = ?", et.UserID)
	c.JSON(http.StatusOK, gin.H{"user": userPayload(&user)})
}

type forgotReq struct {
	Email string `json:"email"`
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req forgotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	// 邮箱维度与 IP 维度一样一视同仁地计数，不能通过「有的邮箱被限流」反推账号是否存在
	if ok, retry := h.mailEmailLim.Allow(auth.HashEmail(email), time.Now()); !ok {
		errs.Abort(c, errs.WithRetryAfter(errs.ErrRateLimited, retry))
		return
	}
	var user model.PlatformUser
	// 无论邮箱是否存在都返回 204
	if err := h.db.Where("email = ?", email).First(&user).Error; err == nil {
		plain, err := createEmailToken(h.db, user.ID, "reset_password")
		if err != nil {
			slog.Error("写入重置令牌失败", "err", err)
		} else {
			h.mail.Send(h.mail.ResetPasswordBody(email, plain))
		}
	}
	c.Status(http.StatusNoContent)
}

type resetReq struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"password": "密码长度需在 8-72 位之间"}))
		return
	}
	tokenHash := auth.HashToken(req.Token)
	var et model.EmailToken
	if err := h.db.Where("token_hash = ? AND purpose = ?", tokenHash, "reset_password").First(&et).Error; err != nil {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	if et.UsedAt != nil || time.Now().After(et.ExpiresAt) {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	now := time.Now()
	// 与 Verify 同一口径：条件更新保证重置令牌只被消费一次。
	res := h.db.Model(&model.EmailToken{}).
		Where("id = ? AND used_at IS NULL", et.ID).
		Update("used_at", now)
	if res.Error != nil {
		slog.Error("重置密码失败", "err", res.Error)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected == 0 {
		errs.Abort(c, errs.ErrTokenInvalid)
		return
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.PlatformUser{}).Where("id = ?", et.UserID).
			Update("password_hash", hash).Error; err != nil {
			return err
		}
		// 重置密码同时递增媒体令牌版本，ic_media cookie 立即失效（差异清单 #9）。
		if err := h.identity.BumpMediaTokenVersionTx(tx, et.UserID); err != nil {
			return err
		}
		// 撤销该用户全部 refresh token，强制所有设备重新登录
		return h.identity.RevokeSessionsTx(tx, et.UserID, now)
	})
	if err != nil {
		slog.Error("重置密码失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.Status(http.StatusNoContent)
}
