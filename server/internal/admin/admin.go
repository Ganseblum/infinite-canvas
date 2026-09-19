package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/account"
	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/authz"
	billingapi "github.com/infinite-canvas/server/internal/billing"
	"github.com/infinite-canvas/server/internal/blog"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/platform/membership"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
)

// AdminHandler 提供管理后台的全部接口，路由挂在 /api/admin 下并由 RequireAdmin 保护。
type AdminHandler struct {
	db         *gorm.DB
	identity   *identity.Service
	cfg        *config.Config
	credits    *billing.Service
	membership *membership.Service
	usage      *platformstorage.Service
	catalog    *service.CatalogService
	audit      *service.AuditService
	cleanup    *service.CleanupService
	upstream   *service.UpstreamService
	requests   *service.AIRequestService
	moderation *service.ModerationService
	media      *service.MediaWriteService
	store      storage.Storage
	wm         releaseWatermarker
	wmEnabled  func() bool
	settings   *service.SiteSettingService
	blog       *blog.AdminHandler
}

// releaseWatermarker 供隔离件人工释放按物主档位烧录水印（评审 E-4），与 ai 域共用 *watermark.Service。
type releaseWatermarker interface {
	Image(data []byte, mimeType string) ([]byte, string, error)
	Video(ctx context.Context, data []byte, mimeType string) ([]byte, error)
}

func NewAdminHandler(db *gorm.DB, cfg *config.Config, stor storage.Storage) *AdminHandler {
	return NewAdminHandlerWithUpstream(db, cfg, stor, nil)
}

// NewAdminHandlerWithUpstream 注入上游服务后启用平台渠道管理。
func NewAdminHandlerWithUpstream(db *gorm.DB, cfg *config.Config, stor storage.Storage, upstream *service.UpstreamService) *AdminHandler {
	return &AdminHandler{
		db:         db,
		identity:   identity.NewService(db),
		cfg:        cfg,
		credits:    billing.NewService(db, model.ProductCanvas),
		membership: membership.NewService(db),
		usage:      platformstorage.NewService(db, model.ProductCanvas),
		catalog:    service.NewCatalogService(db, func() bool { return cfg.PromotionEnabled }),
		audit:      service.NewAuditService(db),
		cleanup:    service.NewCleanupService(db, stor),
		upstream:   upstream,
		requests:   service.NewAIRequestService(db),
		media:      service.NewMediaWriteService(db, stor),
		store:      stor,
	}
}

// SetWatermark 注入水印服务：人工释放隔离件时，生成产物按物主档位烧录水印（评审 E-4）。
// SetBlog 注入博客管理面（内容域 handler）；未注入时管理路由仍注册，
// 请求时会构造无再验证回调的默认实例（测试夹具）。
func (h *AdminHandler) SetBlog(b *blog.AdminHandler) { h.blog = b }

func (h *AdminHandler) SetWatermark(wm releaseWatermarker, enabled func() bool) {
	h.wm = wm
	h.wmEnabled = enabled
}

// ===== 用户管理 =====

var adminUserSorts = map[string]string{
	"createdAt":       "platform_users.created_at",
	"purchasedMicros": "COALESCE(ca.purchased_micros, 0)",
	"grantedMicros":   "COALESCE(ca.granted_micros, 0)",
	"storageBytes":    "COALESCE(sa.used_bytes, 0)",
}

type createUserReq struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	RoleKey     string `json:"roleKey"`
}

// CreateUser 管理员建号：服务端生成一次性临时密码，只在本次 201 响应里返回明文；
// 邮箱由管理员断言为已验证，账号强制首次登录改密。审计摘要只记账号属性，绝不写密码。
func (h *AdminHandler) CreateUser(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	displayName := strings.TrimSpace(req.DisplayName)
	roleKey := strings.TrimSpace(req.RoleKey)
	fields := map[string]string{}
	if !account.EmailRe.MatchString(email) {
		fields["email"] = "邮箱格式不正确"
	}
	if roleKey == "" {
		fields["roleKey"] = "必须指定角色"
	}
	if len([]rune(displayName)) > 64 {
		fields["displayName"] = "展示昵称不能超过 64 个字符"
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	var role model.Role
	if err := h.db.First(&role, "role_key = ?", roleKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"roleKey": "角色不存在"}))
			return
		}
		slog.Error("读取角色失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	password := auth.NewTemporaryPassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		slog.Error("生成临时密码哈希失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	now := time.Now()
	user := model.PlatformUser{
		ID:                 uuid.New(),
		Email:              email,
		PasswordHash:       hash,
		DisplayName:        displayName,
		Role:               authz.RoleProjection(&roleKey),
		RoleKey:            &roleKey,
		Status:             "active",
		MustChangePassword: true,
		EmailVerifiedAt:    &now,
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		username, err := pickUsername(tx, email)
		if err != nil {
			return err
		}
		user.Username = username
		if user.DisplayName == "" {
			user.DisplayName = username
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		// 平台账本行与免费配额随建号一起建，与注册保持一致，后续读余额不需要处理「行不存在」。
		if err := h.credits.EnsureAccount(tx, user.ID); err != nil {
			return err
		}
		if err := h.membership.SyncQuotaWithin(tx, user.ID, now); err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "user.create", "user", user.ID.String(), c.GetString("request_id"), "",
			nil, gin.H{
				"email":              user.Email,
				"username":           user.Username,
				"displayName":        user.DisplayName,
				"roleKey":            roleKey,
				"emailVerified":      true,
				"emailVerifiedBy":    "admin",
				"mustChangePassword": true,
				"note":               "由管理员建号并断言邮箱，用户首次登录必须修改临时密码",
			})
	})
	if err != nil {
		if errs.UniqueViolationColumn(err) == "email" {
			errs.Abort(c, errs.ErrEmailTaken)
			return
		}
		if errs.UniqueViolationColumn(err) == "username" {
			errs.Abort(c, errs.ErrUsernameTaken)
			return
		}
		slog.Error("管理员建号失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"user": gin.H{
			"id":                 user.ID.String(),
			"email":              user.Email,
			"username":           user.Username,
			"displayName":        user.DisplayName,
			"roleKey":            roleKeyJSON(user.RoleKey),
			"status":             user.Status,
			"emailVerified":      user.EmailVerifiedAt != nil,
			"mustChangePassword": user.MustChangePassword,
			"createdAt":          httpx.FormatTime(user.CreatedAt),
		},
		// 明文只在这里出现一次：库里只存 bcrypt 哈希，审计摘要与后续查询都不回传。
		"temporaryPassword": password,
	})
}

