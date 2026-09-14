package handler

import (
	"encoding/base64"
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
	"github.com/infinite-canvas/server/internal/model"
)

var generationKinds = map[string]struct{}{"image": {}, "video": {}}
var generationStatuses = map[string]struct{}{"pending": {}, "success": {}, "failed": {}}

// pendingLimit 是 status=pending 不分页查询的安全上限：pending 是短暂状态，
// 真攒到这个量说明任务卡死，截断比无限返回更安全。
const pendingLimit = 100

// GenerationHandler 生成记录。只增历史流：游标分页，无 POST/PATCH，
// 写入与终态收敛由第四期转发链路负责。
type GenerationHandler struct {
	db *gorm.DB
}

func NewGenerationHandler(db *gorm.DB) *GenerationHandler { return &GenerationHandler{db: db} }

func (h *GenerationHandler) List(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	size, ok := parseGenerationSize(c)
	if !ok {
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" {
		if _, valid := generationKinds[kind]; !valid {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"kind": "kind 只能是 image 或 video"}))
			return
		}
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" {
		if _, valid := generationStatuses[status]; !valid {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 只能是 pending、success 或 failed"}))
			return
		}
	}

	base := func() *gorm.DB {
		db := h.db.Model(&model.Generation{}).Where("user_id = ?", uid)
		if kind != "" {
			db = db.Where("kind = ?", kind)
		}
		return db
	}

	// pending 是唯一不分页的取值：一次返回全部，上限 100，nextCursor 恒为 null。
	if status == "pending" {
		var items []model.Generation
		if err := base().Where("status = ?", "pending").
			Order("created_at DESC, id DESC").Limit(pendingLimit).
			Find(&items).Error; err != nil {
			slog.Error("查询 pending 生成记录失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": generationPayloads(items), "nextCursor": nil})
		return
	}

	cursor := strings.TrimSpace(c.Query("cursor"))
	var cursorTime time.Time
	var cursorID uuid.UUID
	if cursor != "" {
		var err error
		cursorTime, cursorID, err = decodeGenerationCursor(cursor)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"cursor": "游标无法解析"}))
			return
		}
	}

	query := base()
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if cursor != "" {
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursorTime, cursorTime, cursorID)
	}
	var items []model.Generation
	if err := query.Order("created_at DESC, id DESC").Limit(size + 1).Find(&items).Error; err != nil {
		slog.Error("查询生成记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var nextCursor *string
	if len(items) > size {
		items = items[:size]
		last := items[len(items)-1]
		next := encodeGenerationCursor(last.CreatedAt, last.ID)
		nextCursor = &next
	}
	c.JSON(http.StatusOK, gin.H{"items": generationPayloads(items), "nextCursor": nextCursor})
}

func (h *GenerationHandler) Get(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	var item model.Generation
	err := h.db.Where("id = ? AND user_id = ?", c.Param("id"), uid).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("查询生成记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, generationPayload(item))
}

func (h *GenerationHandler) Delete(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	// 跨用户统一 404；自己的记录（含已软删）重复删除仍返回 204。
	var owned int64
	if err := h.db.Unscoped().Model(&model.Generation{}).
		Where("id = ? AND user_id = ?", id, uid).Count(&owned).Error; err != nil {
		slog.Error("查询生成记录归属失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if owned == 0 {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err := h.db.Where("id = ? AND user_id = ?", id, uid).Delete(&model.Generation{}).Error; err != nil {
		slog.Error("删除生成记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
}

func parseGenerationSize(c *gin.Context) (int, bool) {
	raw := c.Query("size")
	if raw == "" {
		return defaultPageSize, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPageSize {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"size": "size 必须是 1-100 的整数"}))
		return 0, false
	}
	return n, true
}

// encodeGenerationCursor 编码 base64url(RFC3339Nano + "|" + id)，对客户端不透明。
func encodeGenerationCursor(createdAt time.Time, id uuid.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeGenerationCursor(cursor string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	sep := strings.LastIndex(string(raw), "|")
	if sep <= 0 || sep == len(raw)-1 {
		return time.Time{}, uuid.Nil, errors.New("游标格式错误")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, string(raw[:sep]))
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(string(raw[sep+1:]))
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return createdAt, id, nil
}

func generationPayloads(items []model.Generation) []gin.H {
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, generationPayload(item))
	}
	return out
}

func generationPayload(item model.Generation) gin.H {
	return gin.H{
		"id":         item.ID.String(),
		"kind":       item.Kind,
		"status":     item.Status,
		"prompt":     item.Prompt,
		"model":      item.Model,
		"config":     item.Config,
		"result":     item.Result,
		"durationMs": item.DurationMs,
		"createdAt":  formatTime(item.CreatedAt),
	}
}
