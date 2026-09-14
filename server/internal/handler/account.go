package handler

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
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

type AccountHandler struct {
	db    *gorm.DB
	cfg   *config.Config
	grant *service.FreeGrantService
	authH *AuthHandler
}

func NewAccountHandler(db *gorm.DB, cfg *config.Config, grant *service.FreeGrantService, authH *AuthHandler) *AccountHandler {
	return &AccountHandler{db: db, cfg: cfg, grant: grant, authH: authH}
}

func (h *AccountHandler) GetMe(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.User
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	var plan model.Plan
	if err := h.db.First(&plan, "id = ?", "free").Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
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
	})
}

type updateMeReq struct {
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

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
	if err := h.db.Model(&model.User{}).Where("id = ?", uid).Updates(updates).Error; err != nil {
		slog.Error("更新资料失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var user model.User
	h.db.First(&user, "id = ?", uid)
	c.JSON(http.StatusOK, gin.H{"user": userPayload(&user)})
}

type changePasswordReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

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
	var user model.User
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
		if err := tx.Model(&user).Update("password_hash", hash).Error; err != nil {
			return err
		}
		// 撤销除当前会话外的全部 refresh token
		currentHash := ""
		if cookie, err := c.Cookie(RefreshCookieName); err == nil {
			currentHash = auth.HashToken(cookie)
		}
		q := tx.Model(&model.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", uid)
		if currentHash != "" {
			q = q.Where("token_hash <> ?", currentHash)
		}
		return q.Update("revoked_at", now).Error
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

func (h *AccountHandler) ClaimFreeGrant(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	if !h.cfg.FreeGrantEnabled {
		errs.Abort(c, errs.ErrFreeGrantUnav)
		return
	}
	var user model.User
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
	// 每日预算
	if !h.grant.WithinDailyBudget(h.cfg.FreeGrantDailyBudgetMicros) {
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
		if isDuplicateKey(err) {
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

func (h *AccountHandler) recordDenied(uid uuid.UUID, reason string) {
	h.db.Create(&model.FreeGrantClaim{
		ID:         uuid.New(),
		UserID:     uid,
		CampaignID: h.cfg.FreeGrantCampaignID,
		Status:     "denied",
		Reason:     reason,
	})
}

func grantPayload(claim model.FreeGrantClaim) gin.H {
	return gin.H{
		"campaignId":    claim.CampaignID,
		"status":        claim.Status,
		"imageTrials":   3,
		"videoTrials":   1,
		"grantedMicros": 0,
	}
}
