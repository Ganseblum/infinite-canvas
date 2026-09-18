package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

// listablePublic 素材未被软删的已发布作品才进公开流：源素材删除后反查到零值
// asset 会渲染出空 src 的卡片（差异清单 #96），列表、热门、详情、主页共用该口径。
const listablePublic = `status = 'published' AND EXISTS (
	SELECT 1 FROM assets a WHERE a.id = community_works.asset_id AND a.deleted_at IS NULL)`

// CommunityHandler 提供社区的发布、浏览、点赞与举报。
// 内容本体复用用户素材（assets），媒体与审核复用第二、五期链路。
type CommunityHandler struct {
	db       *gorm.DB
	settings *service.SiteSettingService
}

func NewCommunityHandler(db *gorm.DB, settings *service.SiteSettingService) *CommunityHandler {
	return &CommunityHandler{db: db, settings: settings}
}

func (h *CommunityHandler) enabled() bool {
	if h.settings == nil {
		return true
	}
	return h.settings.CommunityEnabled()
}

// List 公开作品流：游标分页，支持关键词与标签。
func (h *CommunityHandler) List(c *gin.Context) {
	if !h.enabled() {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"community": "社区暂未开放"}))
		return
	}
	size, err := parseCursorSize(c.Query("size"))
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"size": "size 必须是 1-100 的整数"}))
		return
	}
	query := h.db.Model(&model.CommunityWork{}).Where(listablePublic)
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		pattern := searchPattern(q)
		query = query.Where("LOWER(title) LIKE ? OR LOWER(description) LIKE ? OR LOWER(tags) LIKE ?", pattern, pattern, pattern)
	}
	if tag := strings.TrimSpace(c.Query("tag")); tag != "" {
		query = query.Where("tags LIKE ?", "%"+tag+"%")
	}
	if userId := strings.TrimSpace(c.Query("userId")); userId != "" {
		parsed, err := uuid.Parse(userId)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"userId": "userId 不合法"}))
			return
		}
		query = query.Where("user_id = ?", parsed)
	}
	// 排序：热门按点赞数，最新按创建时间（游标仅用于最新）。
	sortKey := c.DefaultQuery("sort", "latest")
	if sortKey == "hot" {
		works, err := h.queryHot(c)
		if err != nil {
			slog.Error("读取社区作品失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": h.payloads(works), "nextCursor": nil})
		return
	}
	if cursor := c.Query("cursor"); cursor != "" {
		createdAt, id, err := service.DecodeCursor(cursor)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"cursor": "游标不合法"}))
			return
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", createdAt, createdAt, id)
	}
	var works []model.CommunityWork
	if err := query.Order("created_at DESC, id DESC").Limit(size + 1).Find(&works).Error; err != nil {
		slog.Error("读取社区作品失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	nextCursor := ""
	if len(works) > size {
		works = works[:size]
		last := works[len(works)-1]
		nextCursor = service.EncodeCursor(last.CreatedAt, last.ID)
	}
	payload := gin.H{"items": h.payloads(works), "nextCursor": nil}
	if nextCursor != "" {
		payload["nextCursor"] = nextCursor
	}
	c.JSON(http.StatusOK, payload)
}

// Get 作品详情，附带当前用户是否已点赞。
func (h *CommunityHandler) Get(c *gin.Context) {
	work, ok := h.loadWork(c)
	if !ok {
		return
	}
	// 详情对公众只展示源素材仍存在的作品；loadWork 不加该条件是为了
	// 保留作者删除自己作品与管理下架的入口（软删素材后作品已不可见）。
	var alive int64
	if err := h.db.Model(&model.Asset{}).Where("id = ?", work.AssetID).Count(&alive).Error; err != nil {
		slog.Error("读取社区作品素材失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if alive == 0 {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	payload := h.payloads([]model.CommunityWork{*work})[0]
	if uid, err := uuid.Parse(c.GetString("user_id")); err == nil {
		var count int64
		h.db.Model(&model.CommunityLike{}).Where("work_id = ? AND user_id = ?", work.ID, uid).Count(&count)
		payload["liked"] = count > 0
	}
	c.JSON(http.StatusOK, gin.H{"work": payload})
}

// MyWorks 返回当前用户发布过的作品（含未通过审核时隐藏的状态）。
func (h *CommunityHandler) MyWorks(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var works []model.CommunityWork
	if err := h.db.Where("user_id = ?", uid).Order("created_at DESC").Limit(100).Find(&works).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": h.payloads(works)})
}

// communityDailyPublishLimit 是每用户每日发布上限：公开流是共享资源，
// 防止刷屏灌水（差异清单 #38）。路由级 RateLimit 由 main.go 另行挂载，管的是瞬时频率。
const communityDailyPublishLimit = 20

type publishReq struct {
	AssetID      string `json:"assetId"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Tags         string `json:"tags"`
	SourceWorkID string `json:"sourceWorkId"`
}

// Publish 把用户素材发布到社区。素材本身已在上传时过审，这里只校验归属与类型。
func (h *CommunityHandler) Publish(c *gin.Context) {
	if !h.enabled() {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"community": "社区暂未开放"}))
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var req publishReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Title) == "" || req.AssetID == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	assetID, err := uuid.Parse(req.AssetID)
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"assetId": "assetId 不合法"}))
		return
	}
	// 每用户每日发布上限，按自然日统计（含已删除/下架作品，防删除重发绕过）。
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var publishedToday int64
	if err := h.db.Model(&model.CommunityWork{}).
		Where("user_id = ? AND created_at >= ?", uid, dayStart).Count(&publishedToday).Error; err != nil {
		slog.Error("统计今日发布数失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if publishedToday >= communityDailyPublishLimit {
		errs.Abort(c, errs.ErrRateLimited)
		return
	}
	var asset model.Asset
	if err := h.db.Where("id = ? AND user_id = ?", assetID, uid).First(&asset).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if asset.Kind != "image" && asset.Kind != "video" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"assetId": "只有图片或视频素材可以发布到社区"}))
		return
	}
	// 内容去重：同一素材同一天重复发布同一标题返回 409。
	title := strings.TrimSpace(req.Title)
	var dup int64
	if err := h.db.Model(&model.CommunityWork{}).
		Where("user_id = ? AND asset_id = ? AND title = ? AND created_at >= ?", uid, assetID, title, dayStart).
		Count(&dup).Error; err != nil {
		slog.Error("检查重复发布失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if dup > 0 {
		errs.Abort(c, errs.AddConflict("该素材今日已发布过同标题的作品"))
		return
	}
	work := &model.CommunityWork{
		ID:           uuid.New(),
		UserID:       uid,
		AssetID:      assetID,
		Title:        title,
		Description:  strings.TrimSpace(req.Description),
		Tags:         normalizeCommunityTags(req.Tags),
		CoverKey:     asset.StorageKey,
		Status:       "published",
		ModerationOK: true,
	}
	if req.SourceWorkID != "" {
		sourceID, err := uuid.Parse(req.SourceWorkID)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"sourceWorkId": "sourceWorkId 不合法"}))
			return
		}
		var source model.CommunityWork
		if err := h.db.Where("id = ? AND status = ?", sourceID, "published").First(&source).Error; err != nil {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		work.SourceWorkID = &sourceID
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(work).Error; err != nil {
			return err
		}
		if work.SourceWorkID != nil {
			return tx.Model(&model.CommunityWork{}).Where("id = ?", *work.SourceWorkID).
				Update("remix_count", gorm.Expr("remix_count + 1")).Error
		}
		return nil
	}); err != nil {
		slog.Error("发布作品失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"work": h.payloads([]model.CommunityWork{*work})[0]})
}

// Delete 作者本人删除自己的作品；管理员下架走管理接口。
func (h *CommunityHandler) Delete(c *gin.Context) {
	uid, _ := uuid.Parse(c.GetString("user_id"))
	work, ok := h.loadWork(c)
	if !ok {
		return
	}
	if work.UserID != uid {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err := h.db.Model(&model.CommunityWork{}).Where("id = ?", work.ID).Update("status", "removed").Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
}

// Like 点赞与取消点赞，重复调用幂等，计数在同一事务内更新。
func (h *CommunityHandler) Like(c *gin.Context) {
	work, ok := h.loadWork(c)
	if !ok {
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	// 路由参数名是 :id，动作从路径后缀判断，不能用不存在的 :action。
	unlike := strings.HasSuffix(c.Request.URL.Path, "/unlike")
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if unlike {
			result := tx.Where("work_id = ? AND user_id = ?", work.ID, uid).Delete(&model.CommunityLike{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				return tx.Model(&model.CommunityWork{}).Where("id = ?", work.ID).
					Update("like_count", gorm.Expr("like_count - 1")).Error
			}
			return nil
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CommunityLike{
			WorkID: work.ID, UserID: uid, CreatedAt: time.Now(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return tx.Model(&model.CommunityWork{}).Where("id = ?", work.ID).
				Update("like_count", gorm.Expr("like_count + 1")).Error
		}
		return nil
	})
	if err != nil {
		slog.Error("点赞失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var count int64
	h.db.Model(&model.CommunityLike{}).Where("work_id = ?", work.ID).Count(&count)
	c.JSON(http.StatusOK, gin.H{"likeCount": count, "liked": !unlike})
}

type reportReq struct {
	Reason string `json:"reason"`
}

// Report 举报作品；同一用户对同一作品只能举报一次。
func (h *CommunityHandler) Report(c *gin.Context) {
	work, ok := h.loadWork(c)
	if !ok {
		return
	}
	uid, _ := uuid.Parse(c.GetString("user_id"))
	var req reportReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	report := &model.CommunityReport{
		ID:        uuid.New(),
		WorkID:    work.ID,
		UserID:    uid,
		Reason:    strings.TrimSpace(req.Reason),
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(report).Error; err != nil {
			return err
		}
		return tx.Model(&model.CommunityWork{}).Where("id = ?", work.ID).
			Update("report_count", gorm.Expr("report_count + 1")).Error
	}); err != nil {
		if service.IsDuplicateKey(err) {
			errs.Abort(c, errs.AddConflict("你已举报过该作品"))
			return
		}
		slog.Error("举报失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "pending"})
}

// UserProfile 用户主页：公开作品与统计。
func (h *CommunityHandler) UserProfile(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var user model.PlatformUser
	if err := h.db.Select("id", "username", "display_name", "avatar_url", "created_at").
		First(&user, "id = ?", userID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var works []model.CommunityWork
	if err := h.db.Where("user_id = ?", userID).Where(listablePublic).
		Order("created_at DESC").Limit(60).Find(&works).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var likes int64
	for _, work := range works {
		likes += int64(work.LikeCount)
	}
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":          user.ID.String(),
			"username":    user.Username,
			"displayName": user.DisplayName,
			"avatarUrl":   user.AvatarURL,
			"createdAt":   formatTime(user.CreatedAt),
		},
		"stats": gin.H{
			"works": len(works),
			"likes": likes,
		},
		"items": h.payloads(works),
	})
}

func (h *CommunityHandler) queryHot(c *gin.Context) ([]model.CommunityWork, error) {
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "168"))
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var works []model.CommunityWork
	err := h.db.Where(listablePublic+" AND created_at >= ?", since).
		Order("like_count DESC, created_at DESC").Limit(50).Find(&works).Error
	return works, err
}

func (h *CommunityHandler) loadWork(c *gin.Context) (*model.CommunityWork, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	var work model.CommunityWork
	if err := h.db.First(&work, "id = ? AND status = ?", id, "published").Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return nil, false
		}
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	return &work, true
}

// payloads 把作品转成响应结构，并补上作者信息与媒体地址。
func (h *CommunityHandler) payloads(works []model.CommunityWork) []gin.H {
	if len(works) == 0 {
		return []gin.H{}
	}
	userIDs := make([]uuid.UUID, 0, len(works))
	workIDs := make([]uuid.UUID, 0, len(works))
	assetIDs := make([]uuid.UUID, 0, len(works))
	for _, work := range works {
		userIDs = append(userIDs, work.UserID)
		workIDs = append(workIDs, work.ID)
		assetIDs = append(assetIDs, work.AssetID)
	}
	var users []model.PlatformUser
	h.db.Where("id IN ?", userIDs).Find(&users)
	userByID := map[uuid.UUID]model.PlatformUser{}
	for _, user := range users {
		userByID[user.ID] = user
	}
	var assets []model.Asset
	h.db.Where("id IN ?", assetIDs).Find(&assets)
	assetByID := map[uuid.UUID]model.Asset{}
	for _, asset := range assets {
		assetByID[asset.ID] = asset
	}
	items := make([]gin.H, 0, len(works))
	for _, work := range works {
		author := userByID[work.UserID]
		asset := assetByID[work.AssetID]
		item := gin.H{
			"id":          work.ID.String(),
			"title":       work.Title,
			"description": work.Description,
			"tags":        strings.Split(strings.Trim(work.Tags, ","), ","),
			"kind":        asset.Kind,
			"storageKey":  asset.StorageKey,
			"coverKey":    work.CoverKey,
			"likeCount":   work.LikeCount,
			"remixCount":  work.RemixCount,
			"reportCount": work.ReportCount,
			"createdAt":   formatTime(work.CreatedAt),
			"author": gin.H{
				"id":          author.ID.String(),
				"username":    author.Username,
				"displayName": author.DisplayName,
				"avatarUrl":   author.AvatarURL,
			},
		}
		if work.SourceWorkID != nil {
			item["sourceWorkId"] = work.SourceWorkID.String()
		}
		items = append(items, item)
	}
	return items
}

// normalizeTags 把逗号分隔的标签去空、去重并限制总长度。
func normalizeCommunityTags(raw string) string {
	parts := strings.Split(raw, ",")
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		if tag == "" || seen[tag] || len([]rune(tag)) > 16 {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) >= 8 {
			break
		}
	}
	return strings.Join(out, ",")
}
