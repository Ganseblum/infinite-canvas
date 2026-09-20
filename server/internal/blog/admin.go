package blog

import (
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

// 博客管理面端点（17 个）：路由与权限点在 admin 包 RegisterRoutes 注册，
// 本文件只实现 handler。内容变更统一触发按需再验证。

// postWriteReq 文章新建/更新共用的写请求；不含 status 字段，
// 状态迁移只能走 publish/unpublish 专用端点。
type postWriteReq struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	TopicID     uint   `json:"topicId"`
	VolNo       int    `json:"volNo"`
	CoverSeed   string `json:"coverSeed"`
	IsAIGCCover bool   `json:"isAigcCover"`
	IsPinned    bool   `json:"isPinned"`
	OriginURL   string `json:"originUrl"`
	Tags        string `json:"tags"`
	ContentMD   string `json:"contentMd"`
}

// validatePostWrite 校验文章写请求，返回规整后的字段；summary 空则截取正文。
func validatePostWrite(req postWriteReq) (postWriteReq, map[string]string) {
	req.Slug = strings.TrimSpace(req.Slug)
	req.Title = strings.TrimSpace(req.Title)
	req.Tags = strings.Join(strings.Fields(strings.ReplaceAll(req.Tags, "，", ",")), "")
	fields := map[string]string{}
	if !validSlug(req.Slug) {
		fields["slug"] = "slug 需为小写字母数字与单连字符，1-128 位"
	}
	if l := len([]rune(req.Title)); l < 1 || l > 255 {
		fields["title"] = "标题需在 1-255 字之间"
	}
	if l := len([]rune(req.Summary)); l > 500 {
		fields["summary"] = "摘要不能超过 500 字"
	}
	if req.TopicID == 0 {
		fields["topicId"] = "必须选择栏目"
	}
	if l := len([]rune(req.ContentMD)); l < 1 {
		fields["contentMd"] = "正文不能为空"
	}
	if req.OriginURL != "" && len(req.OriginURL) > 500 {
		fields["originUrl"] = "转载链接过长"
	}
	if len(fields) == 0 && req.Summary == "" {
		req.Summary = SummaryFromContent(req.ContentMD)
	}
	return req, fields
}

// applyWriteReq 把写请求落到模型（不含状态迁移），并重算字数。
func applyWriteReq(post *model.BlogPost, req postWriteReq) {
	post.Slug, post.Title, post.Summary = req.Slug, req.Title, req.Summary
	post.TopicID, post.VolNo = req.TopicID, req.VolNo
	post.CoverSeed, post.IsAIGCCover, post.IsPinned = req.CoverSeed, req.IsAIGCCover, req.IsPinned
	post.OriginURL, post.Tags, post.ContentMD = req.OriginURL, req.Tags, req.ContentMD
	post.WordCount = CountWords(req.ContentMD)
}

// ListPosts 管理侧文章列表：全状态可见，status/topic 筛选 + 分页，草稿在前按更新时间。
func (h *AdminHandler) ListPosts(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.BlogPost{})
	if status := c.Query("status"); status != "" {
		if !model.BlogPostStatusValid(status) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
			return
		}
		query = query.Where("status = ?", status)
	}
	if topic := c.Query("topicId"); topic != "" {
		id, err := strconvAtoiUint(topic)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"topicId": "topicId 非法"}))
			return
		}
		query = query.Where("topic_id = ?", id)
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		query = query.Where("title LIKE ?", "%"+q+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var posts []model.BlogPost
	if err := query.Order("status = 'published' DESC, is_pinned DESC, updated_at DESC").
		Limit(params.Size).Offset((params.Page - 1) * params.Size).Find(&posts).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	topics, _ := loadTopicMap(h.db)
	items := make([]PostPayload, 0, len(posts))
	for _, p := range posts {
		items = append(items, postListItem(p, topics[p.TopicID]))
	}
	c.JSON(http.StatusOK, gin.H{"posts": items, "total": total, "page": params.Page, "size": params.Size})
}

// parseUintParam 解析自增主键参数。
func parseUintParam(c *gin.Context) (uint, bool) {
	id, err := strconvAtoiUint(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return 0, false
	}
	return id, true
}

// strconvAtoiUint 解析非零纯数字串；超过 1<<31 视为非法，防主键溢出。
func strconvAtoiUint(s string) (uint, error) {
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errs.ErrValidation
		}
		n = n*10 + uint64(r-'0')
		if n > 1<<31 {
			return 0, errs.ErrValidation
		}
	}
	if n == 0 {
		return 0, errs.ErrValidation
	}
	return uint(n), nil
}

