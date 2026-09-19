package blog

import (
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

// ========== 公共内容读（无鉴权） ==========

// topicBrief 栏目公共信息。
type topicBrief struct {
	ID          uint   `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	EnName      string `json:"enName"`
	Description string `json:"description"`
}

func topicBriefOf(t model.BlogTopic) topicBrief {
	return topicBrief{ID: t.ID, Slug: t.Slug, Name: t.Name, EnName: t.EnName, Description: t.Description}
}

// loadTopicMap 批量取栏目，返回 id → 栏目。
func loadTopicMap(db *gorm.DB) (map[uint]model.BlogTopic, error) {
	var topics []model.BlogTopic
	if err := db.Find(&topics).Error; err != nil {
		return nil, err
	}
	m := make(map[uint]model.BlogTopic, len(topics))
	for _, t := range topics {
		m[t.ID] = t
	}
	return m, nil
}

func (h *PublicHandler) loadTopicMap() (map[uint]model.BlogTopic, error) {
	return loadTopicMap(h.db)
}

func postListItem(p model.BlogPost, t model.BlogTopic) PostPayload {
	return PostPayload{
		ID: p.ID.String(), Slug: p.Slug, Title: p.Title, Summary: p.Summary,
		TopicID: p.TopicID, TopicName: t.Name, TopicEnName: t.EnName,
		VolNo: p.VolNo, CoverSeed: p.CoverSeed, IsAIGCCover: p.IsAIGCCover,
		IsPinned: p.IsPinned, OriginURL: p.OriginURL, Tags: splitTags(p.Tags),
		WordCount: p.WordCount, Status: p.Status,
		PublishedAt: p.PublishedAt, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// ListPosts 已发布文章列表：置顶优先于发布时间，分页。
func (h *PublicHandler) ListPosts(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.BlogPost{}).Where("status = ?", model.BlogPostStatusPublished)
	if topicSlug := c.Query("topic"); topicSlug != "" {
		var topic model.BlogTopic
		if err := h.db.First(&topic, "slug = ?", topicSlug).Error; err != nil {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		query = query.Where("topic_id = ?", topic.ID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var posts []model.BlogPost
	if err := query.Order("is_pinned DESC, published_at DESC").
		Limit(params.Size).Offset((params.Page - 1) * params.Size).Find(&posts).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	topics, _ := h.loadTopicMap()
	items := make([]PostPayload, 0, len(posts))
	for _, p := range posts {
		items = append(items, postListItem(p, topics[p.TopicID]))
	}
	c.JSON(http.StatusOK, gin.H{"posts": items, "total": total, "page": params.Page, "size": params.Size})
}

// GetPost 文章详情：渲染 HTML + 目录 + 上下篇 + 计数。草稿一律 404（不预告存在性）。
func (h *PublicHandler) GetPost(c *gin.Context) {
	var post model.BlogPost
	if err := h.db.First(&post, "slug = ? AND status = ?",
		c.Param("slug"), model.BlogPostStatusPublished).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	rendered := Render(post.ContentMD)

	var prev, next model.BlogPost
	if err := h.db.Where("status = ? AND published_at < ?", model.BlogPostStatusPublished, post.PublishedAt).
		Order("published_at DESC").First(&prev).Error; err != nil {
		prev = model.BlogPost{}
	}
	if err := h.db.Where("status = ? AND published_at > ?", model.BlogPostStatusPublished, post.PublishedAt).
		Order("published_at ASC").First(&next).Error; err != nil {
		next = model.BlogPost{}
	}

	var topic model.BlogTopic
	_ = h.db.First(&topic, "id = ?", post.TopicID).Error

	c.JSON(http.StatusOK, gin.H{
		"post":        postListItem(post, topic),
		"contentHtml": rendered.HTML,
		"toc":         rendered.TOC,
		"likeCount":   post.LikeCount, "bookmarkCount": post.BookmarkCount, "commentCount": post.CommentCount,
		"prev": postRef(prev), "next": postRef(next),
	})
}

type postBriefRef struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// postRef 上一篇/下一篇引用；零值文章（没有上/下一篇）返回空对象。
func postRef(p model.BlogPost) postBriefRef {
	if p.ID == uuid.Nil {
		return postBriefRef{}
	}
	return postBriefRef{Slug: p.Slug, Title: p.Title}
}

// ListTopics 栏目列表：按 sort 升序，带已发布文章计数。
func (h *PublicHandler) ListTopics(c *gin.Context) {
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
	if err := h.db.Model(&model.BlogPost{}).
		Select("topic_id, COUNT(*) as total").
		Where("status = ?", model.BlogPostStatusPublished).
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
		item := topicBriefOf(t)
		items = append(items, gin.H{
			"id": item.ID, "slug": item.Slug, "name": item.Name,
			"enName": item.EnName, "description": item.Description,
			"postCount": countMap[t.ID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"topics": items})
}

// ========== 互动（评论/点赞/收藏） ==========

// commentAuthor 评论人展示信息；isAdmin 用于前台渲染「管理员」徽章。
type commentAuthor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
	IsAdmin   bool   `json:"isAdmin"`
}

type commentPayload struct {
	ID          string        `json:"id"`
	PostID      string        `json:"postId"`
	ReplyToID   string        `json:"replyToId,omitempty"`
	ReplyToName string        `json:"replyToName,omitempty"`
	ContentHTML string        `json:"contentHtml"`
	Status      string        `json:"status"`
	Pinned      bool          `json:"pinned"`
	LikeCount   int           `json:"likeCount"`
	Liked       bool          `json:"liked"`
	Author      commentAuthor `json:"author"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// loadCommentAuthors 批量取评论人展示信息。
func loadCommentAuthors(db *gorm.DB, userIDs []uuid.UUID) (map[uuid.UUID]commentAuthor, error) {
	out := make(map[uuid.UUID]commentAuthor, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	var users []model.PlatformUser
	if err := db.Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		out[u.ID] = commentAuthor{
			ID: u.ID.String(), Name: name, AvatarURL: u.AvatarURL,
			IsAdmin: u.RoleKey != nil && *u.RoleKey == "admin",
		}
	}
	return out, nil
}

// loadVisibleComments 取某文章的可见评论（置顶优先，其余按时间正序），组装展示载荷。
// viewer 为当前登录人（可空）：用于标记自己是否已点赞。
func (h *PublicHandler) loadVisibleComments(postID uuid.UUID, viewer *uuid.UUID) ([]commentPayload, int, error) {
	var comments []model.BlogComment
	if err := h.db.Where("post_id = ? AND status = ?", postID, model.BlogCommentStatusVisible).
		Order("pinned DESC, created_at ASC").Find(&comments).Error; err != nil {
		return nil, 0, err
	}

	authors := make(map[uuid.UUID]commentAuthor)
	replyNames := make(map[uuid.UUID]string)
	ids := make([]uuid.UUID, 0, len(comments))
	for _, cm := range comments {
		ids = append(ids, cm.UserID)
		if cm.ReplyToID != nil {
			ids = append(ids, *cm.ReplyToID)
		}
	}
	if len(ids) > 0 {
		var err error
		if authors, err = loadCommentAuthors(h.db, ids); err != nil {
			return nil, 0, err
		}
	}
	if len(comments) > 0 {
		replyIDs := make([]uuid.UUID, 0, len(comments))
		for _, cm := range comments {
			if cm.ReplyToID != nil {
				replyIDs = append(replyIDs, *cm.ReplyToID)
			}
		}
		var replies []model.BlogComment
		if err := h.db.Where("id IN ?", replyIDs).Find(&replies).Error; err == nil {
			for _, r := range replies {
				if a, ok := authors[r.UserID]; ok {
					replyNames[r.ID] = a.Name
				}
			}
		}
	}

	liked := make(map[uuid.UUID]bool)
	if viewer != nil && len(comments) > 0 {
		commentIDs := make([]uuid.UUID, 0, len(comments))
		for _, cm := range comments {
			commentIDs = append(commentIDs, cm.ID)
		}
		var rows []model.BlogReaction
		if err := h.db.Where("user_id = ? AND target_type = ? AND target_id IN ?",
			*viewer, model.BlogTargetComment, commentIDs).Find(&rows).Error; err == nil {
			for _, r := range rows {
				liked[r.TargetID] = true
			}
		}
	}

	items := make([]commentPayload, 0, len(comments))
	for _, cm := range comments {
		author := authors[cm.UserID]
		item := commentPayload{
			ID: cm.ID.String(), PostID: cm.PostID.String(),
			ContentHTML: Render(cm.ContentMD).HTML,
			Status:      cm.Status, Pinned: cm.Pinned, LikeCount: cm.LikeCount,
			Liked: liked[cm.ID], Author: author, CreatedAt: cm.CreatedAt,
		}
		if cm.ReplyToID != nil {
			item.ReplyToID = cm.ReplyToID.String()
			item.ReplyToName = replyNames[*cm.ReplyToID]
		}
		items = append(items, item)
	}
	return items, len(items), nil
}

// ListComments 评论列表（公开）：文章必须已发布；登录态额外返回自己是否已点赞。
func (h *PublicHandler) ListComments(c *gin.Context) {
	var post model.BlogPost
	if err := h.db.First(&post, "slug = ? AND status = ?",
		c.Param("slug"), model.BlogPostStatusPublished).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	// 评论列表公开：登录态仅用于标记「自己是否已点赞」，匿名直接跳过。
	// 不走 httpx.CurrentUserID——它对无鉴权上下文会 abort 401。
	var viewer *uuid.UUID
	if raw := c.GetString("user_id"); raw != "" {
		if uid, err := uuid.Parse(raw); err == nil {
			viewer = &uid
		}
	}
	items, total, err := h.loadVisibleComments(post.ID, viewer)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"comments": items, "total": total})
}

