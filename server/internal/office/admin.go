package office

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
)

// AdminHandler 是 AI 办公助理的管理面只读接口（/api/admin/office/*，office.read）。
// handler 放在产品域包内、路由表由 admin 包统一注册（admin_routes.go，依赖方向 admin→产品域），
// 照 blog 域 AdminHandler 模板。本期只读，无写执行点（office.write 仅注册预留）。
type AdminHandler struct {
	db *gorm.DB
}

func NewAdminHandler(db *gorm.DB) *AdminHandler { return &AdminHandler{db: db} }

// adminSessionColumns 是管理面会话行查询列：userEmail 来自 LEFT JOIN 平台用户表，
// messagesCount / lastRunStatus 是相关子查询列（双方言均可执行）。
const adminSessionColumns = "office_sessions.id, office_sessions.user_id, office_sessions.title, office_sessions.status, " +
	"office_sessions.agent_id, office_sessions.workspace_path, office_sessions.active_run_id, " +
	"office_sessions.created_at, office_sessions.updated_at, " +
	"platform_users.email AS user_email, " +
	"(SELECT COUNT(*) FROM office_messages WHERE office_messages.session_id = office_sessions.id) AS messages_count, " +
	"(SELECT status FROM office_runs WHERE office_runs.session_id = office_sessions.id ORDER BY office_runs.created_at DESC LIMIT 1) AS last_run_status"

// adminSessionRow 对应 adminSessionColumns 的扫描目标。
type adminSessionRow struct {
	ID            string
	UserID        string
	Title         string
	Status        string
	AgentID       string
	WorkspacePath string
	ActiveRunID   *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	UserEmail     string
	MessagesCount int64
	LastRunStatus *string
}

func (r adminSessionRow) payload() gin.H {
	activeRunID := ""
	if r.ActiveRunID != nil {
		activeRunID = *r.ActiveRunID
	}
	return gin.H{
		"id":            r.ID,
		"title":         r.Title,
		"status":        r.Status,
		"userId":        r.UserID,
		"userEmail":     r.UserEmail,
		"agentId":       r.AgentID,
		"workspacePath": r.WorkspacePath,
		"activeRunId":   activeRunID,
		"messagesCount": r.MessagesCount,
		"createdAt":     httpx.FormatTime(r.CreatedAt),
		"updatedAt":     httpx.FormatTime(r.UpdatedAt),
		"lastRunStatus": r.LastRunStatus, // 无 run 时为 null
	}
}

// ListSessions 管理面会话列表：userId/status 筛选，updated_at 倒序游标分页
// （游标格式与契约一 A2 相同）。userId 非法 400，cursor 非法 400，limit 1-100。
func (h *AdminHandler) ListSessions(c *gin.Context) {
	limit := httpx.DefaultPageSize
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > httpx.MaxPageSize {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"limit": "limit 必须是 1-100 的整数"}))
			return
		}
		limit = n
	}
	query := h.db.Model(&OfficeSession{})
	if raw := c.Query("userId"); raw != "" {
		// 双语义：邮箱按 platform_users.email 解析成 id，否则按 uuid 校验（前端搜索框两者都允许输入）。
		resolved := raw
		if strings.Contains(raw, "@") {
			var id string
			if err := h.db.Raw("SELECT id FROM platform_users WHERE email = ? LIMIT 1", raw).Scan(&id).Error; err != nil {
				slog.Error("admin 按 email 解析用户失败", "err", err)
				errs.Abort(c, errs.ErrInternal)
				return
			}
			if id == "" {
				c.JSON(http.StatusOK, gin.H{"items": []any{}, "nextCursor": ""})
				return
			}
			resolved = id
		} else if _, err := uuid.Parse(raw); err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"userId": "userId 不合法"}))
			return
		}
		query = query.Where("office_sessions.user_id = ?", resolved)
	}
	if status := c.Query("status"); status != "" {
		query = query.Where("office_sessions.status = ?", status)
	}
	if raw := c.Query("cursor"); raw != "" {
		cursorAt, cursorID, ok := decodeOfficeCursor(raw)
		if !ok {
			errs.Abort(c, errs.ErrValidation)
			return
		}
		query = query.Where("(office_sessions.updated_at < ?) OR (office_sessions.updated_at = ? AND office_sessions.id < ?)", cursorAt, cursorAt, cursorID)
	}
	var rows []adminSessionRow
	if err := query.
		Select(adminSessionColumns).
		Joins("LEFT JOIN platform_users ON platform_users.id = office_sessions.user_id").
		Order("office_sessions.updated_at DESC").Order("office_sessions.id DESC").
		Limit(limit + 1).
		Scan(&rows).Error; err != nil {
		slog.Error("admin 查询办公会话列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	nextCursor := ""
	if len(rows) > limit {
		last := rows[limit-1]
		nextCursor = encodeOfficeCursor(last.UpdatedAt, last.ID)
		rows = rows[:limit]
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.payload())
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor})
}

