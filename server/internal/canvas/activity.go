package canvas

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/service"
)

// inviteMinAccountAge 是被邀请人注册后允许绑定邀请的最短时长：
// 给批量注册-立刻绑定的小号留一道时间门槛，与邮箱验证叠加抬高刷号成本。
const inviteMinAccountAge = time.Hour

// ActivityHandler 提供签到与邀请返利。两种奖励都只进 granted 桶，不改变付费身份。
type ActivityHandler struct {
	db       *gorm.DB
	settings *service.SiteSettingService
	credits  *billing.Service
	grant    *service.FreeGrantService
	cfg      *config.Config
}

func NewActivityHandler(db *gorm.DB, settings *service.SiteSettingService, grant *service.FreeGrantService, cfg *config.Config) *ActivityHandler {
	return &ActivityHandler{db: db, settings: settings, credits: billing.NewService(db, model.ProductCanvas), grant: grant, cfg: cfg}
}

func (h *ActivityHandler) checkinEnabled() bool {
	if h.settings == nil {
		return true
	}
	return h.settings.CheckinEnabled()
}

func (h *ActivityHandler) inviteEnabled() bool {
	if h.settings == nil {
		return true
	}
	return h.settings.InviteEnabled()
}

// CheckinStatus 返回今日是否已签到与连续天数。
func (h *ActivityHandler) CheckinStatus(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	today := time.Now().UTC().Format("2006-01-02")
	var record model.CheckinRecord
	err := h.db.Where("user_id = ? AND checkin_date = ?", uid, today).First(&record).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var last model.CheckinRecord
	h.db.Where("user_id = ?", uid).Order("checkin_date DESC").First(&last)
	payload := gin.H{
		"enabled":      h.checkinEnabled(),
		"checkedIn":    err == nil,
		"streakDays":   0,
		"rewardMicros": h.checkinReward(),
		"today":        today,
	}
	if err == nil {
		payload["streakDays"] = record.StreakDays
		payload["rewardMicros"] = record.RewardMicros
	} else if last.ID != uuid.Nil {
		payload["streakDays"] = last.StreakDays
	}
	c.JSON(http.StatusOK, payload)
}

// Checkin 执行签到。同一天重复调用幂等，不重复发放。
func (h *ActivityHandler) Checkin(c *gin.Context) {
	if !h.checkinEnabled() {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"checkin": "签到暂未开放"}))
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	today := time.Now().UTC().Format("2006-01-02")
	reward := h.checkinReward()

	var record model.CheckinRecord
	var granted bool
	err := h.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user_id = ? AND checkin_date = ?", uid, today).First(&record).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		streak := h.streakDays(tx, uid, today)
		record = model.CheckinRecord{
			ID:           uuid.New(),
			UserID:       uid,
			CheckinDate:  today,
			RewardMicros: reward,
			StreakDays:   streak,
			CreatedAt:    time.Now(),
		}
		if err := tx.Create(&record).Error; err != nil {
			if billing.IsDuplicateKey(err) {
				return tx.Where("user_id = ? AND checkin_date = ?", uid, today).First(&record).Error
			}
			return err
		}
		if reward > 0 {
			if _, err := h.credits.Adjust(tx, uid, billing.BucketGranted, reward, "每日签到", "system"); err != nil {
				return err
			}
		}
		granted = true
		return nil
	})
	if err != nil {
		slog.Error("签到失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"checkedIn":    true,
		"granted":      granted,
		"rewardMicros": record.RewardMicros,
		"streakDays":   record.StreakDays,
	})
}

func (h *ActivityHandler) checkinReward() int64 {
	if h.settings == nil {
		return 20000
	}
	return h.settings.CheckinRewardMicros()
}

// streakDays 计算连续签到天数：昨天签过就 +1，否则从 1 重新开始。
func (h *ActivityHandler) streakDays(tx *gorm.DB, uid uuid.UUID, today string) int {
	yesterday, _ := time.Parse("2006-01-02", today)
	yesterday = yesterday.AddDate(0, 0, -1)
	var last model.CheckinRecord
	if err := tx.Where("user_id = ? AND checkin_date = ?", uid, yesterday.Format("2006-01-02")).First(&last).Error; err == nil {
		return last.StreakDays + 1
	}
	return 1
}

// InviteInfo 返回当前用户的邀请码、邀请记录与奖励配置。
func (h *ActivityHandler) InviteInfo(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	code := ""
	if user.InviteCode != nil {
		code = *user.InviteCode
	}
	// 老账号或注册时未生成时懒补一次，保证每个用户都有可分享的邀请码。
	if code == "" {
		for attempt := 0; attempt < 3; attempt++ {
			candidate := randomInviteCode()
			if err := h.db.Model(&model.PlatformUser{}).Where("id = ?", uid).Update("invite_code", candidate).Error; err != nil {
				if billing.IsDuplicateKey(err) {
					continue
				}
				break
			}
			code = candidate
			break
		}
	}
	var invites []model.UserInvite
	h.db.Where("inviter_id = ?", uid).Order("created_at DESC").Limit(100).Find(&invites)
	totalReward := int64(0)
	for _, invite := range invites {
		totalReward += invite.InviterRewardMicros
	}
	rewardMicros := int64(100000)
	if h.settings != nil {
		rewardMicros = h.settings.InviteRewardMicros()
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":               h.inviteEnabled(),
		"code":                  code,
		"invitedCount":          len(invites),
		"rewardMicros":          totalReward,
		"rewardPerInviteMicros": rewardMicros,
	})
}