// CreateComment 发表/回复评论：登录 + 限流 + 送审（命中进隔离，先发后审语义）。
func (h *PublicHandler) CreateComment(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "slug = ? AND status = ?",
		c.Param("slug"), model.BlogPostStatusPublished).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	var req struct {
		Content   string `json:"content"`
		ReplyToID string `json:"replyToId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	content := strings.TrimSpace(req.Content)
	fields := map[string]string{}
	if l := len([]rune(content)); l < 2 || l > 2000 {
		fields["content"] = "评论需在 2-2000 字之间"
	}
	var replyTo *uuid.UUID
	if req.ReplyToID != "" {
		rid, err := uuid.Parse(req.ReplyToID)
		if err != nil {
			fields["replyToId"] = "回复目标不合法"
		} else {
			var target model.BlogComment
			if err := h.db.First(&target, "id = ? AND post_id = ? AND status = ?",
				rid, post.ID, model.BlogCommentStatusVisible).Error; err != nil {
				fields["replyToId"] = "回复的评论不存在或不可见"
			} else {
				replyTo = &rid
			}
		}
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}

	// 送审：审核关闭时钩子未注入，直接可见；命中风险进隔离待放行。
	status := model.BlogCommentStatusVisible
	if h.textChecker != nil {
		allowed, err := h.textChecker(c.Request.Context(), uid, content)
		if err != nil {
			errs.Abort(c, errs.ErrInternal)
			return
		}
		if !allowed {
			status = model.BlogCommentStatusQuarantined
		}
	}

	cm := model.BlogComment{
		ID: uuid.New(), PostID: post.ID, UserID: uid,
		ContentMD: content, Status: status, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if replyTo != nil {
		cm.ReplyToID = replyTo
	}
	if err := h.db.Create(&cm).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if status == model.BlogCommentStatusVisible {
		bumpPostCount(h.db, post.ID, "comment_count", 1)
	}
	c.JSON(http.StatusCreated, gin.H{"comment": gin.H{
		"id": cm.ID.String(), "status": status,
	}})
}

// DeleteComment 本人删除自己的评论（管理员删除走管理面端点）。
func (h *PublicHandler) DeleteComment(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var cm model.BlogComment
	if err := h.db.First(&cm, "id = ? AND user_id = ?", c.Param("id"), uid).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err := h.db.Delete(&cm).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if cm.Status == model.BlogCommentStatusVisible {
		bumpPostCount(h.db, cm.PostID, "comment_count", -1)
	}
	c.Status(http.StatusNoContent)
}

// bumpPostCount 冗余计数增减，column 走白名单防注入。
func bumpPostCount(db *gorm.DB, postID uuid.UUID, column string, delta int) {
	allowed := map[string]bool{"like_count": true, "comment_count": true, "bookmark_count": true}
	if !allowed[column] {
		return
	}
	if err := db.Model(&model.BlogPost{}).Where("id = ?", postID).
		Update(column, gorm.Expr(column+" + ?", delta)).Error; err != nil {
		slog.Error("博客冗余计数更新失败", "column", column, "err", err)
	}
}

// React 点赞：主键去重幂等；重复点赞返回当前状态而非报错。
func (h *PublicHandler) React(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	target, ok := bindReactionTarget(c)
	if !ok {
		return
	}
	if err := h.ensureReactionTarget(c, target); err != nil {
		return
	}
	reaction := model.BlogReaction{
		UserID: uid, TargetType: target.targetType, TargetID: target.targetID,
		CreatedAt: time.Now(),
	}
	res := h.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&reaction)
	if res.Error != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected > 0 {
		bumpReactionCount(h.db, target, 1)
	}
	count := reactionCount(h.db, target)
	c.JSON(http.StatusOK, gin.H{"liked": true, "count": count})
}

// Unreact 取消点赞：未点赞时幂等返回。
func (h *PublicHandler) Unreact(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	target, ok := bindReactionTarget(c)
	if !ok {
		return
	}
	res := h.db.Where("user_id = ? AND target_type = ? AND target_id = ?",
		uid, target.targetType, target.targetID).Delete(&model.BlogReaction{})
	if res.Error != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected > 0 {
		bumpReactionCount(h.db, target, -1)
	}
	c.JSON(http.StatusOK, gin.H{"liked": false, "count": reactionCount(h.db, target)})
}

type reactionTarget struct {
	targetType string
	targetID   uuid.UUID
	postID     uuid.UUID // comment 目标时冗余记录所属文章，便于计数
}

func bindReactionTarget(c *gin.Context) (reactionTarget, bool) {
	var req struct {
		TargetType string `json:"targetType"`
		TargetID   string `json:"targetId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !model.BlogTargetTypeValid(req.TargetType) {
		errs.Abort(c, errs.ErrValidation)
		return reactionTarget{}, false
	}
	tid, err := uuid.Parse(req.TargetID)
	if err != nil {
		errs.Abort(c, errs.ErrValidation)
		return reactionTarget{}, false
	}
	return reactionTarget{targetType: req.TargetType, targetID: tid}, true
}