// pickUsername 按邮箱本地部分生成用户名，冲突时追加最小可用的数字后缀（user、user2、user3…）。
// 与注册的 username 规则一致：只保留字母、数字、下划线与连字符，长度 3-32。
func pickUsername(tx *gorm.DB, email string) (string, error) {
	base := usernameBase(email)
	var taken []string
	if err := tx.Model(&model.PlatformUser{}).
		Where("username = ? OR username LIKE ?", base, base+"%").
		Pluck("username", &taken).Error; err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(taken))
	for _, name := range taken {
		used[name] = struct{}{}
	}
	if _, exists := used[base]; !exists {
		return base, nil
	}
	for i := 2; i <= 999; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if _, exists := used[candidate]; !exists {
			return candidate, nil
		}
	}
	return "", errors.New("邮箱本地部分生成的用户名已用尽")
}

// usernameBase 提取用户名基底：只保留注册规则允许的字符，全部被丢弃时回落到 user，
// 长度截到 28 位，给数字后缀留出空间。
func usernameBase(email string) string {
	local := email
	if idx := strings.IndexByte(email, '@'); idx >= 0 {
		local = email[:idx]
	}
	var b strings.Builder
	for _, r := range local {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		return "user"
	}
	if len(name) > 28 {
		name = name[:28]
	}
	if len(name) < 3 {
		name += "user"[:3-len(name)]
	}
	return name
}

// planFilter 是用户列表按档位筛选的 SQL 条件（D6 新口径，与 membership.ActivePlan
// 逐行派生等价）：存在 period_end 未到的订阅为 paid；最新订阅（period_end 最大）落在
// 到期后 60 天宽限内为 sunset；其余 free。返回条件 SQL 与对应绑定参数。
func planFilter(planID string, now time.Time) (string, []any) {
	sunsetLine := now.AddDate(0, 0, -membership.SunsetGraceDays)
	switch planID {
	case "paid":
		return "EXISTS (SELECT 1 FROM membership_subscriptions s WHERE s.user_id = platform_users.id AND s.period_end > ?)", []any{now}
	case "sunset":
		return "(EXISTS (SELECT 1 FROM membership_subscriptions s WHERE s.user_id = platform_users.id AND s.period_end <= ? AND s.period_end > ?) AND NOT EXISTS (SELECT 1 FROM membership_subscriptions s WHERE s.user_id = platform_users.id AND s.period_end > ?))",
			[]any{now, sunsetLine, now}
	case "free":
		return "NOT EXISTS (SELECT 1 FROM membership_subscriptions s WHERE s.user_id = platform_users.id AND s.period_end > ?)", []any{sunsetLine}
	default:
		return "", nil
	}
}

