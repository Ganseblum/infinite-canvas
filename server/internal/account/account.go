// Package account 是认证与身份面：注册登录、会话签发与轮换、邮箱验证、
// 密码找回、个人资料、数据导出、免费赠送与注销冷静期，另含本服务对外
// 的 OIDC Provider。对外路由由 routes.go 的 MountAuthRoutes 与 main.go
// 挂载的 me/oidc 分组组成。
package account

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/platform/membership"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/service"
)

// AccountHandler 处理 /api/me 侧的身份接口：资料读写、数据导出、改密、
// 免费赠送领取与注销冷静期。聚合点数、会员、存储用量等服务做一次性展示。
type AccountHandler struct {
	db         *gorm.DB
	cfg        *config.Config
	grant      *service.FreeGrantService
	authH      *AuthHandler
	credits    *billing.Service
	membership *membership.Service
	usage      *platformstorage.Service
	quota      *service.QuotaService
	identity   *identity.Service
}

func NewAccountHandler(db *gorm.DB, cfg *config.Config, grant *service.FreeGrantService, authH *AuthHandler) *AccountHandler {
	return &AccountHandler{
		db:         db,
		cfg:        cfg,
		grant:      grant,
		authH:      authH,
		credits:    billing.NewService(db, model.ProductCanvas),
		membership: membership.NewService(db),
		usage:      platformstorage.NewService(db, model.ProductCanvas),
		quota:      service.NewQuotaService(db),
		identity:   identity.NewService(db),
	}
}