// ensureReactionTarget 校验点赞目标存在且可见。
func (h *PublicHandler) ensureReactionTarget(c *gin.Context, t reactionTarget) error {
	switch t.targetType {
	case model.BlogTargetPost:
		var post model.BlogPost
		if err := h.db.First(&post, "id = ? AND status = ?",
			t.targetID, model.BlogPostStatusPublished).Error; err != nil {
			errs.Abort(c, errs.ErrNotFound)
			return err
		}
	case model.BlogTargetComment:
		var cm model.BlogComment
		if err := h.db.First(&cm, "id = ? AND status = ?",
			t.targetID, model.BlogCommentStatusVisible).Error; err != nil {
			errs.Abort(c, errs.ErrNotFound)
			return err
		}
		t.postID = cm.PostID
	}
	return nil
}

// bumpReactionCount 点赞计数：文章走 like_count，评论走 like_count（评论表自身列）。
func bumpReactionCount(db *gorm.DB, t reactionTarget, delta int) {
	switch t.targetType {
	case model.BlogTargetPost:
		bumpPostCount(db, t.targetID, "like_count", delta)
	case model.BlogTargetComment:
		if err := db.Model(&model.BlogComment{}).Where("id = ?", t.targetID).
			Update("like_count", gorm.Expr("like_count + ?", delta)).Error; err != nil {
			slog.Error("评论点赞计数更新失败", "err", err)
		}
	}
}

