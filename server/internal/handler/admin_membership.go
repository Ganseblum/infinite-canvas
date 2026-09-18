package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
)

// ===== 会员订阅管理 =====

// errSubscriptionAlreadyEnded 标记重复作废，映射为 409。
var errSubscriptionAlreadyEnded = errors.New("订阅已作废")

// membershipSubscriptionPayload 把订阅行渲染成管理端契约形状（列表单项与发放响应共用）。
// userEmail/userUsername 来自 JOIN platform_users，planName 来自 JOIN membership_plans；
// 档位行缺失（被删）时 planName 输出空串，不阻断列表展示。
func membershipSubscriptionPayload(sub model.MembershipSubscription, userEmail, userUsername, planName string) gin.H {
	return gin.H{
		"id":           sub.ID.String(),
		"userId":       sub.UserID.String(),
		"userEmail":    userEmail,
		"userUsername": userUsername,
		"planId":       sub.PlanID,
		"planName":     planName,
		"status":       sub.Status,
		"startedAt":    formatTime(sub.StartedAt),
		"periodEnd":    formatTime(sub.PeriodEnd),
		"sourceRef":    sub.SourceRef,
		"createdAt":    formatTime(sub.CreatedAt),
	}
}

// ListSubscriptions 分页列出会员订阅：userId/planId 精确筛选，status 只允许 active|ended。
// 用户邮箱/用户名与档位名通过 LEFT JOIN 一次带出，不逐行回查。
func (h *AdminHandler) ListSubscriptions(c *gin.Context) {
	params, ok := parsePageParams(c)
	if !ok {
		return
	}
	status := c.Query("status")
	if status != "" && status != "active" && status != "ended" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 只能是 active 或 ended"}))
		return
	}
	query := h.db.Model(&model.MembershipSubscription{}).
		Joins("LEFT JOIN platform_users pu ON pu.id = membership_subscriptions.user_id").
		Joins("LEFT JOIN membership_plans mp ON mp.id = membership_subscriptions.plan_id")
	if raw := c.Query("userId"); raw != "" {
		userID, err := uuid.Parse(raw)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"userId": "userId 不合法"}))
			return
		}
		query = query.Where("membership_subscriptions.user_id = ?", userID)
	}
	if planID := c.Query("planId"); planID != "" {
		query = query.Where("membership_subscriptions.plan_id = ?", planID)
	}
	if status != "" {
		query = query.Where("membership_subscriptions.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		slog.Error("统计会员订阅失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var rows []struct {
		ID           uuid.UUID
		UserID       uuid.UUID
		PlanID       string
		Status       string
		StartedAt    time.Time
		PeriodEnd    time.Time
		SourceRef    string
		CreatedAt    time.Time
		UserEmail    string
		UserUsername string
		PlanName     *string
	}
	err := query.Select(`membership_subscriptions.id, membership_subscriptions.user_id, membership_subscriptions.plan_id,
		membership_subscriptions.status, membership_subscriptions.started_at, membership_subscriptions.period_end,
		membership_subscriptions.source_ref, membership_subscriptions.created_at,
		COALESCE(pu.email, '') AS user_email, COALESCE(pu.username, '') AS user_username, mp.name AS plan_name`).
		Order("membership_subscriptions.created_at DESC").Order("membership_subscriptions.id DESC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Scan(&rows).Error
	if err != nil {
		slog.Error("查询会员订阅列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		planName := ""
		if row.PlanName != nil {
			planName = *row.PlanName
		}
		items = append(items, membershipSubscriptionPayload(model.MembershipSubscription{
			ID: row.ID, UserID: row.UserID, PlanID: row.PlanID, Status: row.Status,
			StartedAt: row.StartedAt, PeriodEnd: row.PeriodEnd, SourceRef: row.SourceRef, CreatedAt: row.CreatedAt,
		}, row.UserEmail, row.UserUsername, planName))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

type grantMembershipReq struct {
	UserID string `json:"userId"`
	PlanID string `json:"planId"`
	Reason string `json:"reason"`
}

// GrantSubscription 管理员直接发放会员（不经过订单）。
func (h *AdminHandler) GrantSubscription(c *gin.Context) {
	h.grantMembership(c, "membership.grant")
}

// CompensateSubscription 管理员补偿发放会员：与发放同一实现，仅审计 action 不同。
func (h *AdminHandler) CompensateSubscription(c *gin.Context) {
	h.grantMembership(c, "membership.compensate")
}

// grantMembership 发放/补偿共用主体：校验用户与档位后，在事务内复用 membership.Compensate
// 的管理员发放语义（从 max(当前 period_end, now) 起叠一个档位周期并回写存储配额），
// reason 同时记入订阅 source_ref 与审计。
func (h *AdminHandler) grantMembership(c *gin.Context, action string) {
	var req grantMembershipReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	fields := map[string]string{}
	var user *model.PlatformUser
	userID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		fields["userId"] = "userId 不合法"
	} else {
		user, err = h.identity.GetByID(c.Request.Context(), userID)
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			fields["userId"] = "用户不存在"
		case err != nil:
			slog.Error("读取用户失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		fields["reason"] = "必须填写原因"
	}
	plan, err := h.membership.PlanDef(c.Request.Context(), strings.TrimSpace(req.PlanID))
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		fields["planId"] = "档位不存在或不可购买"
	case err != nil:
		slog.Error("读取会员档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	case plan.DurationDays <= 0:
		// free/sunset 等非售卖档位 DurationDays=0，同样不可发放。
		fields["planId"] = "档位不存在或不可购买"
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	var sub model.MembershipSubscription
	var after gin.H
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := h.membership.Compensate(tx, userID, plan.ID, reason); err != nil {
			return err
		}
		// Compensate 新行的 period_end 严格大于该用户全部历史行（从最晚 period_end 叠加周期），
		// 所以按 ActivePlan 同款排序取到的第一行就是刚创建的订阅。
		if err := tx.Where("user_id = ?", userID).
			Order("period_end DESC, created_at DESC").
			First(&sub).Error; err != nil {
			return err
		}
		after = membershipSubscriptionPayload(sub, user.Email, user.Username, plan.Name)
		return h.audit.Record(tx, actorID, action, "membership_subscription", sub.ID.String(),
			c.GetString("request_id"), reason, nil, after)
	})
	if err != nil {
		slog.Error("管理员发放会员失败", "action", action, "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscription": after})
}

type revokeMembershipReq struct {
	Reason string `json:"reason"`
}

// RevokeSubscription 作废订阅：条件更新 status='ended'，仅 active 可作废，ended 重复作废返回 409。
// period_end 同时压到执行时刻：platform/membership 的 ActivePlan 按 period_end DESC 取最新行判定
// 付费身份且不看 status，作废行若 period_end 仍在未来会被继续派生为 paid；压到当前时刻后该行
// 立即不再派生 paid（作废立即生效，不再保留日落宽限）。
func (h *AdminHandler) RevokeSubscription(c *gin.Context) {
	subID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req revokeMembershipReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"reason": "必须填写原因"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	now := time.Now()
	var before model.MembershipSubscription
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// 先读行区分 404（不存在）与 409（已作废），再条件更新防并发双作废。
		if err := tx.First(&before, "id = ?", subID).Error; err != nil {
			return err
		}
		if before.Status != "active" {
			return errSubscriptionAlreadyEnded
		}
		res := tx.Model(&model.MembershipSubscription{}).
			Where("id = ? AND status = ?", subID, "active").
			Updates(map[string]any{"status": "ended", "period_end": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errSubscriptionAlreadyEnded
		}
		// 作废立即生效的另一半：共享池配额回写到派生档位（free），否则上传侧仍按
		// paid 旧配额放行、而 GetMe/生成预检按 ActivePlan 派生拒绝，口径分裂（评审门③ P1-2）。
		if err := h.membership.SyncQuotaWithin(tx, before.UserID, now); err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "membership.revoke", "membership_subscription", subID.String(),
			c.GetString("request_id"), reason,
			gin.H{"status": before.Status, "periodEnd": formatTime(before.PeriodEnd)},
			gin.H{"status": "ended", "periodEnd": formatTime(now)})
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if errors.Is(err, errSubscriptionAlreadyEnded) {
		errs.Abort(c, errs.AddConflict("该订阅已作废，不能重复作废"))
		return
	}
	if err != nil {
		slog.Error("作废会员订阅失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": subID.String(), "status": "ended"})
}