// GetPost 管理侧详情：含 content_md 原文（编辑器回填用）。
func (h *AdminHandler) GetPost(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var topic model.BlogTopic
	_ = h.db.First(&topic, "id = ?", post.TopicID).Error
	c.JSON(http.StatusOK, gin.H{
		"post":      postListItem(post, topic),
		"contentMd": post.ContentMD,
	})
}

// CreatePost 新建文章（默认草稿）。
func (h *AdminHandler) CreatePost(c *gin.Context) {
	var req postWriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	req, fields := validatePostWrite(req)
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	var topic model.BlogTopic
	if err := h.db.First(&topic, "id = ?", req.TopicID).Error; err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"topicId": "栏目不存在"}))
		return
	}
	post := model.BlogPost{ID: uuid.New(), Status: model.BlogPostStatusDraft, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	applyWriteReq(&post, req)
	if err := h.db.Create(&post).Error; err != nil {
		writeError(c, err, "创建博客文章失败")
		return
	}
	h.notifyListOnly()
	c.JSON(http.StatusCreated, gin.H{"post": gin.H{"id": post.ID.String(), "slug": post.Slug, "status": post.Status}})
}

// notifyListOnly 新建草稿不影响已发布前台，但列表页可能展示草稿计数等，保守失效列表。
func (h *AdminHandler) notifyListOnly() {
	if h.rev != nil {
		h.rev.NotifyList()
	}
}

// UpdatePost 更新文章（状态迁移走 publish/unpublish 专用端点）。
func (h *AdminHandler) UpdatePost(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var req postWriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	req, fields := validatePostWrite(req)
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	var topic model.BlogTopic
	if err := h.db.First(&topic, "id = ?", req.TopicID).Error; err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"topicId": "栏目不存在"}))
		return
	}
	oldSlug := post.Slug
	applyWriteReq(&post, req)
	post.UpdatedAt = time.Now()
	if err := h.db.Save(&post).Error; err != nil {
		writeError(c, err, "更新博客文章失败")
		return
	}
	if post.Status == model.BlogPostStatusPublished {
		h.notifyPost(post.Slug)
		if oldSlug != post.Slug {
			// slug 变更后旧地址已失效，让其 404 即可（不缓存旧 slug）。
			h.notifyListOnly()
		}
	} else {
		h.notifyListOnly()
	}
	c.JSON(http.StatusOK, gin.H{"post": gin.H{"id": post.ID.String(), "slug": post.Slug, "status": post.Status}})
}

// PublishPost 发布：published_at 只在首发时写入。
func (h *AdminHandler) PublishPost(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if post.Status == model.BlogPostStatusPublished {
		c.JSON(http.StatusOK, gin.H{"post": gin.H{"id": post.ID.String(), "status": post.Status}})
		return
	}
	if err := publishPost(h.db, &post); err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	h.notifyPost(post.Slug)
	c.JSON(http.StatusOK, gin.H{"post": gin.H{"id": post.ID.String(), "status": post.Status, "publishedAt": post.PublishedAt}})
}

// UnpublishPost 下架：前台详情立即 404，首发时间保留。
func (h *AdminHandler) UnpublishPost(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err := unpublishPost(h.db, &post); err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	h.notifyPost(post.Slug)
	c.JSON(http.StatusOK, gin.H{"post": gin.H{"id": post.ID.String(), "status": post.Status}})
}

// DeletePost 物理删除：连同互动明细一并清理（点赞/收藏/评论）。
func (h *AdminHandler) DeletePost(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("post_id = ?", post.ID).Delete(&model.BlogComment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("target_type = ? AND target_id = ?", model.BlogTargetPost, post.ID).Delete(&model.BlogReaction{}).Error; err != nil {
			return err
		}
		if err := tx.Where("post_id = ?", post.ID).Delete(&model.BlogBookmark{}).Error; err != nil {
			return err
		}
		return tx.Delete(&post).Error
	})
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	h.notifyPost(post.Slug)
	c.Status(http.StatusNoContent)
}

// ========== 栏目管理 ==========

// topicWriteReq 栏目新建/更新共用的写请求。
type topicWriteReq struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	EnName      string `json:"enName"`
	Description string `json:"description"`
	Sort        int    `json:"sort"`
}

// validateTopicWrite 校验栏目写请求，返回规整后的字段；口径同 validatePostWrite。
func validateTopicWrite(req topicWriteReq) (topicWriteReq, map[string]string) {
	req.Slug, req.Name = strings.TrimSpace(req.Slug), strings.TrimSpace(req.Name)
	req.EnName = strings.TrimSpace(req.EnName)
	fields := map[string]string{}
	if !validSlug(req.Slug) {
		fields["slug"] = "slug 需为小写字母数字与单连字符，1-64 位"
	}
	if l := len([]rune(req.Name)); l < 1 || l > 64 {
		fields["name"] = "栏目名需在 1-64 字之间"
	}
	if l := len([]rune(req.EnName)); l > 64 {
		fields["enName"] = "英文名不能超过 64 字"
	}
	if l := len([]rune(req.Description)); l > 255 {
		fields["description"] = "描述不能超过 255 字"
	}
	return req, fields
}

