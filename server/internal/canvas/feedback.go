package canvas

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
)

// FeedbackHandler 提供用户面反馈工单接口：提交、我的工单、详情对话与关闭。
// 后台处理接口在 admin 域（/api/admin/feedback/**），由 feedback.read/write 权限点保护。
type FeedbackHandler struct {
	db *gorm.DB
}

func NewFeedbackHandler(db *gorm.DB) *FeedbackHandler { return &FeedbackHandler{db: db} }

// MountFeedbackRoutes 注册用户面反馈工单五条路由，鉴权中间件由调用方挂在分组上。
func MountFeedbackRoutes(g *gin.RouterGroup, h *FeedbackHandler) {
	g.POST("", h.Create)
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.POST("/:id/replies", h.Reply)
	g.POST("/:id/close", h.Close)
}

type feedbackCreateReq struct {
	Category string `json:"category"`
	Content  string `json:"content"`
}

// Create 提交反馈工单。类别走白名单，正文去空白后 2-1000 字。
func (h *FeedbackHandler) Create(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var req feedbackCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	content := strings.TrimSpace(req.Content)
	fields := map[string]string{}
	if !model.FeedbackCategoryValid(req.Category) {
		fields["category"] = "category 取值非法"
	}
	if l := len([]rune(content)); l < 2 || l > 1000 {
		fields["content"] = "反馈内容需在 2-1000 字之间"
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	ticket := model.FeedbackTicket{
		ID:        uuid.New(),
		UserID:    uid,
		Category:  req.Category,
		Status:    "open",
		Content:   content,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := h.db.Create(&ticket).Error; err != nil {
		slog.Error("创建反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ticket": feedbackTicketPayload(ticket)})
}

// List 我的工单，按创建时间倒序分页。
func (h *FeedbackHandler) List(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.FeedbackTicket{}).Where("user_id = ?", uid)
	if status := c.Query("status"); status != "" {
		if !model.FeedbackStatusValid(status) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
			return
		}
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		slog.Error("统计反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var tickets []model.FeedbackTicket
	if err := query.Order("created_at DESC").Order("id ASC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Find(&tickets).Error; err != nil {
		slog.Error("查询反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(tickets))
	for i := range tickets {
		items = append(items, feedbackTicketPayload(tickets[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

// Get 工单详情与全部回复，仅提交人本人可见。
func (h *FeedbackHandler) Get(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	ticket, replies, ok := h.loadOwnedTicket(c, uid)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": feedbackTicketPayload(*ticket), "replies": feedbackRepliesPayload(replies)})
}

type feedbackReplyReq struct {
	Content string `json:"content"`
}

// Reply 用户追加回复；对已解决的工单回复会重新打开它。
func (h *FeedbackHandler) Reply(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	ticket, ok := h.loadOwned(c, uid)
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
	reply := model.FeedbackTicketReply{
		ID:        uuid.New(),
		TicketID:  ticket.ID,
		UserID:    uid,
		IsStaff:   false,
		Content:   content,
		CreatedAt: time.Now(),
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&reply).Error; err != nil {
			return err
		}
		updates := map[string]any{"reply_count": gorm.Expr("reply_count + 1"), "updated_at": time.Now()}
		if ticket.Status != "open" {
			// 用户再回复 = 工单重新打开；已关闭的工单同样回到待处理。
			updates["status"] = "open"
			updates["resolved_at"] = nil
		}
		return tx.Model(&model.FeedbackTicket{}).Where("id = ?", ticket.ID).Updates(updates).Error
	})
	if err != nil {
		slog.Error("回复反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"reply": feedbackReplyPayload(reply)})
}

// Close 用户主动关闭自己的工单。
func (h *FeedbackHandler) Close(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	ticket, ok := h.loadOwned(c, uid)
	if !ok {
		return
	}
	if ticket.Status == "closed" {
		errs.Abort(c, errs.AddConflict("工单已关闭"))
		return
	}
	if err := h.db.Model(&model.FeedbackTicket{}).Where("id = ?", ticket.ID).
		Updates(map[string]any{"status": "closed", "updated_at": time.Now()}).Error; err != nil {
		slog.Error("关闭反馈工单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": ticket.ID.String(), "status": "closed"})
}

// loadOwned 取当前用户名下的工单；跨用户与不存在一律 404。
func (h *FeedbackHandler) loadOwned(c *gin.Context, uid uuid.UUID) (*model.FeedbackTicket, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	var ticket model.FeedbackTicket
	if err := h.db.Where("id = ? AND user_id = ?", id, uid).First(&ticket).Error; err != nil {
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

// loadOwnedTicket 取工单并附带全部回复（按创建时间正序，对话顺序展示）。
func (h *FeedbackHandler) loadOwnedTicket(c *gin.Context, uid uuid.UUID) (*model.FeedbackTicket, []model.FeedbackTicketReply, bool) {
	ticket, ok := h.loadOwned(c, uid)
	if !ok {
		return nil, nil, false
	}
	var replies []model.FeedbackTicketReply
	if err := h.db.Where("ticket_id = ?", ticket.ID).Order("created_at ASC").Order("id ASC").Find(&replies).Error; err != nil {
		slog.Error("查询工单回复失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return nil, nil, false
	}
	return ticket, replies, true
}

// ===== 生成结果点赞点踩 =====

type generationFeedbackReq struct {
	Rating int    `json:"rating"`
	Labels string `json:"labels"`
	Note   string `json:"note"`
}

// SetGenerationFeedback 对自己的成功生成记录提交点赞/点踩；重复提交按 upsert 覆盖。
// 点踩可携带预设标签（逗号分隔）与文本说明，供后台了解生成质量。
func (h *GenerationHandler) SetGenerationFeedback(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	generationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var req generationFeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if req.Rating != 1 && req.Rating != -1 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"rating": "rating 只能是 1 或 -1"}))
		return
	}
	labels := sanitizeFeedbackLabels(req.Labels)
	note := strings.TrimSpace(req.Note)
	if len([]rune(note)) > 500 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"note": "说明最多 500 字"}))
		return
	}
	// 只有本人的成功生成可以反馈；别人的或未成功的记录一律 404，不暴露存在性。
	var generation model.Generation
	if err := h.db.Where("id = ? AND user_id = ? AND status = ?", generationID, uid, "success").First(&generation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		slog.Error("查询生成记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	now := time.Now()
	feedback := model.GenerationFeedback{
		ID:           uuid.New(),
		UserID:       uid,
		GenerationID: generationID,
		Rating:       req.Rating,
		Labels:       labels,
		Note:         note,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	// upsert：按 (user_id, generation_id) 唯一键覆盖评分、标签与说明。
	if err := h.db.Clauses(gormClauseOnConflictGenerationFeedback()).Create(&feedback).Error; err != nil {
		slog.Error("写入生成反馈失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, generationFeedbackPayload(feedback))
}

// DeleteGenerationFeedback 撤销点赞/点踩。
func (h *GenerationHandler) DeleteGenerationFeedback(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	generationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	result := h.db.Where("user_id = ? AND generation_id = ?", uid, generationID).Delete(&model.GenerationFeedback{})
	if result.Error != nil {
		slog.Error("删除生成反馈失败", "err", result.Error)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	httpx.NoContent(c)
}

// GetGenerationFeedback 读回本人对该生成记录的反馈，未反馈返回 null。
func (h *GenerationHandler) GetGenerationFeedback(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	generationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var feedback model.GenerationFeedback
	if err := h.db.Where("user_id = ? AND generation_id = ?", uid, generationID).First(&feedback).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, gin.H{"feedback": nil})
			return
		}
		slog.Error("查询生成反馈失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"feedback": generationFeedbackPayload(feedback)})
}

// gormClauseOnConflictGenerationFeedback 返回生成反馈 upsert 的冲突子句：
// 撞 (user_id, generation_id) 唯一键时覆盖评分、标签、说明与更新时间。
func gormClauseOnConflictGenerationFeedback() clause.Expression {
	return clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "generation_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"rating", "labels", "note", "updated_at"}),
	}
}

// sanitizeFeedbackLabels 清洗预设标签：去空、去重、单个限 20 字、最多 6 个。
func sanitizeFeedbackLabels(raw string) string {
	parts := strings.Split(raw, ",")
	seen := map[string]bool{}
	out := make([]string, 0, 6)
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		if tag == "" || seen[tag] || len([]rune(tag)) > 20 {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) >= 6 {
			break
		}
	}
	return strings.Join(out, ",")
}

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

func generationFeedbackPayload(feedback model.GenerationFeedback) gin.H {
	// 未携带标签时输出空数组而不是 [""]。
	labels := []string{}
	if feedback.Labels != "" {
		labels = strings.Split(feedback.Labels, ",")
	}
	return gin.H{
		"generationId": feedback.GenerationID.String(),
		"rating":       feedback.Rating,
		"labels":       labels,
		"note":         feedback.Note,
		"updatedAt":    httpx.FormatTime(feedback.UpdatedAt),
	}
}
