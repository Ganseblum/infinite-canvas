package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

// ===== 站点设置 =====

// PublicSettings 是无需登录即可读取的站点公开信息（公告、维护状态与可用功能）。
func (h *AdminHandler) PublicSettings(c *gin.Context) {
	if h.settings == nil {
		c.JSON(http.StatusOK, gin.H{})
		return
	}
	snapshot := h.settings.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"announcement":      snapshot.Announcement,
		"maintenanceMode":   snapshot.MaintenanceMode,
		"maintenanceNotice": snapshot.MaintenanceNotice,
		"communityEnabled":  snapshot.CommunityEnabled,
		"checkinEnabled":    snapshot.CheckinEnabled,
		"inviteEnabled":     snapshot.InviteEnabled,
	})
}

func (h *AdminHandler) GetSettings(c *gin.Context) {
	if h.settings == nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, h.settings.Snapshot())
}

type settingsReq struct {
	Announcement          *string `json:"announcement"`
	RegistrationEnabled   *bool   `json:"registrationEnabled"`
	MaintenanceMode       *bool   `json:"maintenanceMode"`
	MaintenanceNotice     *string `json:"maintenanceNotice"`
	CommunityEnabled      *bool   `json:"communityEnabled"`
	CheckinEnabled        *bool   `json:"checkinEnabled"`
	CheckinRewardMicros   *int64  `json:"checkinRewardMicros"`
	InviteEnabled         *bool   `json:"inviteEnabled"`
	InviteRewardMicros    *int64  `json:"inviteRewardMicros"`
	InviteeRewardMicros   *int64  `json:"inviteeRewardMicros"`
	MaxUploadBytes        *int64  `json:"maxUploadBytes"`
	GenerationConcurrency *int64  `json:"generationConcurrency"`
}

