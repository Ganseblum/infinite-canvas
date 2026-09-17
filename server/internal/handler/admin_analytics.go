package handler

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
)

// costExpr 是消费口径：只累计成功请求的实扣点数，失败请求按 0 计。
// 依据：失败收敛走 MarkFailed→Refund（internal/service/request.go:98、internal/service/credit.go:122），
// 按原消费流水逐条原桶全额退回，退款流水有 refund_of_transaction_id 唯一索引兜底幂等。
// 已知口径偏差有两处：一是产物审核拒绝（markRequestRejected）把请求置为 failed 但不退点，
// 实际已发生消耗，此处不计入，会低估消费；二是退款失败暂存 refund_pending 的失败请求
// 同样先按 0 计，等人工重试退款成功后用户余额才真正回补。
const costExpr = "COALESCE(SUM(CASE WHEN status = 'succeeded' THEN final_cost_micros ELSE 0 END), 0)"

// usageDayRow 是按 stat_date 聚合的单天用量行。
type usageDayRow struct {
	StatDate    string
	Requests    int64
	Succeeded   int64
	Failed      int64
	CostMicros  int64
	ActiveUsers int64
	Sessions    int64
}

// UsageAnalytics 输出用量分析看板的一次性取数：总览、按天趋势与模型/能力/规格/用户消费分布。
// 时间口径统一用 stat_date 字符串列（UTC+8 日界）做 >= 比较，不使用 DATE()、CONVERT_TZ
// 等方言函数，MySQL 与 SQLite 行为一致；迁移前 stat_date 为空的历史行不参与统计。
// days 只接受 7/30/90，默认 30。successRate 口径：succeeded/(succeeded+failed)，
// running 请求未到终态，不压低成功率。
func (h *AdminHandler) UsageAnalytics(c *gin.Context) {
	days := 30
	if raw := c.Query("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed != 7 && parsed != 30 && parsed != 90 {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"days": "days 只支持 7、30、90"}))
			return
		}
		days = parsed
	}
	today := time.Now().In(statZone)
	start := today.AddDate(0, 0, -(days - 1)).Format(statDateFormat)

	var summary struct {
		Requests       int64
		Succeeded      int64
		Failed         int64
		CostMicros     int64
		ActiveUsers    int64
		ActiveSessions int64
	}
	if err := h.db.Model(&model.AIRequest{}).Select(
		"COUNT(*) AS requests, "+
			"COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0) AS succeeded, "+
			"COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS failed, "+
			costExpr+" AS cost_micros, "+
			"COUNT(DISTINCT user_id) AS active_users, "+
			"COUNT(DISTINCT session_id) AS active_sessions",
	).Where("stat_date >= ?", start).Scan(&summary).Error; err != nil {
		slog.Error("统计用量总览失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	successRate := 0.0
	if terminal := summary.Succeeded + summary.Failed; terminal > 0 {
		successRate = math.Round(float64(summary.Succeeded)/float64(terminal)*10000) / 10000
	}

	var dayRows []usageDayRow
	if err := h.db.Model(&model.AIRequest{}).Select(
		"stat_date, COUNT(*) AS requests, "+
			"COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0) AS succeeded, "+
			"COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS failed, "+
			costExpr+" AS cost_micros, "+
			"COUNT(DISTINCT user_id) AS active_users, "+
			"COUNT(DISTINCT session_id) AS sessions",
	).Where("stat_date >= ?", start).Group("stat_date").Scan(&dayRows).Error; err != nil {
		slog.Error("统计按天用量失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	byDate := make(map[string]usageDayRow, len(dayRows))
	for _, row := range dayRows {
		byDate[row.StatDate] = row
	}
	daily := make([]gin.H, 0, days)
	for i := days - 1; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format(statDateFormat)
		row := byDate[date]
		daily = append(daily, gin.H{
			"date":        date,
			"requests":    row.Requests,
			"succeeded":   row.Succeeded,
			"failed":      row.Failed,
			"costMicros":  row.CostMicros,
			"activeUsers": row.ActiveUsers,
			"sessions":    row.Sessions,
		})
	}

	var modelRows []struct {
		Model      string
		Requests   int64
		CostMicros int64
	}
	if err := h.db.Model(&model.AIRequest{}).Select("model, COUNT(*) AS requests, "+costExpr+" AS cost_micros").
		Where("stat_date >= ?", start).Group("model").
		Order("requests DESC, model ASC").Limit(20).Scan(&modelRows).Error; err != nil {
		slog.Error("统计模型分布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	byModel := make([]gin.H, 0, len(modelRows))
	for _, row := range modelRows {
		byModel = append(byModel, gin.H{"key": row.Model, "requests": row.Requests, "costMicros": row.CostMicros})
	}

	var capabilityRows []struct {
		Capability string
		Requests   int64
		CostMicros int64
	}
	if err := h.db.Model(&model.AIRequest{}).Select("capability, COUNT(*) AS requests, "+costExpr+" AS cost_micros").
		Where("stat_date >= ?", start).Group("capability").
		Order("requests DESC, capability ASC").Scan(&capabilityRows).Error; err != nil {
		slog.Error("统计能力分布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	byCapability := make([]gin.H, 0, len(capabilityRows))
	for _, row := range capabilityRows {
		byCapability = append(byCapability, gin.H{"key": row.Capability, "requests": row.Requests, "costMicros": row.CostMicros})
	}

	var specRows []struct {
		Capability string
		ParamSpec  string
		Requests   int64
		CostMicros int64
	}
	if err := h.db.Model(&model.AIRequest{}).Select("capability, param_spec, COUNT(*) AS requests, "+costExpr+" AS cost_micros").
		Where("stat_date >= ?", start).Where("param_spec <> ''").Group("capability, param_spec").
		Order("requests DESC, capability ASC, param_spec ASC").Scan(&specRows).Error; err != nil {
		slog.Error("统计规格分布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	bySpec := make([]gin.H, 0, len(specRows))
	for _, row := range specRows {
		bySpec = append(bySpec, gin.H{"capability": row.Capability, "key": row.ParamSpec, "requests": row.Requests, "costMicros": row.CostMicros})
	}

	var userRows []struct {
		UserID     uuid.UUID
		Requests   int64
		CostMicros int64
	}
	if err := h.db.Model(&model.AIRequest{}).Select("user_id, COUNT(*) AS requests, "+costExpr+" AS cost_micros").
		Where("stat_date >= ?", start).Group("user_id").
		Order("requests DESC, user_id ASC").Limit(10).Scan(&userRows).Error; err != nil {
		slog.Error("统计用户消费排行失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	users := make(map[uuid.UUID]model.User, len(userRows))
	if len(userRows) > 0 {
		ids := make([]uuid.UUID, 0, len(userRows))
		for _, row := range userRows {
			ids = append(ids, row.UserID)
		}
		var found []model.User
		if err := h.db.Where("id IN ?", ids).Find(&found).Error; err != nil {
			slog.Error("读取用户消费排行信息失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		for _, item := range found {
			users[item.ID] = item
		}
	}
	topUsers := make([]gin.H, 0, len(userRows))
	for _, row := range userRows {
		info := users[row.UserID]
		topUsers = append(topUsers, gin.H{
			"userId":      row.UserID.String(),
			"email":       info.Email,
			"displayName": info.DisplayName,
			"requests":    row.Requests,
			"costMicros":  row.CostMicros,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"summary": gin.H{
			"requests":       summary.Requests,
			"succeeded":      summary.Succeeded,
			"failed":         summary.Failed,
			"successRate":    successRate,
			"costMicros":     summary.CostMicros,
			"activeUsers":    summary.ActiveUsers,
			"activeSessions": summary.ActiveSessions,
		},
		"daily":        daily,
		"byModel":      byModel,
		"byCapability": byCapability,
		"bySpec":       bySpec,
		"topUsers":     topUsers,
	})
}