func (h *AdminHandler) ListUsers(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	sortColumn, ok := httpx.ParseSort(c.Query("sort"), adminUserSorts, "-createdAt")
	if !ok {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"sort": "sort 不在白名单内"}))
		return
	}
	status := c.Query("status")
	if status != "" && status != "active" && status != "disabled" && status != "pending_deletion" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
		return
	}
	planID := c.Query("planId")
	if planID != "" && planID != "free" && planID != "paid" && planID != "sunset" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"planId": "planId 取值非法"}))
		return
	}

	now := time.Now()
	base := h.db.Model(&model.PlatformUser{}).
		Joins("LEFT JOIN credit_accounts ca ON ca.user_id = platform_users.id").
		Joins("LEFT JOIN storage_accounts sa ON sa.user_id = platform_users.id").
		Where("platform_users.id <> ?", uuid.Nil)
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		pattern := httpx.SearchPattern(q)
		base = base.Where("LOWER(platform_users.email) LIKE ? OR LOWER(platform_users.username) LIKE ?", pattern, pattern)
	}
	if status != "" {
		base = base.Where("platform_users.status = ?", status)
	}
	if planID != "" {
		cond, args := planFilter(planID, now)
		base = base.Where(cond, args...)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		slog.Error("统计用户失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	var rows []struct {
		ID              uuid.UUID
		Email           string
		Username        string
		Role            string
		RoleKey         *string
		Status          string
		EmailVerifiedAt *time.Time
		CreatedAt       time.Time
		PurchasedMicros int64
		GrantedMicros   int64
		StorageBytes    int64
		StorageQuota    int64
	}
	selectExpr := `platform_users.id, platform_users.email, platform_users.username, platform_users.role, platform_users.role_key, platform_users.status,
		platform_users.email_verified_at, platform_users.created_at,
		COALESCE(ca.purchased_micros, 0) AS purchased_micros,
		COALESCE(ca.granted_micros, 0) AS granted_micros,
		COALESCE(sa.used_bytes, 0) AS storage_bytes,
		COALESCE(sa.quota_bytes, 0) AS storage_quota`
	err := base.Select(selectExpr).
		Order(sortColumn).Order("platform_users.id ASC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Scan(&rows).Error
	if err != nil {
		slog.Error("查询用户列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	// 管理流量低：逐行派生档位与最新订阅周期，planId/paidUntil 键名不变、语义按 D6 新口径。
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		derived, graceEndsAt, err := h.membership.ActivePlan(c.Request.Context(), row.ID, now)
		if err != nil {
			slog.Error("派生用户档位失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		// membership.periodEnd 与 paidUntil 同源：最新订阅行的 period_end。
		paidUntil := membership.LatestPaidUntil(h.db, row.ID)
		items = append(items, gin.H{
			"id":              row.ID.String(),
			"email":           row.Email,
			"username":        row.Username,
			"role":            row.Role,
			"roleKey":         roleKeyJSON(row.RoleKey),
			"status":          row.Status,
			"emailVerified":   row.EmailVerifiedAt != nil,
			"planId":          derived,
			"purchasedMicros": row.PurchasedMicros,
			"grantedMicros":   row.GrantedMicros,
			"paidUntil":       httpx.FormatTimePtr(paidUntil),
			"storageBytes":    row.StorageBytes,
			"createdAt":       httpx.FormatTime(row.CreatedAt),
			// T07 纯加法：会员/存储/产品聚合键，旧键全部保留。
			"membership": gin.H{
				"planId":      derived,
				"periodEnd":   httpx.FormatTimePtr(paidUntil),
				"graceEndsAt": httpx.FormatTimePtr(graceEndsAt),
			},
			"storage": gin.H{
				"usedBytes":  row.StorageBytes,
				"quotaBytes": row.StorageQuota,
			},
			"products": []string{model.ProductCanvas},
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

func (h *AdminHandler) GetUser(c *gin.Context) {
	userID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	user, err := h.identity.GetByID(c.Request.Context(), userID)
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	now := time.Now()
	planID, graceEndsAt, err := h.membership.ActivePlan(c.Request.Context(), userID, now)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	plan, err := h.membership.PlanDef(c.Request.Context(), planID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	storageBytes, quotaBytes, _ := h.usage.Snapshot(c.Request.Context(), userID)
	balance, err := h.credits.Balance(c.Request.Context(), userID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	mediaCount, _ := h.mediaCount(userID)
	// membership.periodEnd 与 paidUntil 同源：最新订阅行的 period_end。
	paidUntil := membership.LatestPaidUntil(h.db, userID)
	c.JSON(http.StatusOK, gin.H{"user": gin.H{
		"id":              user.ID.String(),
		"email":           user.Email,
		"username":        user.Username,
		"role":            user.Role,
		"roleKey":         roleKeyJSON(user.RoleKey),
		"status":          user.Status,
		"emailVerified":   user.EmailVerifiedAt != nil,
		"createdAt":       httpx.FormatTime(user.CreatedAt),
		"lastLoginAt":     httpx.FormatTimePtr(user.LastLoginAt),
		"planId":          plan.ID,
		"planName":        plan.Name,
		"storageLimit":    plan.StorageBytes,
		"maxFileBytes":    plan.MaxFileBytes,
		"retentionDays":   plan.RetentionDays,
		"purchasedMicros": balance.PurchasedMicros,
		"grantedMicros":   balance.GrantedMicros,
		"paidUntil":       httpx.FormatTimePtr(paidUntil),
		"storageBytes":    storageBytes,
		"mediaCount":      mediaCount,
		"readOnly":        storageBytes > plan.StorageBytes,
		// T07 纯加法：会员/存储/产品聚合键，旧键全部保留。
		"membership": gin.H{
			"planId":      plan.ID,
			"periodEnd":   httpx.FormatTimePtr(paidUntil),
			"graceEndsAt": httpx.FormatTimePtr(graceEndsAt),
		},
		"storage": gin.H{
			"usedBytes":  storageBytes,
			"quotaBytes": quotaBytes,
		},
		"products": []string{model.ProductCanvas},
	}})
}

func (h *AdminHandler) mediaCount(userID uuid.UUID) (int64, error) {
	var count int64
	err := h.db.Model(&model.MediaFile{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

type patchUserReq struct {
	Status *string `json:"status"`
}

func (h *AdminHandler) PatchUser(c *gin.Context) {
	userID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req patchUserReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Status == nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if *req.Status != "active" && *req.Status != "disabled" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 只能是 active 或 disabled"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	now := time.Now()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// 封禁前先确认不会把系统角色上的最后一个 active 用户停掉。
		if *req.Status == "disabled" {
			var target model.PlatformUser
			if err := tx.First(&target, "id = ?", userID).Error; err != nil {
				return err
			}
			if isSystemRoleKey(target.RoleKey) {
				remain, err := h.countActiveSystemMembers(tx, userID)
				if err != nil {
					return err
				}
				if remain == 0 {
					return errLastSystemMember
				}
			}
		}
		if err := h.identity.SetStatusTx(tx, userID, *req.Status); err != nil {
			return err
		}
		// 封禁必须同时撤销全部 refresh token，最多 15 分钟后彻底掉线。
		if *req.Status == "disabled" {
			// 封禁同时递增媒体令牌版本，ic_media 立即失效（差异清单 #9）。
			if err := h.identity.BumpMediaTokenVersionTx(tx, userID); err != nil {
				return err
			}
			if err := h.identity.RevokeSessionsTx(tx, userID, now); err != nil {
				return err
			}
		}
		return h.audit.Record(tx, actorID, "user.status", "user", userID.String(), c.GetString("request_id"), "", nil,
			gin.H{"status": *req.Status})
	})
	if errors.Is(err, errLastSystemMember) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{
			"status": "系统角色必须保留至少一个 active 用户，不能封禁最后一个管理员",
		}))
		return
	}
	if err != nil {
		slog.Error("更新用户状态失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": userID.String(), "status": *req.Status})
}

type resetPasswordReq struct {
	Password string `json:"password"`
}

func (h *AdminHandler) ResetPassword(c *gin.Context) {
	userID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req resetPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Password) < 8 || len(req.Password) > 72 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"password": "密码长度需在 8-72 位之间"}))
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	now := time.Now()
	// 管理员重置他人密码要求下次登录改密；自己给自己重置不置位，否则会把自己锁进改密流程。
	mustChange := actorID != userID
	err = h.db.Transaction(func(tx *gorm.DB) error {
		// 密码由管理员指定，接口不回传明文。
		if err := tx.Model(&model.PlatformUser{}).Where("id = ?", userID).Updates(map[string]any{
			"password_hash":        hash,
			"must_change_password": mustChange,
		}).Error; err != nil {
			return err
		}
		// 管理员重置密码：媒体令牌版本一并递增（差异清单 #9）。
		if err := h.identity.BumpMediaTokenVersionTx(tx, userID); err != nil {
			return err
		}
		if err := h.identity.RevokeSessionsTx(tx, userID, now); err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "user.password_reset", "user", userID.String(), c.GetString("request_id"), "",
			nil, gin.H{"mustChangePassword": mustChange})
	})
	if err != nil {
		slog.Error("重置密码失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	httpx.NoContent(c)
}

type adjustCreditsReq struct {
	Bucket       string `json:"bucket"`
	AmountMicros int64  `json:"amountMicros"`
	Note         string `json:"note"`
}

func (h *AdminHandler) AdjustCredits(c *gin.Context) {
	userID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req adjustCreditsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	fields := map[string]string{}
	if req.Bucket != billing.BucketPurchased && req.Bucket != billing.BucketGranted {
		fields["bucket"] = "bucket 只能是 purchased 或 granted"
	}
	if req.AmountMicros == 0 {
		fields["amountMicros"] = "金额不能为 0"
	}
	if strings.TrimSpace(req.Note) == "" {
		fields["note"] = "必须填写说明"
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	var account model.CreditAccount
	err := h.db.Transaction(func(tx *gorm.DB) error {
		updated, err := h.credits.Adjust(tx, userID, req.Bucket, req.AmountMicros, req.Note, actorID.String())
		if err != nil {
			return err
		}
		account = updated
		return h.audit.Record(tx, actorID, "user.credits_adjust", "user", userID.String(), c.GetString("request_id"), req.Note,
			nil, gin.H{"bucket": req.Bucket, "amountMicros": req.AmountMicros})
	})
	if err != nil {
		if errors.Is(err, billing.ErrInsufficientCredits) {
			errs.Abort(c, errs.ErrInsufficientCredits)
			return
		}
		slog.Error("调整点数失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"purchasedMicros": account.PurchasedMicros,
		"grantedMicros":   account.GrantedMicros,
	})
}

func (h *AdminHandler) RecalculateUsage(c *gin.Context) {
	userID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	used, err := h.usage.Recalculate(c.Request.Context(), userID)
	if err != nil {
		slog.Error("重算存储用量失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"storageBytes": used})
}

func (h *AdminHandler) ReclaimMedia(c *gin.Context) {
	userID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	dryRun := c.Query("dryRun") == "true"
	now := time.Now()
	plan, err := service.PlanDefFor(c.Request.Context(), h.db, userID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	report, err := h.cleanup.Reclaim(c.Request.Context(), userID, plan.RetentionDays, plan.StorageBytes, now, dryRun)
	if err != nil {
		slog.Error("清理媒体失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, report)
}

// ===== 总览 =====

func (h *AdminHandler) Stats(c *gin.Context) {
	now := time.Now()
	dayStart := now.UTC().Truncate(24 * time.Hour)
	var userTotal, userToday, generationToday, orderToday int64
	var storageTotal, creditsTotal, revenueToday int64
	h.db.Model(&model.PlatformUser{}).Count(&userTotal)
	h.db.Model(&model.PlatformUser{}).Where("created_at >= ?", dayStart).Count(&userToday)
	h.db.Model(&model.Generation{}).Where("created_at >= ?", dayStart).Count(&generationToday)
	h.db.Model(&model.Order{}).Where("status = ? AND paid_at >= ?", "paid", dayStart).Count(&orderToday)
	h.db.Model(&model.MediaFile{}).Select("COALESCE(SUM(bytes), 0)").Scan(&storageTotal)
	h.db.Model(&model.CreditAccount{}).Select("COALESCE(SUM(purchased_micros + granted_micros), 0)").Scan(&creditsTotal)
	h.db.Model(&model.Order{}).Where("status = ? AND paid_at >= ?", "paid", dayStart).
		Select("COALESCE(SUM(price_micros), 0)").Scan(&revenueToday)
	c.JSON(http.StatusOK, gin.H{
		"userTotal":          userTotal,
		"userToday":          userToday,
		"storageBytes":       storageTotal,
		"generationToday":    generationToday,
		"orderToday":         orderToday,
		"revenueMicrosToday": revenueToday,
		"creditsTotal":       creditsTotal,
	})
}

// ===== 模型目录 =====

func (h *AdminHandler) ListModels(c *gin.Context) {
	var models []model.ModelCatalog
	if err := h.db.Order("sort ASC, name ASC").Find(&models).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]service.ModelSummary, 0, len(models))
	for _, item := range models {
		items = append(items, service.NewModelSummary(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type modelReq struct {
	Name              string          `json:"name"`
	DisplayName       string          `json:"displayName"`
	Capability        string          `json:"capability"`
	Provider          string          `json:"provider"`
	Constraints       json.RawMessage `json:"constraints"`
	CreditCost        json.RawMessage `json:"creditCost"`
	ChannelIDs        []string        `json:"channelIds"`
	FreeTrialEligible *bool           `json:"freeTrialEligible"`
	Enabled           *bool           `json:"enabled"`
	Sort              *int            `json:"sort"`
}

func (h *AdminHandler) CreateModel(c *gin.Context) {
	var req modelReq
	if err := c.ShouldBindJSON(&req); err != nil || !validModelReq(&req) {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	constraints, err := service.ParseConstraints(datatypes.JSON(req.Constraints))
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"constraints": err.Error()}))
		return
	}
	cost, err := service.ParseCreditCost(datatypes.JSON(req.CreditCost))
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"creditCost": err.Error()}))
		return
	}
	if err := service.ValidateCreditCost(cost, constraints); err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"creditCost": err.Error()}))
		return
	}
	var count int64
	h.db.Model(&model.ModelCatalog{}).Where("name = ?", req.Name).Count(&count)
	if count > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"name": "模型标识已存在"}))
		return
	}
	channelIDs, err := encodeChannelIDs(req.ChannelIDs)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"channelIds": "channelIds 必须是合法的渠道 id 列表"}))
		return
	}
	item := model.ModelCatalog{
		ID:                uuid.New(),
		Name:              req.Name,
		DisplayName:       req.DisplayName,
		Capability:        req.Capability,
		Provider:          req.Provider,
		Constraints:       datatypes.JSON(req.Constraints),
		CreditCost:        datatypes.JSON(req.CreditCost),
		ChannelIDs:        channelIDs,
		FreeTrialEligible: req.FreeTrialEligible != nil && *req.FreeTrialEligible,
		Enabled:           req.Enabled == nil || *req.Enabled,
	}
	if req.Sort != nil {
		item.Sort = *req.Sort
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "model.create", "model", item.ID.String(), c.GetString("request_id"), "", nil, modelSummaryAudit(item))
	})
	if err != nil {
		if service.IsDuplicateKey(err) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"name": "模型标识已存在"}))
			return
		}
		slog.Error("新增模型失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, service.NewModelSummary(item))
}