// UpdateSettings 批量更新站点设置，每一项都写审计。
func (h *AdminHandler) UpdateSettings(c *gin.Context) {
	if h.settings == nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var req settingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if req.CheckinRewardMicros != nil && *req.CheckinRewardMicros < 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"checkinRewardMicros": "赠送点数不能为负"}))
		return
	}
	if req.InviteRewardMicros != nil && *req.InviteRewardMicros < 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"inviteRewardMicros": "赠送点数不能为负"}))
		return
	}
	if req.InviteeRewardMicros != nil && *req.InviteeRewardMicros < 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"inviteeRewardMicros": "赠送点数不能为负"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	before := h.settings.Snapshot()
	updates := map[string]any{}
	if req.Announcement != nil {
		updates[service.SettingAnnouncement] = *req.Announcement
	}
	if req.RegistrationEnabled != nil {
		updates[service.SettingRegistrationEnabled] = *req.RegistrationEnabled
	}
	if req.MaintenanceMode != nil {
		updates[service.SettingMaintenanceMode] = *req.MaintenanceMode
	}
	if req.MaintenanceNotice != nil {
		updates[service.SettingMaintenanceNotice] = *req.MaintenanceNotice
	}
	if req.CommunityEnabled != nil {
		updates[service.SettingCommunityEnabled] = *req.CommunityEnabled
	}
	if req.CheckinEnabled != nil {
		updates[service.SettingCheckinEnabled] = *req.CheckinEnabled
	}
	if req.CheckinRewardMicros != nil {
		updates[service.SettingCheckinRewardMicros] = *req.CheckinRewardMicros
	}
	if req.InviteEnabled != nil {
		updates[service.SettingInviteEnabled] = *req.InviteEnabled
	}
	if req.InviteRewardMicros != nil {
		updates[service.SettingInviteRewardMicros] = *req.InviteRewardMicros
	}
	if req.InviteeRewardMicros != nil {
		updates[service.SettingInviteeRewardMicros] = *req.InviteeRewardMicros
	}
	if req.MaxUploadBytes != nil {
		updates[service.SettingMaxUploadBytes] = *req.MaxUploadBytes
	}
	if req.GenerationConcurrency != nil {
		updates[service.SettingGenerationConcurrency] = *req.GenerationConcurrency
	}
	if len(updates) == 0 {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	for key, value := range updates {
		if err := h.settings.Set(c.Request.Context(), key, value, &actorID); err != nil {
			slog.Error("写入站点设置失败", "key", key, "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
	}
	after := h.settings.Snapshot()
	_ = h.audit.Record(h.db, actorID, "settings.update", "site_setting", "site", c.GetString("request_id"), "",
		before, after)
	c.JSON(http.StatusOK, after)
}

// ===== 管理员管理（过渡期兼容接口，内部已按角色模型实现） =====

// ListAdmins 返回系统角色成员列表，附带注册时间与最近登录时间。
// 过渡期保留：等价于筛选 role_key = admin 的后台成员。
func (h *AdminHandler) ListAdmins(c *gin.Context) {
	var admins []model.PlatformUser
	if err := h.db.Where("role_key = ?", authz.SystemRoleKey).Order("created_at ASC").Find(&admins).Error; err != nil {
		slog.Error("读取管理员列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(admins))
	for i := range admins {
		items = append(items, gin.H{
			"id":          admins[i].ID.String(),
			"email":       admins[i].Email,
			"username":    admins[i].Username,
			"displayName": admins[i].DisplayName,
			"status":      admins[i].Status,
			"roleKey":     authz.SystemRoleKey,
			"createdAt":   formatTime(admins[i].CreatedAt),
			"lastLoginAt": formatTimePtr(admins[i].LastLoginAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type adminInviteReq struct {
	Email string `json:"email"`
}

// AddAdmin 依据邮箱把已有用户提升为系统角色成员。不创建新账号，避免绕过注册风控。
func (h *AdminHandler) AddAdmin(c *gin.Context) {
	var req adminInviteReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Email) == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	var user model.PlatformUser
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"email": "该邮箱尚未注册，请先让用户注册账号"}))
			return
		}
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if isSystemRoleKey(user.RoleKey) {
		errs.Abort(c, errs.AddConflict("该用户已经是管理员"))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	beforeName := h.roleDisplayName(user.RoleKey)
	roleKey := authz.SystemRoleKey
	afterName := h.roleDisplayName(&roleKey)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := authz.AssignRole(tx, user.ID, &roleKey); err != nil {
			return err
		}
		// 角色变更后撤销该用户全部 refresh token，避免旧会话沿用旧角色。
		if err := h.identity.RevokeSessionsTx(tx, user.ID, time.Now()); err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "user.role", "user", user.ID.String(), c.GetString("request_id"), "",
			userRoleAudit(user.RoleKey, beforeName), userRoleAudit(&roleKey, afterName))
	})
	if err != nil {
		slog.Error("提升管理员失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": user.ID.String(), "role": authz.SystemRoleKey, "roleKey": authz.SystemRoleKey})
}

// RemoveAdmin 撤销用户的系统角色。不允许撤销自己，也不允许移除最后一个 active 成员。
func (h *AdminHandler) RemoveAdmin(c *gin.Context) {
	targetID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	if targetID == actorID {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"id": "不能撤销自己的管理员权限"}))
		return
	}
	var target model.PlatformUser
	if err := h.db.First(&target, "id = ?", targetID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if !isSystemRoleKey(target.RoleKey) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	beforeName := h.roleDisplayName(target.RoleKey)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		remain, err := h.countActiveSystemMembers(tx, targetID)
		if err != nil {
			return err
		}
		if remain == 0 {
			return errLastSystemMember
		}
		if err := authz.AssignRole(tx, targetID, nil); err != nil {
			return err
		}
		// 降权后撤销其全部 refresh token，管理权限下一次请求即失效。
		if err := h.identity.RevokeSessionsTx(tx, targetID, time.Now()); err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "user.role", "user", targetID.String(), c.GetString("request_id"), "",
			userRoleAudit(target.RoleKey, beforeName), userRoleAudit(nil, ""))
	})
	if errors.Is(err, errLastSystemMember) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"id": "至少保留一个管理员"}))
		return
	}
	if err != nil {
		slog.Error("撤销管理员失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
}

// ===== 审计日志 =====

// ListAuditLogs 分页返回管理操作审计，支持按操作名与目标筛选。
func (h *AdminHandler) ListAuditLogs(c *gin.Context) {
	params, ok := parsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.AdminAuditLog{})
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if targetType := c.Query("targetType"); targetType != "" {
		query = query.Where("target_type = ?", targetType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var logs []model.AdminAuditLog
	if err := query.Order("created_at DESC").Order("id ASC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).Find(&logs).Error; err != nil {
		slog.Error("查询审计日志失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(logs))
	for i := range logs {
		items = append(items, gin.H{
			"id":            logs[i].ID.String(),
			"actorUserId":   logs[i].ActorUserID.String(),
			"action":        logs[i].Action,
			"targetType":    logs[i].TargetType,
			"targetId":      logs[i].TargetID,
			"requestId":     logs[i].RequestID,
			"reason":        logs[i].Reason,
			"beforeSummary": json.RawMessage(logs[i].BeforeSummary),
			"afterSummary":  json.RawMessage(logs[i].AfterSummary),
			"createdAt":     formatTime(logs[i].CreatedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

// randomInviteCode 生成短邀请码，使用 URL 安全字符集。
func randomInviteCode() string {
	buffer := make([]byte, 9)
	if _, err := rand.Read(buffer); err != nil {
		return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
func randomInviteCodeOrFallback() string { return randomInviteCode() }

// RevenueStats 后台总览的收入与转化指标：今日/近 7 天/累计充值、付费用户数与转化率。
func (h *AdminHandler) RevenueStats(c *gin.Context) {
	now := time.Now()
	dayStart := now.UTC().Truncate(24 * time.Hour)
	weekStart := dayStart.AddDate(0, 0, -6)
	type revenueRow struct {
		Amount int64
		Orders int64
	}
	var today, week, total revenueRow
	h.db.Model(&model.Order{}).Where("status = ? AND paid_at >= ?", "paid", dayStart).
		Select("COALESCE(SUM(price_micros), 0) AS amount, COUNT(*) AS orders").Scan(&today)
	h.db.Model(&model.Order{}).Where("status = ? AND paid_at >= ?", "paid", weekStart).
		Select("COALESCE(SUM(price_micros), 0) AS amount, COUNT(*) AS orders").Scan(&week)
	h.db.Model(&model.Order{}).Where("status = ?", "paid").
		Select("COALESCE(SUM(price_micros), 0) AS amount, COUNT(*) AS orders").Scan(&total)

	var userTotal, paidUsers int64
	h.db.Model(&model.PlatformUser{}).Count(&userTotal)
	// 付费用户 = 存在 period_end 未到的订阅，与 membership.ActivePlan 派生口径一致（D6）。
	h.db.Model(&model.MembershipSubscription{}).Where("period_end > ?", now).Distinct("user_id").Count(&paidUsers)
	conversion := 0.0
	if userTotal > 0 {
		conversion = float64(paidUsers) / float64(userTotal)
	}
	c.JSON(http.StatusOK, gin.H{
		"revenueMicrosToday": today.Amount,
		"ordersToday":        today.Orders,
		"revenueMicrosWeek":  week.Amount,
		"ordersWeek":         week.Orders,
		"revenueMicrosTotal": total.Amount,
		"ordersTotal":        total.Orders,
		"paidUsers":          paidUsers,
		"userTotal":          userTotal,
		"conversionRate":     conversion,
	})
}
