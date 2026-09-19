package admin

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
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
)

// ListFeedbackTickets 工单台列表：按状态、类别筛选，附提交人邮箱与用户名。
func (h *AdminHandler) ListFeedbackTickets(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.FeedbackTicket{})
	if status := c.Query("status"); status != "" {
		if !model.FeedbackStatusValid(status) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
			return
		}
		query = query.Where("feedback_tickets.status = ?", status)
	}
	if category := c.Query("category"); category != "" {
		if !model.FeedbackCategoryValid(category) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"category": "category 取值非法"}))
			return
		}
		query = query.Where("feedback_tickets.category = ?", category)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		slog.Error("统计反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	type feedbackRow struct {
		ID         uuid.UUID
		UserID     uuid.UUID
		Category   string
		Status     string
		Content    string
		ReplyCount int
		ResolvedAt *time.Time
		CreatedAt  time.Time
		UpdatedAt  time.Time
		Email      string
		Username   string
	}
	var rows []feedbackRow
	if err := query.
		Select("feedback_tickets.id, feedback_tickets.user_id, feedback_tickets.category, feedback_tickets.status, feedback_tickets.content, feedback_tickets.reply_count, feedback_tickets.resolved_at, feedback_tickets.created_at, feedback_tickets.updated_at, platform_users.email AS email, platform_users.username AS username").
		Joins("LEFT JOIN platform_users ON platform_users.id = feedback_tickets.user_id").
		Order("feedback_tickets.created_at DESC").Order("feedback_tickets.id ASC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Scan(&rows).Error; err != nil {
		slog.Error("查询反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for i := range rows {
		payload := feedbackTicketPayload(model.FeedbackTicket{
			ID: rows[i].ID, UserID: rows[i].UserID, Category: rows[i].Category, Status: rows[i].Status,
			Content: rows[i].Content, ReplyCount: rows[i].ReplyCount, ResolvedAt: rows[i].ResolvedAt,
			CreatedAt: rows[i].CreatedAt, UpdatedAt: rows[i].UpdatedAt,
		})
		payload["user"] = gin.H{"email": rows[i].Email, "username": rows[i].Username}
		items = append(items, payload)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

// GetFeedbackTicket 工单详情：完整对话线程与提交人信息。
func (h *AdminHandler) GetFeedbackTicket(c *gin.Context) {
	ticket, ok := h.loadFeedbackTicket(c)
	if !ok {
		return
	}
	var replies []model.FeedbackTicketReply
	if err := h.db.Where("ticket_id = ?", ticket.ID).Order("created_at ASC").Order("id ASC").Find(&replies).Error; err != nil {
		slog.Error("查询工单回复失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var user model.PlatformUser
	if err := h.db.Select("email", "username", "display_name").First(&user, "id = ?", ticket.UserID).Error; err != nil {
		slog.Error("查询工单提交人失败", "err", err)
	}
	c.JSON(http.StatusOK, gin.H{
		"ticket":  feedbackTicketPayload(*ticket),
		"replies": feedbackRepliesPayload(replies),
		"user":    gin.H{"email": user.Email, "username": user.Username, "displayName": user.DisplayName},
	})
}

type feedbackReplyReq struct {
	Content string `json:"content"`
}

// ReplyFeedbackTicket 客服侧回复。回复不改状态；需要改状态走独立的状态接口。
func (h *AdminHandler) ReplyFeedbackTicket(c *gin.Context) {
	ticket, ok := h.loadFeedbackTicket(c)
	if !ok {
		return
	}
	var req feedbackReplyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	content := strings.TrimSpace(req.Content)
	if l := len([]rune(content)); l < 1 || l > 1000 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"content": "回复内容需在 1-1000 字之间"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	reply := model.FeedbackTicketReply{
		ID:        uuid.New(),
		TicketID:  ticket.ID,
		UserID:    actorID,
		IsStaff:   true,
		Content:   content,
		CreatedAt: time.Now(),
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&reply).Error; err != nil {
			return err
		}
		return tx.Model(&model.FeedbackTicket{}).Where("id = ?", ticket.ID).
			Updates(map[string]any{"reply_count": gorm.Expr("reply_count + 1"), "updated_at": time.Now()}).Error
	})
	if err != nil {
		slog.Error("回复反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"reply": feedbackReplyPayload(reply)})
}

type feedbackStatusReq struct {
	Status string `json:"status"`
}

// UpdateFeedbackTicketStatus 更新工单状态；置为 resolved 时记录解决时间，退回 open 时清空。
func (h *AdminHandler) UpdateFeedbackTicketStatus(c *gin.Context) {
	ticket, ok := h.loadFeedbackTicket(c)
	if !ok {
		return
	}
	var req feedbackStatusReq
	if err := c.ShouldBindJSON(&req); err != nil || !model.FeedbackStatusValid(req.Status) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
		return
	}
	updates := map[string]any{"status": req.Status, "updated_at": time.Now()}
	if req.Status == "resolved" {
		now := time.Now()
		updates["resolved_at"] = &now
	} else {
		updates["resolved_at"] = nil
	}
	if err := h.db.Model(&model.FeedbackTicket{}).Where("id = ?", ticket.ID).Updates(updates).Error; err != nil {
		slog.Error("更新工单状态失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": ticket.ID.String(), "status": req.Status})
}

// ListGenerationFeedbacks 生成结果点赞点踩反馈列表：联用户与生成记录，供后台了解生成质量。
func (h *AdminHandler) ListGenerationFeedbacks(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.GenerationFeedback{})
	if rating := c.Query("rating"); rating == "1" || rating == "-1" {
		query = query.Where("generation_feedbacks.rating = ?", rating)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		slog.Error("统计生成反馈失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	type feedbackRow struct {
		ID           uuid.UUID
		UserID       uuid.UUID
		GenerationID uuid.UUID
		Rating       int
		Labels       string
		Note         string
		UpdatedAt    time.Time
		Email        string
		Username     string
		Kind         string
		GModel       string
		Status       string
	}
	var rows []feedbackRow
	if err := query.
		Select("generation_feedbacks.id, generation_feedbacks.user_id, generation_feedbacks.generation_id, generation_feedbacks.rating, generation_feedbacks.labels, generation_feedbacks.note, generation_feedbacks.updated_at, platform_users.email AS email, platform_users.username AS username, generations.kind AS kind, generations.model AS g_model, generations.status AS status").
		Joins("LEFT JOIN platform_users ON platform_users.id = generation_feedbacks.user_id").
		Joins("LEFT JOIN generations ON generations.id = generation_feedbacks.generation_id").
		Order("generation_feedbacks.updated_at DESC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Scan(&rows).Error; err != nil {
		slog.Error("查询生成反馈失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for i := range rows {
		items = append(items, gin.H{
			"generationId": rows[i].GenerationID.String(),
			"rating":       rows[i].Rating,
			"labels":       strings.Split(strings.Trim(rows[i].Labels, ","), ","),
			"note":         rows[i].Note,
			"updatedAt":    httpx.FormatTime(rows[i].UpdatedAt),
			"user":         gin.H{"email": rows[i].Email, "username": rows[i].Username},
			"generation":   gin.H{"kind": rows[i].Kind, "model": rows[i].GModel, "status": rows[i].Status},
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

func (h *AdminHandler) loadFeedbackTicket(c *gin.Context) (*model.FeedbackTicket, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	var ticket model.FeedbackTicket
	if err := h.db.First(&ticket, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return nil, false
		}
		slog.Error("查询反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	return &ticket, true
}

// 反馈工单载荷助手：与 canvas 包同形状，各自维护（跨包共享需上移模型层，不值得）。
func feedbackTicketPayload(ticket model.FeedbackTicket) gin.H {
	payload := gin.H{
		"id":         ticket.ID.String(),
		"category":   ticket.Category,
		"status":     ticket.Status,
		"content":    ticket.Content,
		"replyCount": ticket.ReplyCount,
		"createdAt":  httpx.FormatTime(ticket.CreatedAt),
		"updatedAt":  httpx.FormatTime(ticket.UpdatedAt),
	}
	if ticket.ResolvedAt != nil {
		payload["resolvedAt"] = httpx.FormatTime(*ticket.ResolvedAt)
	}
	return payload
}

func feedbackReplyPayload(reply model.FeedbackTicketReply) gin.H {
	return gin.H{
		"id":        reply.ID.String(),
		"isStaff":   reply.IsStaff,
		"content":   reply.Content,
		"createdAt": httpx.FormatTime(reply.CreatedAt),
	}
}

func feedbackRepliesPayload(replies []model.FeedbackTicketReply) []gin.H {
	items := make([]gin.H, 0, len(replies))
	for i := range replies {
		items = append(items, feedbackReplyPayload(replies[i]))
	}
	return items
}