func (h *AdminHandler) UpdateModel(c *gin.Context) {
	modelID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req modelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	var item model.ModelCatalog
	if err := h.db.First(&item, "id = ?", modelID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	before := modelSummaryAudit(item)
	if req.Name != "" {
		var count int64
		h.db.Model(&model.ModelCatalog{}).Where("name = ? AND id <> ?", req.Name, modelID).Count(&count)
		if count > 0 {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"name": "模型标识已存在"}))
			return
		}
		item.Name = req.Name
	}
	if req.DisplayName != "" {
		item.DisplayName = req.DisplayName
	}
	if req.Capability != "" {
		if !validCapability(req.Capability) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"capability": "capability 取值非法"}))
			return
		}
		item.Capability = req.Capability
	}
	if req.Provider != "" {
		item.Provider = req.Provider
	}
	if len(req.Constraints) > 0 {
		if _, err := service.ParseConstraints(datatypes.JSON(req.Constraints)); err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"constraints": err.Error()}))
			return
		}
		item.Constraints = datatypes.JSON(req.Constraints)
	}
	if len(req.CreditCost) > 0 {
		if _, err := service.ParseCreditCost(datatypes.JSON(req.CreditCost)); err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"creditCost": err.Error()}))
			return
		}
		item.CreditCost = datatypes.JSON(req.CreditCost)
	}
	if req.ChannelIDs != nil {
		channelIDs, err := encodeChannelIDs(req.ChannelIDs)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"channelIds": "channelIds 必须是合法的渠道 id 列表"}))
			return
		}
		item.ChannelIDs = channelIDs
	}
	// 两个 JSON 字段改过任意一个都要重新校验组合合法性。
	constraints, err := service.ParseConstraints(item.Constraints)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"constraints": err.Error()}))
		return
	}
	cost, err := service.ParseCreditCost(item.CreditCost)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"creditCost": err.Error()}))
		return
	}
	if err := service.ValidateCreditCost(cost, constraints); err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"creditCost": err.Error()}))
		return
	}
	if req.FreeTrialEligible != nil {
		item.FreeTrialEligible = *req.FreeTrialEligible
	}
	if req.Enabled != nil {
		item.Enabled = *req.Enabled
	}
	if req.Sort != nil {
		item.Sort = *req.Sort
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&item).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "model.update", "model", item.ID.String(), c.GetString("request_id"), "", before, modelSummaryAudit(item))
	})
	if err != nil {
		slog.Error("更新模型失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, service.NewModelSummary(item))
}

func (h *AdminHandler) DeleteModel(c *gin.Context) {
	modelID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var item model.ModelCatalog
	if err := h.db.First(&item, "id = ?", modelID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var used int64
	h.db.Model(&model.CreditTransaction{}).Where("note = ?", item.Name).Count(&used)
	if used > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"id": "该模型已产生过流水，只能下架，不能删除"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("model_id = ?", modelID).Delete(&model.ModelPricePromotion{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", modelID).Delete(&model.ModelCatalog{}).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "model.delete", "model", modelID.String(), c.GetString("request_id"), "", modelSummaryAudit(item), nil)
	})
	if err != nil {
		slog.Error("删除模型失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	httpx.NoContent(c)
}

func modelSummaryAudit(item model.ModelCatalog) gin.H {
	return gin.H{
		"name":              item.Name,
		"displayName":       item.DisplayName,
		"capability":        item.Capability,
		"provider":          item.Provider,
		"channelIds":        json.RawMessage(item.ChannelIDs),
		"freeTrialEligible": item.FreeTrialEligible,
		"enabled":           item.Enabled,
		"sort":              item.Sort,
	}
}

// ===== 折扣活动 =====

func (h *AdminHandler) ListPromotions(c *gin.Context) {
	query := h.db.Model(&model.ModelPricePromotion{})
	if modelID := c.Query("modelId"); modelID != "" {
		id, err := uuid.Parse(modelID)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"modelId": "modelId 不合法"}))
			return
		}
		query = query.Where("model_id = ?", id)
	}
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	var promotions []model.ModelPricePromotion
	if err := query.Order("created_at DESC").Find(&promotions).Error; err != nil {
		slog.Error("读取折扣活动失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(promotions))
	for i := range promotions {
		items = append(items, promotionPayload(&promotions[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type promotionReq struct {
	ModelID     string          `json:"modelId"`
	Name        string          `json:"name"`
	MatchParams json.RawMessage `json:"matchParams"`
	DiscountBPS *int            `json:"discountBps"`
	Priority    *int            `json:"priority"`
	StartsAt    string          `json:"startsAt"`
	EndsAt      string          `json:"endsAt"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
}

func (h *AdminHandler) CreatePromotion(c *gin.Context) {
	var req promotionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	modelID, err := uuid.Parse(req.ModelID)
	if err != nil || req.Name == "" || req.DiscountBPS == nil || req.StartsAt == "" || req.EndsAt == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"startsAt": "时间格式必须是 RFC3339"}))
		return
	}
	endsAt, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"endsAt": "时间格式必须是 RFC3339"}))
		return
	}
	promotion := model.ModelPricePromotion{
		ID:          uuid.New(),
		ModelID:     modelID,
		Name:        req.Name,
		MatchParams: datatypes.JSON(req.MatchParams),
		DiscountBPS: *req.DiscountBPS,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
	}
	if req.Priority != nil {
		promotion.Priority = *req.Priority
	}
	if err := h.catalog.ValidatePromotion(c.Request.Context(), promotion); err != nil {
		if errors.Is(err, service.ErrPromotionConflict) {
			errs.Abort(c, errs.ErrPromotionConflict)
			return
		}
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"promotion": err.Error()}))
		return
	}
	promotion.Status = promotionStatus(promotion, time.Now())
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&promotion).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "promotion.create", "promotion", promotion.ID.String(), c.GetString("request_id"), req.Reason, nil, promotionAudit(promotion))
	})
	if err != nil {
		slog.Error("创建折扣活动失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, promotionPayload(&promotion))
}

func (h *AdminHandler) UpdatePromotion(c *gin.Context) {
	promotionID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req promotionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	var promotion model.ModelPricePromotion
	if err := h.db.First(&promotion, "id = ?", promotionID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	before := promotionAudit(promotion)
	if req.Name != "" {
		promotion.Name = req.Name
	}
	if len(req.MatchParams) > 0 {
		promotion.MatchParams = datatypes.JSON(req.MatchParams)
	}
	if req.DiscountBPS != nil {
		promotion.DiscountBPS = *req.DiscountBPS
	}
	if req.Priority != nil {
		promotion.Priority = *req.Priority
	}
	if req.StartsAt != "" {
		startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"startsAt": "时间格式必须是 RFC3339"}))
			return
		}
		promotion.StartsAt = startsAt
	}
	if req.EndsAt != "" {
		endsAt, err := time.Parse(time.RFC3339, req.EndsAt)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"endsAt": "时间格式必须是 RFC3339"}))
			return
		}
		promotion.EndsAt = endsAt
	}
	// 停用是显式状态，不靠把结束时间改到当前时间来伪造。
	if req.Status == "disabled" {
		promotion.Status = "disabled"
	}
	// 每次修改递增版本，历史版本可查询，不能直接覆盖。
	promotion.Version++
	if err := h.catalog.ValidatePromotion(c.Request.Context(), promotion); err != nil {
		if errors.Is(err, service.ErrPromotionConflict) {
			errs.Abort(c, errs.ErrPromotionConflict)
			return
		}
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"promotion": err.Error()}))
		return
	}
	if promotion.Status != "disabled" {
		promotion.Status = promotionStatus(promotion, time.Now())
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&promotion).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "promotion.update", "promotion", promotion.ID.String(), c.GetString("request_id"), req.Reason, before, promotionAudit(promotion))
	})
	if err != nil {
		slog.Error("更新折扣活动失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, promotionPayload(&promotion))
}

func promotionStatus(promotion model.ModelPricePromotion, now time.Time) string {
	switch {
	case now.Before(promotion.StartsAt):
		return "scheduled"
	case now.Before(promotion.EndsAt):
		return "active"
	default:
		return "ended"
	}
}

func promotionPayload(promotion *model.ModelPricePromotion) gin.H {
	return gin.H{
		"id":          promotion.ID.String(),
		"modelId":     promotion.ModelID.String(),
		"name":        promotion.Name,
		"matchParams": json.RawMessage(promotion.MatchParams),
		"discountBps": promotion.DiscountBPS,
		"priority":    promotion.Priority,
		"version":     promotion.Version,
		"status":      promotion.Status,
		"startsAt":    httpx.FormatTime(promotion.StartsAt),
		"endsAt":      httpx.FormatTime(promotion.EndsAt),
		"createdAt":   httpx.FormatTime(promotion.CreatedAt),
		"updatedAt":   httpx.FormatTime(promotion.UpdatedAt),
	}
}

func promotionAudit(promotion model.ModelPricePromotion) gin.H {
	return gin.H{
		"name":        promotion.Name,
		"discountBps": promotion.DiscountBPS,
		"priority":    promotion.Priority,
		"version":     promotion.Version,
		"status":      promotion.Status,
		"startsAt":    httpx.FormatTime(promotion.StartsAt),
		"endsAt":      httpx.FormatTime(promotion.EndsAt),
	}
}

// ===== 充值档位 =====

func (h *AdminHandler) ListPackages(c *gin.Context) {
	var packs []model.CreditPackage
	if err := h.db.Order("sort ASC, id ASC").Find(&packs).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(packs))
	for _, pack := range packs {
		items = append(items, packagePayload(pack))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// packagePayload 与用户侧 GET /api/credit-packages 保持同一字段形状。
// 点数包不再承载会员时长（D5）：entitlementDays 键保留形状、固定输出 0。
func packagePayload(pack model.CreditPackage) gin.H {
	return gin.H{
		"id":              pack.ID,
		"name":            pack.Name,
		"priceMicros":     pack.PriceMicros,
		"purchasedMicros": pack.PriceMicros,
		"bonusMicros":     pack.BonusMicros,
		"entitlementDays": 0,
		"currency":        pack.Currency,
		"enabled":         pack.Enabled,
		"sort":            pack.Sort,
	}
}

type packageReq struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceMicros     *int64 `json:"priceMicros"`
	BonusMicros     *int64 `json:"bonusMicros"`
	EntitlementDays *int   `json:"entitlementDays"`
	Enabled         *bool  `json:"enabled"`
	Sort            *int   `json:"sort"`
}

func (h *AdminHandler) CreatePackage(c *gin.Context) {
	var req packageReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" || req.Name == "" || req.PriceMicros == nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if err := validatePackage(req.PriceMicros, req.BonusMicros); err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"priceMicros": err.Error()}))
		return
	}
	pack := model.CreditPackage{
		ID:          req.ID,
		Name:        req.Name,
		PriceMicros: *req.PriceMicros,
		Currency:    "CNY",
		Enabled:     req.Enabled == nil || *req.Enabled,
	}
	if req.BonusMicros != nil {
		pack.BonusMicros = *req.BonusMicros
	}
	if req.Sort != nil {
		pack.Sort = *req.Sort
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pack).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "package.create", "credit_package", pack.ID, c.GetString("request_id"), "", nil, packageAudit(pack))
	})
	if err != nil {
		if service.IsDuplicateKey(err) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"id": "档位标识已存在"}))
			return
		}
		slog.Error("新增充值档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, packagePayload(pack))
}

func (h *AdminHandler) UpdatePackage(c *gin.Context) {
	packID := c.Param("id")
	var req packageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	var pack model.CreditPackage
	if err := h.db.First(&pack, "id = ?", packID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	before := packageAudit(pack)
	if req.Name != "" {
		pack.Name = req.Name
	}
	if req.PriceMicros != nil {
		pack.PriceMicros = *req.PriceMicros
	}
	if req.BonusMicros != nil {
		pack.BonusMicros = *req.BonusMicros
	}
	if err := validatePackage(&pack.PriceMicros, &pack.BonusMicros); err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"priceMicros": err.Error()}))
		return
	}
	if req.Enabled != nil {
		pack.Enabled = *req.Enabled
	}
	if req.Sort != nil {
		pack.Sort = *req.Sort
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&pack).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "package.update", "credit_package", pack.ID, c.GetString("request_id"), "", before, packageAudit(pack))
	})
	if err != nil {
		slog.Error("更新充值档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, packagePayload(pack))
}

func validatePackage(price *int64, bonus *int64) error {
	if *price <= 0 {
		return errors.New("价格必须大于 0")
	}
	// 提交给支付渠道时换算成分，价格必须是整分。
	if *price%10000 != 0 {
		return errors.New("价格必须是 10000 微元（一分）的整数倍")
	}
	if bonus != nil && *bonus < 0 {
		return errors.New("赠送点数不能为负数")
	}
	return nil
}

func packageAudit(pack model.CreditPackage) gin.H {
	return gin.H{
		"name":        pack.Name,
		"priceMicros": pack.PriceMicros,
		"bonusMicros": pack.BonusMicros,
		"enabled":     pack.Enabled,
		"sort":        pack.Sort,
	}
}

// ===== 订单 =====

var adminOrderSorts = map[string]string{
	"createdAt":   "created_at",
	"priceMicros": "price_micros",
}

func (h *AdminHandler) ListOrders(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	sortColumn, ok := httpx.ParseSort(c.Query("sort"), adminOrderSorts, "-createdAt")
	if !ok {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"sort": "sort 不在白名单内"}))
		return
	}
	query := h.db.Model(&model.Order{})
	if status := c.Query("status"); status != "" {
		if !validOrderStatusValue(status) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
			return
		}
		query = query.Where("status = ?", status)
	}
	if provider := c.Query("provider"); provider != "" {
		query = query.Where("provider = ?", provider)
	}
	if raw := c.Query("userId"); raw != "" {
		userID, err := uuid.Parse(raw)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"userId": "userId 不合法"}))
			return
		}
		query = query.Where("user_id = ?", userID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var orders []model.Order
	if err := query.Order(sortColumn).Order("id ASC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Find(&orders).Error; err != nil {
		slog.Error("查询订单列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(orders))
	for i := range orders {
		payload := billingapi.OrderPayload(&orders[i])
		payload["userId"] = orders[i].UserID.String()
		items = append(items, payload)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

func validOrderStatusValue(status string) bool {
	switch status {
	case "pending", "paid", "failed", "refunded":
		return true
	default:
		return false
	}
}

// ===== 公共辅助 =====

func parseUUIDParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func validCapability(capability string) bool {
	switch capability {
	case "image", "video", "text", "audio":
		return true
	default:
		return false
	}
}

// encodeChannelIDs 校验并序列化渠道绑定；空列表也序列化成 []，便于清空绑定。
func encodeChannelIDs(ids []string) (datatypes.JSON, error) {
	if ids == nil {
		return nil, nil
	}
	parsed := make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, id)
	}
	return datatypes.JSON(json.RawMessage(mustJSON(parsed))), nil
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

func validModelReq(req *modelReq) bool {
	return req.Name != "" && req.DisplayName != "" && validCapability(req.Capability) && req.Provider != ""
}