// GetSession 管理面会话详情：会话全字段 + 全部消息（created_at 升序）。
// 管理面按 id 全局可见（office.read 授权），不做契约一的用户归属过滤。
func (h *AdminHandler) GetSession(c *gin.Context) {
	var row adminSessionRow
	err := h.db.Model(&OfficeSession{}).
		Select(adminSessionColumns).
		Joins("LEFT JOIN platform_users ON platform_users.id = office_sessions.user_id").
		Take(&row, "office_sessions.id = ?", c.Param("id")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		slog.Error("admin 查询办公会话详情失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var messages []OfficeMessage
	if err := h.db.Where("session_id = ?", row.ID).Order("created_at ASC, id ASC").Find(&messages).Error; err != nil {
		slog.Error("admin 查询办公会话消息失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]messageResp, 0, len(messages))
	for _, m := range messages {
		items = append(items, messageResp{
			ID: m.ID, SessionID: m.SessionID, RunID: m.RunID, Role: m.Role,
			Content: json.RawMessage(m.Content), ThreadID: m.ThreadID, TurnID: m.TurnID,
			CreatedAt: httpx.FormatTime(m.CreatedAt),
		})
	}
	payload := row.payload()
	payload["messages"] = items
	c.JSON(http.StatusOK, payload)
}

// toolCallNameExpr 返回从 office_events.payload 提取 tool_call 工具名的方言表达式。
// 落库形状：office_events.payload 存的是事件信封的 payload 字段本身（service.go ingestEvent），
// tool_call 的 payload 是 {toolCallId,name,inputPreview?,status} 平铺结构，路径即 $.name。
// MySQL 用 JSON_UNQUOTE + JSON_EXTRACT，测试库 SQLite 用 json_extract（照 canvas/asset.go 先例）。
func toolCallNameExpr(dialector string) string {
	if dialector == "mysql" {
		return "JSON_UNQUOTE(JSON_EXTRACT(office_events.payload, '$.name'))"
	}
	return "json_extract(office_events.payload, '$.name')"
}

// Stats 管理面统计看板：days 只接受 7/30（默认 7，非法 400），窗口按 created_at >= now-days。
// 成功率由前端按 runsByStatus 计算（succeeded/(succeeded+failed+cancelled)），服务端不重复出数。
func (h *AdminHandler) Stats(c *gin.Context) {
	days := 7
	if raw := c.Query("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || (parsed != 7 && parsed != 30) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"days": "days 只支持 7、30"}))
			return
		}
		days = parsed
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	var runsTotal int64
	if err := h.db.Model(&OfficeRun{}).Where("created_at >= ?", cutoff).Count(&runsTotal).Error; err != nil {
		slog.Error("统计办公 run 总数失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	var statusRows []struct {
		Status string
		Cnt    int64
	}
	if err := h.db.Model(&OfficeRun{}).Select("status, COUNT(*) AS cnt").
		Where("created_at >= ?", cutoff).Group("status").Scan(&statusRows).Error; err != nil {
		slog.Error("统计办公 run 状态分布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	runsByStatus := gin.H{
		RunQueued: int64(0), RunRunning: int64(0), RunSucceeded: int64(0),
		RunFailed: int64(0), RunCancelled: int64(0),
	}
	for _, row := range statusRows {
		runsByStatus[row.Status] = row.Cnt
	}

	var modelRows []struct {
		Model string
		Cnt   int64
	}
	if err := h.db.Model(&OfficeRun{}).Select("model, COUNT(*) AS cnt").
		Where("created_at >= ?", cutoff).Group("model").
		Order("cnt DESC, model ASC").Limit(10).Scan(&modelRows).Error; err != nil {
		slog.Error("统计办公模型分布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	runsByModel := make([]gin.H, 0, len(modelRows))
	for _, row := range modelRows {
		runsByModel = append(runsByModel, gin.H{"model": row.Model, "count": row.Cnt})
	}

	var messagesTotal, sessionsTotal int64
	if err := h.db.Model(&OfficeMessage{}).Where("created_at >= ?", cutoff).Count(&messagesTotal).Error; err != nil {
		slog.Error("统计办公消息总数失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if err := h.db.Model(&OfficeSession{}).Where("created_at >= ?", cutoff).Count(&sessionsTotal).Error; err != nil {
		slog.Error("统计办公会话总数失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	// office_runs 无 user_id 列，活跃用户经 session 归属去重。
	var activeUsers int64
	if err := h.db.Model(&OfficeRun{}).
		Select("COUNT(DISTINCT office_sessions.user_id)").
		Joins("JOIN office_sessions ON office_sessions.id = office_runs.session_id").
		Where("office_runs.created_at >= ?", cutoff).
		Scan(&activeUsers).Error; err != nil {
		slog.Error("统计办公活跃用户失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	// topUsers 按 run 数取前 10；messages 单独按窗口聚合后回填，避免 join 笛卡尔放大。
	type userCountRow struct {
		UserID string
		Cnt    int64
	}
	var runRows []userCountRow
	if err := h.db.Model(&OfficeRun{}).
		Select("office_sessions.user_id AS user_id, COUNT(*) AS cnt").
		Joins("JOIN office_sessions ON office_sessions.id = office_runs.session_id").
		Where("office_runs.created_at >= ?", cutoff).
		Group("office_sessions.user_id").
		Order("cnt DESC, user_id ASC").Limit(10).
		Scan(&runRows).Error; err != nil {
		slog.Error("统计办公用户排行失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var msgRows []userCountRow
	if err := h.db.Model(&OfficeMessage{}).
		Select("office_sessions.user_id AS user_id, COUNT(*) AS cnt").
		Joins("JOIN office_sessions ON office_sessions.id = office_messages.session_id").
		Where("office_messages.created_at >= ?", cutoff).
		Group("office_sessions.user_id").
		Scan(&msgRows).Error; err != nil {
		slog.Error("统计办公用户消息数失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	runByUser := make(map[string]int64, len(runRows))
	userIDs := make([]uuid.UUID, 0, len(runRows))
	for _, row := range runRows {
		runByUser[row.UserID] = row.Cnt
		if id, err := uuid.Parse(row.UserID); err == nil {
			userIDs = append(userIDs, id)
		}
	}
	msgByUser := make(map[string]int64, len(msgRows))
	for _, row := range msgRows {
		msgByUser[row.UserID] = row.Cnt
	}
	emails := make(map[uuid.UUID]string, len(userIDs))
	if len(userIDs) > 0 {
		var found []struct {
			ID    uuid.UUID
			Email string
		}
		if err := h.db.Table("platform_users").Select("id, email").Where("id IN ?", userIDs).Scan(&found).Error; err != nil {
			slog.Error("读取办公用户排行邮箱失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		for _, item := range found {
			emails[item.ID] = item.Email
		}
	}
	topUsers := make([]gin.H, 0, len(runRows))
	for _, row := range runRows {
		id, _ := uuid.Parse(row.UserID)
		topUsers = append(topUsers, gin.H{
			"userId":   row.UserID,
			"email":    emails[id],
			"runs":     row.Cnt,
			"messages": msgByUser[row.UserID],
		})
	}

	// toolCalls 从事件负载聚合工具名；payload 缺 name 的坏帧不计入。
	nameExpr := toolCallNameExpr(h.db.Dialector.Name())
	var toolRows []struct {
		Name string
		Cnt  int64
	}
	if err := h.db.Table("office_events").
		Select(nameExpr+" AS name, COUNT(*) AS cnt").
		Where("type = ? AND created_at >= ?", EventToolCall, cutoff).
		Group(nameExpr).
		Order("cnt DESC, name ASC").Limit(10).
		Scan(&toolRows).Error; err != nil {
		slog.Error("统计办公工具调用失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	toolCalls := make([]gin.H, 0, len(toolRows))
	for _, row := range toolRows {
		if row.Name == "" {
			continue
		}
		toolCalls = append(toolCalls, gin.H{"name": row.Name, "count": row.Cnt})
	}

	c.JSON(http.StatusOK, gin.H{
		"runsTotal":     runsTotal,
		"runsByStatus":  runsByStatus,
		"runsByModel":   runsByModel,
		"messagesTotal": messagesTotal,
		"sessionsTotal": sessionsTotal,
		"activeUsers":   activeUsers,
		"topUsers":      topUsers,
		"toolCalls":     toolCalls,
	})
}