// latestPaidUntil 返回该用户最新订阅的 period_end；无订阅时为 nil。
// paidUntil 键的值来源由 credits.paid_until 迁移到订阅周期（D6）。
// GetMe 一次聚合当前用户档案、有效档位、点数余额、存储用量、媒体到期与
// 注销状态；存储用量超过档位上限时置 readOnly。
func (h *AccountHandler) GetMe(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	now := time.Now()
	planID, graceEndsAt, err := h.membership.ActivePlan(c.Request.Context(), uid, now)
	if err != nil {
		slog.Error("读取档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	plan, err := h.membership.PlanDef(c.Request.Context(), planID)
	if err != nil {
		slog.Error("读取档位定义失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	used, _, err := h.usage.Snapshot(c.Request.Context(), uid)
	if err != nil {
		slog.Error("读取存储用量失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	readOnly := used > plan.StorageBytes
	expiry, err := h.quota.ExpiringMedia(c.Request.Context(), uid, plan.RetentionDays, now)
	if err != nil {
		slog.Error("计算媒体到期时间失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	imageTrials, _ := h.usageValue(uid, service.MetricFreeImageTrial)
	videoTrials, _ := h.usageValue(uid, service.MetricFreeVideoTrial)
	account, err := h.credits.Balance(c.Request.Context(), uid)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("读取点数余额失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	deletion := gin.H{"status": "none", "scheduledAt": nil}
	if user.Status == "pending_deletion" {
		deletion = gin.H{"status": "pending", "scheduledAt": httpx.FormatTimePtr(user.DeletionScheduledAt)}
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":            user.ID.String(),
			"email":         user.Email,
			"username":      user.Username,
			"displayName":   user.DisplayName,
			"avatarUrl":     user.AvatarURL,
			"role":          user.Role,
			"emailVerified": user.EmailVerifiedAt != nil,
			"createdAt":     user.CreatedAt.UTC().Format(time.RFC3339Nano),
		},
		"plan": gin.H{
			"id":            plan.ID,
			"name":          plan.Name,
			"storageBytes":  plan.StorageBytes,
			"maxFileBytes":  plan.MaxFileBytes,
			"retentionDays": plan.RetentionDays,
		},
		"credits": gin.H{
			"purchasedMicros": account.PurchasedMicros,
			"grantedMicros":   account.GrantedMicros,
			"totalMicros":     account.PurchasedMicros + account.GrantedMicros,
			"paidUntil":       httpx.FormatTimePtr(membership.LatestPaidUntil(h.db, uid)),
		},
		"usage": gin.H{
			"storageBytes":        used,
			"freeImageTrialsUsed": imageTrials,
			"freeVideoTrialsUsed": videoTrials,
		},
		"mediaExpiry": gin.H{
			"nearestAt":     httpx.FormatTimePtr(expiry.NearestAt),
			"expiringCount": expiry.ExpiringCount,
		},
		"deletion":    deletion,
		"readOnly":    readOnly,
		"graceEndsAt": httpx.FormatTimePtr(graceEndsAt),
	})
}

func (h *AccountHandler) usageValue(uid uuid.UUID, metric string) (int64, error) {
	var record model.UsageRecord
	err := h.db.Where("user_id = ? AND metric = ? AND period = ?", uid, metric, service.PeriodTotal).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return record.Value, err
}

// ExportMe 导出当前用户的个人数据（差异清单 #123）：档案基本字段、点数余额、
// 订单列表、生成记录与画布列表的元数据；媒体二进制与画布正文 JSON 不在导出范围。
func (h *AccountHandler) ExportMe(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	credit, err := h.credits.Balance(c.Request.Context(), uid)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("导出读取点数余额失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var orders []model.Order
	if err := h.db.Where("user_id = ?", uid).Order("created_at DESC").Find(&orders).Error; err != nil {
		slog.Error("导出读取订单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var generations []model.Generation
	if err := h.db.Where("user_id = ?", uid).Order("created_at DESC").Find(&generations).Error; err != nil {
		slog.Error("导出读取生成记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var canvases []model.Canvas
	if err := h.db.Where("user_id = ?", uid).Order("updated_at DESC").Find(&canvases).Error; err != nil {
		slog.Error("导出读取画布列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	orderItems := make([]gin.H, 0, len(orders))
	for _, order := range orders {
		orderItems = append(orderItems, gin.H{
			"id":              order.ID.String(),
			"provider":        order.Provider,
			"packageId":       order.PackageID,
			"priceMicros":     order.PriceMicros,
			"currency":        order.Currency,
			"purchasedMicros": order.PurchasedMicros,
			"grantedMicros":   order.GrantedMicros,
			"status":          order.Status,
			"paidAt":          httpx.FormatTimePtr(order.PaidAt),
			"createdAt":       httpx.FormatTime(order.CreatedAt),
		})
	}
	generationItems := make([]gin.H, 0, len(generations))
	for _, item := range generations {
		generationItems = append(generationItems, gin.H{
			"id":               item.ID.String(),
			"kind":             item.Kind,
			"status":           item.Status,
			"prompt":           item.Prompt,
			"model":            item.Model,
			"durationMs":       item.DurationMs,
			"moderationStatus": item.ModerationStatus,
			"createdAt":        httpx.FormatTime(item.CreatedAt),
		})
	}
	canvasItems := make([]gin.H, 0, len(canvases))
	for _, canvas := range canvases {
		canvasItems = append(canvasItems, gin.H{
			"id":              canvas.ID.String(),
			"title":           canvas.Title,
			"nodeCount":       canvas.NodeCount,
			"connectionCount": canvas.ConnectionCount,
			"createdAt":       httpx.FormatTime(canvas.CreatedAt),
			"updatedAt":       httpx.FormatTime(canvas.UpdatedAt),
		})
	}
	c.Header("Content-Disposition", `attachment; filename="youc-export.json"`)
	c.JSON(http.StatusOK, gin.H{
		"exportedAt": httpx.FormatTime(time.Now()),
		"profile": gin.H{
			"id":            user.ID.String(),
			"email":         user.Email,
			"username":      user.Username,
			"displayName":   user.DisplayName,
			"avatarUrl":     user.AvatarURL,
			"emailVerified": user.EmailVerifiedAt != nil,
			"createdAt":     httpx.FormatTime(user.CreatedAt),
		},
		"credits": gin.H{
			"purchasedMicros": credit.PurchasedMicros,
			"grantedMicros":   credit.GrantedMicros,
		},
		"orders":      orderItems,
		"generations": generationItems,
		"canvases":    canvasItems,
	})
}

type updateMeReq struct {
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// UpdateMe 局部更新展示名与头像，两个字段都不传按 400 处理。
func (h *AccountHandler) UpdateMe(c *gin.Context) {
	var req updateMeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	updates := map[string]interface{}{}
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.AvatarURL != nil {
		updates["avatar_url"] = *req.AvatarURL
	}
	if len(updates) == 0 {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if err := h.db.Model(&model.PlatformUser{}).Where("id = ?", uid).Updates(updates).Error; err != nil {
		slog.Error("更新资料失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var user model.PlatformUser
	h.db.First(&user, "id = ?", uid)
	c.JSON(http.StatusOK, gin.H{"user": userPayload(&user)})
}

type changePasswordReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// ChangePassword 校验旧密码后改密，撤销该用户全部会话并重发当前会话 cookie。
func (h *AccountHandler) ChangePassword(c *gin.Context) {
	var req changePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if len(req.NewPassword) < 8 || len(req.NewPassword) > 72 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"newPassword": "新密码长度需在 8-72 位之间"}))
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.OldPassword) {
		errs.Abort(c, errs.ErrInvalidCreds)
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	now := time.Now()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		// 改密同时清掉强制改密标记：这是该标志唯一的清除点。
		if err := tx.Model(&user).Updates(map[string]any{
			"password_hash":        hash,
			"must_change_password": false,
		}).Error; err != nil {
			return err
		}
		// 撤销该用户全部 refresh token（含当前会话），旧令牌一律失效；
		// 同时递增媒体令牌版本，ic_media cookie 随之失效（差异清单 #9）。
		if err := h.identity.BumpMediaTokenVersionTx(tx, uid); err != nil {
			return err
		}
		return h.identity.RevokeSessionsTx(tx, uid, now)
	})
	if err != nil {
		slog.Error("修改密码失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	// 下发新的 refresh cookie 保持当前会话
	accessToken := h.authH.issueSession(c, &user)
	c.Header("X-Session-Refreshed", "true")
	_ = accessToken
	c.Status(http.StatusNoContent)
}

// ClaimFreeGrant 领取免费赠送：开关、邮箱已验证、风控评分与每日预算层层放行，
// 被拒也落 denied 记录；同一活动重复领取幂等返回既有结论。
func (h *AccountHandler) ClaimFreeGrant(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	if !h.cfg.FreeGrantEnabled {
		errs.Abort(c, errs.ErrFreeGrantUnav)
		return
	}
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if user.EmailVerifiedAt == nil {
		errs.Abort(c, errs.ErrEmailNotVerif)
		return
	}

	// 幂等：同一用户同一活动已领取则返回既有结论
	var existing model.FreeGrantClaim
	err := h.db.Where("user_id = ? AND campaign_id = ?", uid, h.cfg.FreeGrantCampaignID).First(&existing).Error
	if err == nil {
		c.JSON(http.StatusOK, grantPayload(existing))
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("查询赠送领取记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	// 风控评分
	ip := middleware.ClientIP(c)
	ua := c.Request.UserAgent()
	risk := h.grant.RiskScore(uid, ip, ua)
	if risk >= h.cfg.FreeGrantRiskThreshold {
		h.recordDenied(uid, "risk_threshold")
		errs.Abort(c, errs.ErrFreeGrantUnav)
		return
	}
	// 每日预算：按当日已发放领取数 × 单次估算成本与配置预算比较
	if !h.grant.WithinDailyBudget(h.cfg.FreeGrantDailyBudgetMicros, h.grant.EstimatePerClaimMicros()) {
		h.recordDenied(uid, "daily_budget")
		errs.Abort(c, errs.ErrFreeGrantUnav)
		return
	}

	claim := model.FreeGrantClaim{
		ID:         uuid.New(),
		UserID:     uid,
		CampaignID: h.cfg.FreeGrantCampaignID,
		Status:     "granted",
		Reason:     "",
	}
	if err := h.db.Create(&claim).Error; err != nil {
		// 唯一索引兜底并发
		if errs.IsDuplicateKey(err) {
			h.db.Where("user_id = ? AND campaign_id = ?", uid, h.cfg.FreeGrantCampaignID).First(&existing)
			c.JSON(http.StatusOK, grantPayload(existing))
			return
		}
		slog.Error("写入赠送领取记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, grantPayload(claim))
}

// recordDenied 落一条被拒的领取记录（风控/预算原因），供事后审计。
func (h *AccountHandler) recordDenied(uid uuid.UUID, reason string) {
	h.db.Create(&model.FreeGrantClaim{
		ID:         uuid.New(),
		UserID:     uid,
		CampaignID: h.cfg.FreeGrantCampaignID,
		Status:     "denied",
		Reason:     reason,
	})
}

type deletionReq struct {
	Password string `json:"password"`
}

// RequestDeletion 校验密码后把账号置为 pending_deletion，预约 7 天后的匿名化任务。
// 效果立即生效：生成与下单被 403 ACCOUNT_PENDING_DELETION 拦截；重复调用幂等，不重置倒计时。
func (h *AccountHandler) RequestDeletion(c *gin.Context) {
	var req deletionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		errs.Abort(c, errs.ErrInvalidCreds)
		return
	}
	scheduledAt, err := h.identity.RequestDeletion(c.Request.Context(), uid, time.Now())
	if err != nil {
		slog.Error("申请注销失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"scheduledAt": scheduledAt.UTC().Format(time.RFC3339Nano)})
}

// CancelDeletion 撤销注销申请，仅当处于冷静期时有效。
func (h *AccountHandler) CancelDeletion(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	if err := h.identity.CancelDeletion(c.Request.Context(), uid); err != nil {
		if errors.Is(err, identity.ErrNotPendingDeletion) {
			errs.Abort(c, errs.ErrDeletionNotPending)
			return
		}
		slog.Error("撤销注销失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "active"})
}

// grantPayload 输出赠送内容：试用次数与全局试用上限（FreeImageTrialLimit=3、
// FreeVideoTrialLimit=1）同源，grantedMicros 恒为 0——本期只登记领取，不改点数。
func grantPayload(claim model.FreeGrantClaim) gin.H {
	return gin.H{
		"campaignId":    claim.CampaignID,
		"status":        claim.Status,
		"imageTrials":   3,
		"videoTrials":   1,
		"grantedMicros": 0,
	}
}