// ListTopics 管理侧栏目列表：含全状态文章数（删除保护提示用）。
func (h *AdminHandler) ListTopics(c *gin.Context) {
	var topics []model.BlogTopic
	if err := h.db.Order("sort ASC, id ASC").Find(&topics).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	type counted struct {
		TopicID uint
		Total   int64
	}
	var counts []counted
	if err := h.db.Model(&model.BlogPost{}).Select("topic_id, COUNT(*) as total").
		Group("topic_id").Scan(&counts).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	countMap := make(map[uint]int64, len(counts))
	for _, ct := range counts {
		countMap[ct.TopicID] = ct.Total
	}
	items := make([]gin.H, 0, len(topics))
	for _, t := range topics {
		items = append(items, gin.H{
			"id": t.ID, "slug": t.Slug, "name": t.Name, "enName": t.EnName,
			"description": t.Description, "sort": t.Sort, "postCount": countMap[t.ID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"topics": items})
}

// CreateTopic 新增栏目。
func (h *AdminHandler) CreateTopic(c *gin.Context) {
	var req topicWriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	req, fields := validateTopicWrite(req)
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	topic := model.BlogTopic{Slug: req.Slug, Name: req.Name, EnName: req.EnName, Description: req.Description, Sort: req.Sort}
	if err := h.db.Create(&topic).Error; err != nil {
		writeError(c, err, "创建博客栏目失败")
		return
	}
	h.notifyTopics()
	c.JSON(http.StatusCreated, gin.H{"topic": gin.H{"id": topic.ID, "slug": topic.Slug}})
}

// UpdateTopic 修改栏目。
func (h *AdminHandler) UpdateTopic(c *gin.Context) {
	var topic model.BlogTopic
	id, ok := parseUintParam(c)
	if !ok {
		return
	}
	if err := h.db.First(&topic, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var req topicWriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	req, fields := validateTopicWrite(req)
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	topic.Slug, topic.Name, topic.EnName, topic.Description, topic.Sort =
		req.Slug, req.Name, req.EnName, req.Description, req.Sort
	if err := h.db.Save(&topic).Error; err != nil {
		writeError(c, err, "更新博客栏目失败")
		return
	}
	h.notifyTopics()
	c.JSON(http.StatusOK, gin.H{"topic": gin.H{"id": topic.ID, "slug": topic.Slug}})
}

// DeleteTopic 删除栏目：被文章引用（任意状态）时 409 并返回引用数。
func (h *AdminHandler) DeleteTopic(c *gin.Context) {
	id, ok := parseUintParam(c)
	if !ok {
		return
	}
	var topic model.BlogTopic
	if err := h.db.First(&topic, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var count int64
	if err := h.db.Model(&model.BlogPost{}).Where("topic_id = ?", id).Count(&count).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if count > 0 {
		errs.Abort(c, errs.WithExtra(errs.ErrTopicInUse, map[string]any{"postCount": count}))
		return
	}
	if err := h.db.Delete(&topic).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	h.notifyTopics()
	c.Status(http.StatusNoContent)
}

// Preview Markdown → HTML 同源预览：与前台渲染走同一条 goldmark 管线。
func (h *AdminHandler) Preview(c *gin.Context) {
	var req struct {
		Markdown string `json:"markdown"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	rendered := Render(req.Markdown)
	c.JSON(http.StatusOK, gin.H{"html": rendered.HTML, "toc": rendered.TOC, "wordCount": CountWords(req.Markdown)})
}

// ========== 评论管理（审核台） ==========

// adminCommentPayload 管理侧评论条目：全状态可见，含作者与所属文章。
func (h *AdminHandler) adminCommentPayloads(comments []model.BlogComment) []gin.H {
	authorIDs := make([]uuid.UUID, 0, len(comments))
	postIDs := make([]uuid.UUID, 0, len(comments))
	for _, cm := range comments {
		authorIDs = append(authorIDs, cm.UserID)
		postIDs = append(postIDs, cm.PostID)
	}
	authors, _ := loadCommentAuthors(h.db, authorIDs)
	var posts []model.BlogPost
	_ = h.db.Where("id IN ?", postIDs).Find(&posts).Error
	postTitles := make(map[uuid.UUID]string, len(posts))
	postSlugs := make(map[uuid.UUID]string, len(posts))
	for _, p := range posts {
		postTitles[p.ID] = p.Title
		postSlugs[p.ID] = p.Slug
	}
	items := make([]gin.H, 0, len(comments))
	for _, cm := range comments {
		items = append(items, gin.H{
			"id": cm.ID.String(), "postId": cm.PostID.String(),
			"postTitle": postTitles[cm.PostID], "postSlug": postSlugs[cm.PostID],
			"author": authors[cm.UserID], "replyToId": cm.ReplyToID,
			"contentMd": cm.ContentMD, "contentHtml": Render(cm.ContentMD).HTML,
			"status": cm.Status, "pinned": cm.Pinned, "likeCount": cm.LikeCount,
			"createdAt": cm.CreatedAt,
		})
	}
	return items
}

// ListComments 管理侧评论列表：status/postId 筛选 + 分页。
func (h *AdminHandler) ListComments(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.BlogComment{})
	if status := c.Query("status"); status != "" {
		if !model.BlogCommentStatusValid(status) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
			return
		}
		query = query.Where("status = ?", status)
	}
	if postID := c.Query("postId"); postID != "" {
		pid, err := uuid.Parse(postID)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"postId": "postId 非法"}))
			return
		}
		query = query.Where("post_id = ?", pid)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var comments []model.BlogComment
	if err := query.Order("created_at DESC").Limit(params.Size).
		Offset((params.Page - 1) * params.Size).Find(&comments).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"comments": h.adminCommentPayloads(comments), "total": total, "page": params.Page, "size": params.Size})
}

// findComment 按 id 取评论。
func (h *AdminHandler) findComment(c *gin.Context) (model.BlogComment, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return model.BlogComment{}, false
	}
	var cm model.BlogComment
	if err := h.db.First(&cm, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return model.BlogComment{}, false
	}
	return cm, true
}

// HideComment 隐藏评论：前台显示占位，评论计数同步 -1。
func (h *AdminHandler) HideComment(c *gin.Context) {
	cm, ok := h.findComment(c)
	if !ok {
		return
	}
	if cm.Status == model.BlogCommentStatusVisible {
		if err := h.db.Model(&cm).Update("status", model.BlogCommentStatusHidden).Error; err != nil {
			errs.Abort(c, errs.ErrInternal)
			return
		}
		bumpPostCount(h.db, cm.PostID, "comment_count", -1)
	}
	var post model.BlogPost
	_ = h.db.First(&post, "id = ?", cm.PostID).Error
	h.notifyPost(post.Slug)
	c.JSON(http.StatusOK, gin.H{"comment": gin.H{"id": cm.ID.String(), "status": model.BlogCommentStatusHidden}})
}

// RestoreComment 恢复隐藏/隔离的评论。
func (h *AdminHandler) RestoreComment(c *gin.Context) {
	cm, ok := h.findComment(c)
	if !ok {
		return
	}
	if cm.Status != model.BlogCommentStatusVisible {
		if err := h.db.Model(&cm).Update("status", model.BlogCommentStatusVisible).Error; err != nil {
			errs.Abort(c, errs.ErrInternal)
			return
		}
		bumpPostCount(h.db, cm.PostID, "comment_count", 1)
	}
	var post model.BlogPost
	_ = h.db.First(&post, "id = ?", cm.PostID).Error
	h.notifyPost(post.Slug)
	c.JSON(http.StatusOK, gin.H{"comment": gin.H{"id": cm.ID.String(), "status": model.BlogCommentStatusVisible}})
}

// DeleteComment 管理员物理删除任意评论。
func (h *AdminHandler) DeleteComment(c *gin.Context) {
	cm, ok := h.findComment(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&cm).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if cm.Status == model.BlogCommentStatusVisible {
		bumpPostCount(h.db, cm.PostID, "comment_count", -1)
	}
	var post model.BlogPost
	_ = h.db.First(&post, "id = ?", cm.PostID).Error
	h.notifyPost(post.Slug)
	c.Status(http.StatusNoContent)
}

// PinComment 置顶/取消置顶（body: {"pinned": true}）。
func (h *AdminHandler) PinComment(c *gin.Context) {
	cm, ok := h.findComment(c)
	if !ok {
		return
	}
	var req struct {
		Pinned *bool `json:"pinned"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Pinned == nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if err := h.db.Model(&cm).Update("pinned", *req.Pinned).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var post model.BlogPost
	_ = h.db.First(&post, "id = ?", cm.PostID).Error
	h.notifyPost(post.Slug)
	c.JSON(http.StatusOK, gin.H{"comment": gin.H{"id": cm.ID.String(), "pinned": *req.Pinned}})
}
