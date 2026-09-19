// Package blog 博客产品域：内容（文章/栏目）、Markdown 渲染管线、公共读 API、
// 管理面 CRUD（路由在 admin 包注册）与互动（评论/点赞/收藏）。
// 数据模型在 internal/model，管理路由经 admin.RegisterRoutes 挂权限点，
// 公共读与互动路由由本包 Mount 函数维护，main 与测试夹具共用同一张表。
package blog

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
)

// PublicHandler 承载公共内容读与互动接口：读接口无鉴权（前台 Next 服务端
// 内网拉取 + 游客浏览器），互动接口由调用方挂 Auth 中间件。
type PublicHandler struct {
	db             *gorm.DB
	commentLimiter *middleware.Limiter
	textChecker    TextChecker
}

// TextChecker 评论正文送审钩子：返回 false 表示命中风险（进隔离待放行）。
// main 侧接 ModerationService.Check，审核关闭时不注入（直接可见）。
type TextChecker func(ctx context.Context, userID uuid.UUID, text string) (bool, error)

func NewPublicHandler(db *gorm.DB) *PublicHandler {
	return &PublicHandler{db: db}
}

// SetCommentLimiter 设置发评限流器（建议 2 次/分钟 ≈ 30 秒一条，待确认口径）。
func (h *PublicHandler) SetCommentLimiter(l *middleware.Limiter) { h.commentLimiter = l }

// SetTextChecker 设置评论送审钩子。
func (h *PublicHandler) SetTextChecker(f TextChecker) { h.textChecker = f }

// MountContentRoutes 注册公共内容读三条路由，无鉴权。
func MountContentRoutes(g *gin.RouterGroup, h *PublicHandler) {
	g.GET("/posts", h.ListPosts)
	g.GET("/posts/:slug", h.GetPost)
	g.GET("/posts/:slug/comments", h.ListComments)
	g.GET("/topics", h.ListTopics)
}

// MountInteractionRoutes 注册互动路由，鉴权中间件由调用方挂在分组上
// （评论列表公开：同一方法路径注册在无鉴权分组亦可）。
func MountInteractionRoutes(g *gin.RouterGroup, h *PublicHandler) {
	g.POST("/posts/:slug/comments", h.commentRateLimit(), h.CreateComment)
	g.DELETE("/comments/:id", h.DeleteComment)
	g.POST("/reactions", h.React)
	g.DELETE("/reactions", h.Unreact)
	g.PUT("/posts/:slug/bookmark", h.Bookmark)
	g.DELETE("/posts/:slug/bookmark", h.Unbookmark)
	g.GET("/bookmarks", h.ListBookmarks)
}

// commentRateLimit 发评限流；未注入限流器（测试）时直通。
func (h *PublicHandler) commentRateLimit() gin.HandlerFunc {
	if h.commentLimiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return middleware.RateLimit(h.commentLimiter, func(c *gin.Context) string {
		return "comment:" + c.GetString("user_id")
	})
}

// AdminHandler 博客管理面：文章 CRUD / 发布下架 / 栏目 CRUD / 预览 / 评论管理。
// 路由在 admin 包 RegisterRoutes 里挂 blog.read / blog.write 权限点注册。
// rev 为按需再验证通知器，可 nil（单测）：内容变更后回调前台失效缓存。
type AdminHandler struct {
	db  *gorm.DB
	rev *Revalidator
}

func NewAdminHandler(db *gorm.DB, rev *Revalidator) *AdminHandler {
	return &AdminHandler{db: db, rev: rev}
}

// notifyPost 内容变更后失效前台的文章页与列表缓存。
func (h *AdminHandler) notifyPost(slug string) {
	if h.rev != nil {
		h.rev.NotifyList()
		h.rev.NotifyPost(slug)
	}
}

// notifyTopics 栏目变更后失效前台栏目缓存。
func (h *AdminHandler) notifyTopics() {
	if h.rev != nil {
		h.rev.NotifyTopics()
	}
}

// PostPayload 文章列表项与管理详情的公共字段。
type PostPayload struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	TopicID     uint       `json:"topicId"`
	TopicName   string     `json:"topicName"`
	TopicEnName string     `json:"topicEnName"`
	VolNo       int        `json:"volNo"`
	CoverSeed   string     `json:"coverSeed"`
	IsAIGCCover bool       `json:"isAigcCover"`
	IsPinned    bool       `json:"isPinned"`
	OriginURL   string     `json:"originUrl"`
	Tags        []string   `json:"tags"`
	WordCount   int        `json:"wordCount"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"publishedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func splitTags(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// slugRe 文章与栏目 slug 规则：全小写字母数字与单连字符分段。
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validSlug(s string) bool { return len(s) >= 1 && len(s) <= 128 && slugRe.MatchString(s) }

// publishPost 把草稿置为已发布：published_at 只在首发时写入，重复发布不覆盖首发时间。
func publishPost(db *gorm.DB, post *model.BlogPost) error {
	if post.Status == model.BlogPostStatusPublished {
		return nil
	}
	nowT := time.Now()
	post.Status = model.BlogPostStatusPublished
	if post.PublishedAt == nil {
		post.PublishedAt = &nowT
	}
	return db.Save(post).Error
}

// unpublishPost 下架：前台详情立即 404，published_at 保留（重新上架沿用首发时间）。
func unpublishPost(db *gorm.DB, post *model.BlogPost) error {
	post.Status = model.BlogPostStatusDraft
	return db.Save(post).Error
}

// writeError 把写库错误统一转为 409（唯一冲突）或 500。
func writeError(c *gin.Context, err error, what string) {
	if errs.IsDuplicateKey(err) {
		errs.Abort(c, errs.ErrSlugTaken)
		return
	}
	slog.Error(what, "err", err)
	errs.Abort(c, errs.ErrInternal)
}
