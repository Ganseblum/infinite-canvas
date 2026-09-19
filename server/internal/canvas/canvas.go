package canvas

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
)

// CanvasHandler 画布资源：列表只回摘要，详情含 data，PUT 走单条 revision 乐观锁。
type CanvasHandler struct {
	db *gorm.DB
}

func NewCanvasHandler(db *gorm.DB) *CanvasHandler { return &CanvasHandler{db: db} }

var canvasSortColumns = map[string]string{
	"updatedAt": "updated_at",
	"createdAt": "created_at",
	"title":     "title",
}

type canvasCreateReq struct {
	Title string          `json:"title"`
	Data  json.RawMessage `json:"data"`
}

type canvasUpdateReq struct {
	Data     json.RawMessage `json:"data"`
	Revision int64           `json:"revision"`
}

type canvasPatchReq struct {
	Title *string `json:"title"`
}

func (h *CanvasHandler) List(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	page, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	order, ok := httpx.ParseSort(c.Query("sort"), canvasSortColumns, "-updatedAt")
	if !ok {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"sort": "sort 不在白名单内"}))
		return
	}
	search := strings.TrimSpace(c.Query("q"))

	apply := func(db *gorm.DB) *gorm.DB {
		db = db.Where("user_id = ?", uid)
		if search != "" {
			db = db.Where("LOWER(title) LIKE ?", httpx.SearchPattern(search))
		}
		return db
	}

	var total int64
	if err := apply(h.db.Model(&model.Canvas{})).Count(&total).Error; err != nil {
		slog.Error("统计画布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var canvases []model.Canvas
	if err := apply(h.db.Model(&model.Canvas{})).
		Order(order).Limit(page.Size).Offset((page.Page - 1) * page.Size).
		Find(&canvases).Error; err != nil {
		slog.Error("查询画布列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	items := make([]gin.H, 0, len(canvases))
	for _, cv := range canvases {
		items = append(items, canvasSummary(cv))
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": total,
		"page":  page.Page,
		"size":  page.Size,
	})
}

func (h *CanvasHandler) Create(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var req canvasCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if !validTitle(c, req.Title) {
		return
	}
	data, nodes, connections, cover, ok := analyzeCanvasData(c, req.Data)
	if !ok {
		return
	}

	canvas := model.Canvas{
		ID:              uuid.New(),
		UserID:          uid,
		Title:           req.Title,
		Data:            data,
		Revision:        1,
		NodeCount:       nodes,
		ConnectionCount: connections,
		CoverKey:        cover,
	}
	if err := h.db.Create(&canvas).Error; err != nil {
		slog.Error("创建画布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, canvasDetail(canvas))
}

func (h *CanvasHandler) Get(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	canvas, ok := h.findOwned(c, uid)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, canvasDetail(*canvas))
}

func (h *CanvasHandler) Update(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var req canvasUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if req.Revision < 1 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"revision": "revision 必须是正整数"}))
		return
	}
	data, nodes, connections, cover, ok := analyzeCanvasData(c, req.Data)
	if !ok {
		return
	}

	now := time.Now()
	res := h.db.Model(&model.Canvas{}).
		Where("id = ? AND user_id = ? AND revision = ?", id, uid, req.Revision).
		Updates(map[string]any{
			"data":             data,
			"revision":         gorm.Expr("revision + 1"),
			"node_count":       nodes,
			"connection_count": connections,
			"cover_key":        cover,
			"updated_at":       now,
		})
	if res.Error != nil {
		slog.Error("更新画布失败", "err", res.Error)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected == 0 {
		// 影响行数为 0：再查一次当前记录，存在则带当前 revision 返回 409，否则 404。
		var current model.Canvas
		err := h.db.Where("id = ? AND user_id = ?", id, uid).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		if err != nil {
			slog.Error("查询画布冲突状态失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		errs.Abort(c, errs.WithExtra(errs.ErrRevisionConflict, map[string]any{"revision": current.Revision}))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"revision":  req.Revision + 1,
		"updatedAt": httpx.FormatTime(now),
	})
}

func (h *CanvasHandler) Patch(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var req canvasPatchReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Title == nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if !validTitle(c, *req.Title) {
		return
	}

	now := time.Now()
	res := h.db.Model(&model.Canvas{}).
		Where("id = ? AND user_id = ?", id, uid).
		Updates(map[string]any{"title": *req.Title, "updated_at": now})
	if res.Error != nil {
		slog.Error("修改画布元数据失败", "err", res.Error)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected == 0 {
		// 标题与现值相同可能影响 0 行，先确认记录是否存在。
		var current model.Canvas
		err := h.db.Where("id = ? AND user_id = ?", id, uid).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		if err != nil {
			slog.Error("查询画布失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"id":        current.ID.String(),
			"title":     current.Title,
			"updatedAt": httpx.FormatTime(current.UpdatedAt),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":        id.String(),
		"title":     *req.Title,
		"updatedAt": httpx.FormatTime(now),
	})
}

func (h *CanvasHandler) Delete(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
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
	if err := h.db.Unscoped().Model(&model.Canvas{}).
		Where("id = ? AND user_id = ?", id, uid).Count(&owned).Error; err != nil {
		slog.Error("查询画布归属失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if owned == 0 {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err := h.db.Where("id = ? AND user_id = ?", id, uid).Delete(&model.Canvas{}).Error; err != nil {
		slog.Error("删除画布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	httpx.NoContent(c)
}

func (h *CanvasHandler) findOwned(c *gin.Context, uid uuid.UUID) (*model.Canvas, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	var canvas model.Canvas
	err = h.db.Where("id = ? AND user_id = ?", id, uid).First(&canvas).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	if err != nil {
		slog.Error("查询画布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	return &canvas, true
}

func validTitle(c *gin.Context, title string) bool {
	if utf8.RuneCountInString(title) > 200 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"title": "标题长度不能超过 200 字符"}))
		return false
	}
	return true
}

// analyzeCanvasData 校验 data 并算出列表页冗余字段。nodeCount/connectionCount
// 与 coverKey 一律由服务端从 data 计算，客户端传了也忽略。
func analyzeCanvasData(c *gin.Context, raw json.RawMessage) (datatypes.JSON, int, int, string, bool) {
	data := datatypes.JSON(raw)
	if len(raw) == 0 || string(raw) == "null" {
		data = datatypes.JSON([]byte("{}"))
	}
	if len(data) > maxCanvasDataBytes {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"data": "画布数据超过 2 MB 上限"}))
		return nil, 0, 0, "", false
	}
	var shape struct {
		Nodes       []json.RawMessage `json:"nodes"`
		Connections []json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"data": "画布数据必须是 JSON 对象"}))
		return nil, 0, 0, "", false
	}

	cover := ""
	for _, rawNode := range shape.Nodes {
		var node struct {
			Type     string `json:"type"`
			Metadata struct {
				StorageKey string `json:"storageKey"`
				Images     []struct {
					StorageKey string `json:"storageKey"`
				} `json:"images"`
			} `json:"metadata"`
		}
		if json.Unmarshal(rawNode, &node) != nil || node.Type != "image" {
			continue
		}
		key := node.Metadata.StorageKey
		if key == "" {
			for _, img := range node.Metadata.Images {
				if img.StorageKey != "" {
					key = img.StorageKey
					break
				}
			}
		}
		if model.StorageKeyRe.MatchString(key) {
			cover = key
			break
		}
	}
	return data, len(shape.Nodes), len(shape.Connections), cover, true
}

func canvasSummary(cv model.Canvas) gin.H {
	return gin.H{
		"id":              cv.ID.String(),
		"title":           cv.Title,
		"nodeCount":       cv.NodeCount,
		"connectionCount": cv.ConnectionCount,
		"coverKey":        cv.CoverKey,
		"updatedAt":       httpx.FormatTime(cv.UpdatedAt),
	}
}

func canvasDetail(cv model.Canvas) gin.H {
	payload := canvasSummary(cv)
	payload["data"] = cv.Data
	payload["revision"] = cv.Revision
	payload["createdAt"] = httpx.FormatTime(cv.CreatedAt)
	return payload
}

// maxCanvasDataBytes 是画布 data 的字节上限（第一期约定「建议 2 MB」）。
// 反代已限 2m，这里再兜一层，直连 8080 的请求同样会被拦住。
const maxCanvasDataBytes = 2 * 1024 * 1024