// reactionCount 读取当前计数。
func reactionCount(db *gorm.DB, t reactionTarget) int {
	switch t.targetType {
	case model.BlogTargetPost:
		var post model.BlogPost
		if err := db.First(&post, "id = ?", t.targetID).Error; err == nil {
			return post.LikeCount
		}
	case model.BlogTargetComment:
		var cm model.BlogComment
		if err := db.First(&cm, "id = ?", t.targetID).Error; err == nil {
			return cm.LikeCount
		}
	}
	return 0
}

// Bookmark 收藏已发布文章，幂等。
func (h *PublicHandler) Bookmark(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	post, ok := h.publishedPostBySlug(c)
	if !ok {
		return
	}
	bm := model.BlogBookmark{UserID: uid, PostID: post.ID, CreatedAt: time.Now()}
	res := h.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&bm)
	if res.Error != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected > 0 {
		bumpPostCount(h.db, post.ID, "bookmark_count", 1)
	}
	c.JSON(http.StatusOK, gin.H{"bookmarked": true, "count": postBookmarkCount(h.db, post.ID)})
}

// Unbookmark 取消收藏，幂等。
func (h *PublicHandler) Unbookmark(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var post model.BlogPost
	if err := h.db.First(&post, "slug = ?", c.Param("slug")).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	res := h.db.Where("user_id = ? AND post_id = ?", uid, post.ID).Delete(&model.BlogBookmark{})
	if res.Error != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if res.RowsAffected > 0 {
		bumpPostCount(h.db, post.ID, "bookmark_count", -1)
	}
	c.JSON(http.StatusOK, gin.H{"bookmarked": false, "count": postBookmarkCount(h.db, post.ID)})
}

