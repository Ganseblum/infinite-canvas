package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
)

var assetKinds = map[string]struct{}{"text": {}, "image": {}, "video": {}}

var assetSortColumns = map[string]string{
	"updatedAt": "updated_at",
	"createdAt": "created_at",
	"title":     "title",
}

// AssetHandler 素材资源。列表返回完整对象（data 只有元数据），
// 第一页附带该用户标签全集 facets。
type AssetHandler struct {
	db *gorm.DB
}

func NewAssetHandler(db *gorm.DB) *AssetHandler { return &AssetHandler{db: db} }

type assetCreateReq struct {
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Tags       []string        `json:"tags"`
	Data       json.RawMessage `json:"data"`
	StorageKey string          `json:"storageKey"`
	Bytes      int64           `json:"bytes"`
}

type assetPatchReq struct {
	Title      *string          `json:"title"`
	Tags       *[]string        `json:"tags"`
	Data       *json.RawMessage `json:"data"`
	StorageKey *string          `json:"storageKey"`
	Bytes      *int64           `json:"bytes"`
}

func (h *AssetHandler) List(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	page, ok := parsePageParams(c)
	if !ok {
		return
	}
	order, ok := parseSort(c.Query("sort"), assetSortColumns, "-updatedAt")
	if !ok {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"sort": "sort 不在白名单内"}))
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" {
		if _, valid := assetKinds[kind]; !valid {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"kind": "kind 只能是 text、image 或 video"}))
			return
		}
	}
	search := strings.TrimSpace(c.Query("q"))
	tags := normalizeQueryTags(c.QueryArray("tag"))

	apply := func(db *gorm.DB) *gorm.DB {
		db = db.Where("assets.user_id = ?", uid)
		if kind != "" {
			db = db.Where("assets.kind = ?", kind)
		}
		if search != "" {
			pattern := searchPattern(search)
			db = db.Where("LOWER(assets.title) LIKE ? OR LOWER("+assetContentExpr(h.db.Dialector.Name())+") LIKE ?", pattern, pattern)
		}
		if len(tags) > 0 {
			db = db.Joins("JOIN asset_tags ON asset_tags.asset_id = assets.id").
				Where("asset_tags.tag IN ?", tags).
				Group("assets.id").
				Having("COUNT(DISTINCT asset_tags.tag) = ?", len(tags))
		}
		return db
	}

	var total int64
	if err := h.db.Table("(?) AS filtered", apply(h.db.Model(&model.Asset{})).Select("assets.id")).
		Count(&total).Error; err != nil {
		slog.Error("统计素材失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var assets []model.Asset
	if err := apply(h.db.Model(&model.Asset{})).
		Order(order).Limit(page.Size).Offset((page.Page - 1) * page.Size).
		Find(&assets).Error; err != nil {
		slog.Error("查询素材列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}

	tagMap, err := h.tagsFor(assetIDs(assets))
	if err != nil {
		slog.Error("查询素材标签失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(assets))
	for _, asset := range assets {
		items = append(items, assetPayload(asset, tagMap[asset.ID]))
	}
	payload := gin.H{
		"items": items,
		"total": total,
		"page":  page.Page,
		"size":  page.Size,
	}
	if page.Page == 1 {
		allTags, err := h.allTags(uid)
		if err != nil {
			slog.Error("查询标签全集失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		payload["tags"] = allTags
	}
	c.JSON(http.StatusOK, payload)
}

func (h *AssetHandler) Create(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	var req assetCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if _, valid := assetKinds[req.Kind]; !valid {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"kind": "kind 只能是 text、image 或 video"}))
		return
	}
	if !validTitle(c, req.Title) {
		return
	}
	if req.Bytes < 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"bytes": "bytes 不能为负数"}))
		return
	}
	if req.StorageKey != "" && !storageKeyRe.MatchString(req.StorageKey) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"storageKey": "storageKey 格式不合法"}))
		return
	}
	data, err := normalizeJSONObject(req.Data)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"data": "data 必须是 JSON 对象"}))
		return
	}
	tags, fieldErrs := normalizeTags(req.Tags)
	if len(fieldErrs) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fieldErrs))
		return
	}

	asset := model.Asset{
		ID:               uuid.New(),
		UserID:           uid,
		Kind:             req.Kind,
		Title:            req.Title,
		Data:             data,
		StorageKey:       req.StorageKey,
		Bytes:            req.Bytes,
		ModerationStatus: "skipped",
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		return replaceAssetTags(tx, asset.ID, tags)
	})
	if err != nil {
		slog.Error("创建素材失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, assetPayload(asset, tags))
}

func (h *AssetHandler) Get(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	asset, ok := h.findOwned(c, uid)
	if !ok {
		return
	}
	tagMap, err := h.tagsFor([]uuid.UUID{asset.ID})
	if err != nil {
		slog.Error("查询素材标签失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, assetPayload(*asset, tagMap[asset.ID]))
}

func (h *AssetHandler) Patch(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	asset, ok := h.findOwned(c, uid)
	if !ok {
		return
	}
	var req assetPatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if req.Title == nil && req.Tags == nil && req.Data == nil && req.StorageKey == nil && req.Bytes == nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"body": "至少提供一个可更新字段"}))
		return
	}
	if req.Title != nil && !validTitle(c, *req.Title) {
		return
	}
	if req.Bytes != nil && *req.Bytes < 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"bytes": "bytes 不能为负数"}))
		return
	}
	if req.StorageKey != nil && *req.StorageKey != "" && !storageKeyRe.MatchString(*req.StorageKey) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"storageKey": "storageKey 格式不合法"}))
		return
	}
	var newTags []string
	var fieldErrs map[string]string
	if req.Tags != nil {
		newTags, fieldErrs = normalizeTags(*req.Tags)
		if len(fieldErrs) > 0 {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, fieldErrs))
			return
		}
	}

	updates := map[string]any{}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.StorageKey != nil {
		updates["storage_key"] = *req.StorageKey
	}
	if req.Bytes != nil {
		updates["bytes"] = *req.Bytes
	}
	if req.Data != nil {
		data, err := normalizeJSONObject(*req.Data)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"data": "data 必须是 JSON 对象"}))
			return
		}
		updates["data"] = data
	}
	// storageKey/bytes 在列与 data 里各存一份：单独 PATCH 时同步镜像到 data，
	// 避免前端按 data.storageKey 拼出的地址与实际列指向不同对象。
	if req.StorageKey != nil || req.Bytes != nil {
		mirrored, err := mirrorAssetData(asset, updates, req.StorageKey, req.Bytes)
		if err != nil {
			slog.Error("同步素材 data 失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		if mirrored != nil {
			updates["data"] = mirrored
		}
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&model.Asset{}).Where("id = ? AND user_id = ?", asset.ID, uid).Updates(updates).Error; err != nil {
				return err
			}
		}
		if req.Tags != nil {
			return replaceAssetTags(tx, asset.ID, newTags)
		}
		return nil
	})
	if err != nil {
		slog.Error("更新素材失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	updated, ok := h.findOwned(c, uid)
	if !ok {
		return
	}
	tagMap, err := h.tagsFor([]uuid.UUID{updated.ID})
	if err != nil {
		slog.Error("查询素材标签失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, assetPayload(*updated, tagMap[updated.ID]))
}

func (h *AssetHandler) Delete(c *gin.Context) {
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
	if err := h.db.Unscoped().Model(&model.Asset{}).
		Where("id = ? AND user_id = ?", id, uid).Count(&owned).Error; err != nil {
		slog.Error("查询素材归属失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if owned == 0 {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err := h.db.Where("id = ? AND user_id = ?", id, uid).Delete(&model.Asset{}).Error; err != nil {
		slog.Error("删除素材失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
}

func (h *AssetHandler) findOwned(c *gin.Context, uid uuid.UUID) (*model.Asset, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	var asset model.Asset
	err = h.db.Where("id = ? AND user_id = ?", id, uid).First(&asset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	if err != nil {
		slog.Error("查询素材失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	return &asset, true
}

func (h *AssetHandler) tagsFor(ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	tagMap := make(map[uuid.UUID][]string, len(ids))
	if len(ids) == 0 {
		return tagMap, nil
	}
	var rows []model.AssetTag
	if err := h.db.Where("asset_id IN ?", ids).Order("tag ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		tagMap[row.AssetID] = append(tagMap[row.AssetID], row.Tag)
	}
	return tagMap, nil
}

// allTags 返回该用户未删除素材的标签全集，服务 facets。
func (h *AssetHandler) allTags(uid uuid.UUID) ([]string, error) {
	tags := make([]string, 0)
	err := h.db.Table("asset_tags").
		Select("DISTINCT asset_tags.tag").
		Joins("JOIN assets ON assets.id = asset_tags.asset_id").
		Where("assets.user_id = ? AND assets.deleted_at IS NULL", uid).
		Order("asset_tags.tag ASC").
		Pluck("asset_tags.tag", &tags).Error
	return tags, err
}

func assetIDs(assets []model.Asset) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(assets))
	for _, asset := range assets {
		ids = append(ids, asset.ID)
	}
	return ids
}

// replaceAssetTags 用给定集合整体替换素材标签。
func replaceAssetTags(tx *gorm.DB, assetID uuid.UUID, tags []string) error {
	if err := tx.Where("asset_id = ?", assetID).Delete(&model.AssetTag{}).Error; err != nil {
		return err
	}
	if len(tags) == 0 {
		return nil
	}
	rows := make([]model.AssetTag, 0, len(tags))
	for _, tag := range tags {
		rows = append(rows, model.AssetTag{AssetID: assetID, Tag: tag})
	}
	return tx.Create(&rows).Error
}

func normalizeQueryTags(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, tag := range raw {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// normalizeTags 清洗标签数组：去空白、去重、单标签不超过 64 字符。
func normalizeTags(raw []string) ([]string, map[string]string) {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	fields := map[string]string{}
	for i, tag := range raw {
		tag = strings.TrimSpace(tag)
		key := fmt.Sprintf("tags[%d]", i)
		if tag == "" {
			fields[key] = "标签不能为空"
			continue
		}
		if utf8.RuneCountInString(tag) > 64 {
			fields[key] = "标签长度不能超过 64 字符"
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, fields
}

// normalizeJSONObject 校验 data 是 JSON 对象，缺省时返回空对象。
func normalizeJSONObject(raw json.RawMessage) (datatypes.JSON, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return datatypes.JSON([]byte("{}")), nil
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	return datatypes.JSON(raw), nil
}

// mirrorAssetData 把单独 PATCH 的 storageKey/bytes 同步进 data JSON。
// data 本身也在本次 PATCH 里时以更新后的 data 为基础。
func mirrorAssetData(asset *model.Asset, updates map[string]any, storageKey *string, bytes *int64) (datatypes.JSON, error) {
	var base []byte
	if data, ok := updates["data"]; ok {
		base = []byte(data.(datatypes.JSON))
	} else {
		base = []byte(asset.Data)
	}
	var obj map[string]any
	if err := json.Unmarshal(base, &obj); err != nil {
		return nil, err
	}
	if storageKey != nil {
		obj["storageKey"] = *storageKey
	}
	if bytes != nil {
		obj["bytes"] = *bytes
	}
	merged, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(merged), nil
}

func assetPayload(asset model.Asset, tags []string) gin.H {
	if tags == nil {
		tags = []string{}
	}
	return gin.H{
		"id":         asset.ID.String(),
		"kind":       asset.Kind,
		"title":      asset.Title,
		"tags":       tags,
		"data":       asset.Data,
		"storageKey": asset.StorageKey,
		"bytes":      asset.Bytes,
		"createdAt":  formatTime(asset.CreatedAt),
		"updatedAt":  formatTime(asset.UpdatedAt),
	}
}