// BindInvite 用邀请码绑定邀请关系。注册时调用一次，重复绑定返回冲突。
func (h *ActivityHandler) BindInvite(c *gin.Context) {
	if !h.inviteEnabled() {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"invite": "邀请返利暂未开放"}))
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	var inviter model.PlatformUser
	if err := h.db.Where("invite_code = ?", req.Code).First(&inviter).Error; err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"code": "邀请码不存在"}))
		return
	}
	if inviter.ID == uid {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"code": "不能使用自己的邀请码"}))
		return
	}
	// 防刷（差异清单 #35）：被邀请人必须邮箱已验证且注册满最短时长，
	// 并与免费领取共用同一套风控评分与每日预算，补前端入口前先补风控。
	var invitee model.PlatformUser
	if err := h.db.First(&invitee, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if invitee.EmailVerifiedAt == nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"invite": "请先验证邮箱再绑定邀请码"}))
		return
	}
	if time.Since(invitee.CreatedAt) < inviteMinAccountAge {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"invite": "注册时间过短，暂时不能绑定邀请码"}))
		return
	}
	if h.grant != nil && h.cfg != nil {
		risk := h.grant.RiskScore(uid, middleware.ClientIP(c), c.Request.UserAgent())
		if risk >= h.cfg.FreeGrantRiskThreshold {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"invite": "暂时无法绑定邀请码，请稍后再试"}))
			return
		}
	}
	var existing model.UserInvite
	if err := h.db.Where("invitee_id = ?", uid).First(&existing).Error; err == nil {
		errs.Abort(c, errs.AddConflict("你已经绑定过邀请关系"))
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	inviterReward := int64(100000)
	inviteeReward := int64(50000)
	if h.settings != nil {
		inviterReward = h.settings.InviteRewardMicros()
		inviteeReward = h.settings.InviteeRewardMicros()
	}
	if h.cfg != nil && !h.withinInviteDailyBudget(inviterReward+inviteeReward) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"invite": "今日邀请名额已用完，请明天再试"}))
		return
	}
	invite := model.UserInvite{
		Code:                *inviter.InviteCode,
		InviterID:           inviter.ID,
		InviteeID:           uid,
		InviterRewardMicros: inviterReward,
		InviteeRewardMicros: inviteeReward,
		CreatedAt:           time.Now(),
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&invite).Error; err != nil {
			return err
		}
		if inviterReward > 0 {
			if _, err := h.credits.Adjust(tx, inviter.ID, billing.BucketGranted, inviterReward, "邀请返利", "system"); err != nil {
				return err
			}
		}
		if inviteeReward > 0 {
			if _, err := h.credits.Adjust(tx, uid, billing.BucketGranted, inviteeReward, "受邀奖励", "system"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if billing.IsDuplicateKey(err) {
			errs.Abort(c, errs.AddConflict("你已经绑定过邀请关系"))
			return
		}
		slog.Error("绑定邀请关系失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"inviterRewardMicros": inviterReward,
		"inviteeRewardMicros": inviteeReward,
	})
}

// withinInviteDailyBudget 与免费领取共用 FREE_GRANT_DAILY_BUDGET_MICROS：
// 当日已发出的邀请奖励与免费领取估算成本合并计入，超出则拒绝本次绑定。
// 统计落库（而非进程内存），重启与多实例口径一致，日界按 UTC+8。
// randomInviteCode 生成短邀请码，使用 URL 安全字符集。
func randomInviteCode() string {
	buffer := make([]byte, 9)
	if _, err := rand.Read(buffer); err != nil {
		return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func (h *ActivityHandler) withinInviteDailyBudget(bindCostMicros int64) bool {
	if h.cfg.FreeGrantDailyBudgetMicros <= 0 {
		return false
	}
	if bindCostMicros <= 0 {
		return true
	}
	now := time.Now().In(model.StatZone)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, model.StatZone)
	dayEnd := dayStart.Add(24 * time.Hour)
	var invites int64
	if err := h.db.Model(&model.UserInvite{}).
		Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).
		Count(&invites).Error; err != nil {
		slog.Error("统计当日邀请数失败，预算闸门按拒绝处理", "err", err)
		return false
	}
	spent := invites * bindCostMicros
	if h.grant != nil {
		if estimate := h.grant.EstimatePerClaimMicros(); estimate > 0 {
			var grantedClaims int64
			if err := h.db.Model(&model.FreeGrantClaim{}).
				Where("status = ? AND created_at >= ? AND created_at < ?", "granted", dayStart, dayEnd).
				Count(&grantedClaims).Error; err != nil {
				slog.Error("统计当日领取数失败，预算闸门按拒绝处理", "err", err)
				return false
			}
			spent += grantedClaims * estimate
		}
	}
	return spent+bindCostMicros <= h.cfg.FreeGrantDailyBudgetMicros
}