// ListBookmarks 我的收藏列表（仅已发布文章）。
func (h *PublicHandler) ListBookmarks(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.BlogBookmark{}).
		Joins("JOIN blog_posts ON blog_posts.id = blog_bookmarks.post_id").
		Where("blog_bookmarks.user_id = ? AND blog_posts.status = ?", uid, model.BlogPostStatusPublished)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var marks []model.BlogBookmark
	if err := query.Order("blog_bookmarks.created_at DESC").
		Limit(params.Size).Offset((params.Page - 1) * params.Size).Find(&marks).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	topics, _ := h.loadTopicMap()
	items := make([]PostPayload, 0, len(marks))
	for _, bm := range marks {
		var post model.BlogPost
		if err := h.db.First(&post, "id = ?", bm.PostID).Error; err != nil {
			continue
		}
		items = append(items, postListItem(post, topics[post.TopicID]))
	}
	c.JSON(http.StatusOK, gin.H{"posts": items, "total": total, "page": params.Page, "size": params.Size})
}

func (h *PublicHandler) publishedPostBySlug(c *gin.Context) (model.BlogPost, bool) {
	var post model.BlogPost
	if err := h.db.First(&post, "slug = ? AND status = ?",
		c.Param("slug"), model.BlogPostStatusPublished).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return post, false
	}
	return post, true
}

func postBookmarkCount(db *gorm.DB, postID uuid.UUID) int {
	var post model.BlogPost
	if err := db.First(&post, "id = ?", postID).Error; err == nil {
		return post.BookmarkCount
	}
	return 0
}
