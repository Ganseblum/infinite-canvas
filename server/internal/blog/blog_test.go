package blog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/admin"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/blog"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/testutil"
)

const sampleContent = `# 引言

## 一、为什么是画布

正文段落，包含 **加粗** 与 [链接](https://example.com)。

> [!TIP]
> 这是提示框内容。

## 二、取舍

` + "```go\nconst src = scale < 0.4 ? thumb : original;\n```" + `

脚注引用[^1]。

[^1]: 脚注内容。
`

// blogEnv 组装与生产同源的测试路由：内容读/评论列表公开、互动需登录、
// 管理面走 admin.RegisterRoutes（权限点由 RequirePermission 在线判定）。
type blogEnv struct {
	r       *gin.Engine
	g       *gorm.DB
	cfg     *config.Config
	publicH *blog.PublicHandler
	admin   model.PlatformUser
	adminTk string
	user    model.PlatformUser
	userTk  string
}

// newBlogEnv 组装基础环境；checker 非 nil 时作为评论送审钩子注入。
func newBlogEnv(t *testing.T, checker blog.TextChecker) *blogEnv {
	t.Helper()
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	publicH := blog.NewPublicHandler(g)
	if checker != nil {
		publicH.SetTextChecker(checker)
	}
	blogAdminH := blog.NewAdminHandler(g, nil)

	adminUser := testutil.CreateUser(t, g, "blog-admin@example.com", "blogadmin", "password123", true)
	adminKey := authz.SystemRoleKey
	testutil.SetUserRole(t, g, &adminUser, &adminKey)
	normalUser := testutil.CreateUser(t, g, "blog-user@example.com", "bloguser", "password123", true)

	r := testutil.NewRouter(t, func(r *gin.Engine) {
		secret := []byte(cfg.JWTSecret)
		idn := identity.NewService(g)
		blog.MountContentRoutes(r.Group("/api/v1/blog"), publicH)
		blog.MountInteractionRoutes(r.Group("/api/v1/blog", middleware.Auth(secret)), publicH)
		adminGroup := r.Group("/api/admin", middleware.Auth(secret), middleware.LoadAdminAccess(idn, g))
		adminHandler := admin.NewAdminHandlerWithUpstream(g, cfg, nil, nil)
		adminHandler.SetBlog(blogAdminH)
		admin.RegisterRoutes(adminGroup, adminHandler)
	})
	return &blogEnv{
		r: r, g: g, cfg: cfg, publicH: publicH,
		admin: adminUser, adminTk: testutil.AccessToken(t, cfg, &adminUser),
		user: normalUser, userTk: testutil.AccessToken(t, cfg, &normalUser),
	}
}

func (e *blogEnv) adminJSON(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	return testutil.DoAuthJSON(e.r, method, path, e.adminTk, body).Result()
}

func (e *blogEnv) userJSON(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	return testutil.DoAuthJSON(e.r, method, path, e.userTk, body).Result()
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return out
}

// createTopicViaAdmin 走管理接口建栏目。
func (e *blogEnv) createTopicViaAdmin(t *testing.T, slug string) uint {
	t.Helper()
	resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/topics", map[string]any{
		"slug": slug, "name": "栏目-" + slug, "enName": "TOPIC", "sort": 1,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建栏目失败: %d", resp.StatusCode)
	}
	out := decode[struct {
		Topic struct {
			ID uint `json:"id"`
		} `json:"topic"`
	}](t, resp)
	return out.Topic.ID
}

// createPostViaAdmin 走管理接口建草稿文章。
func (e *blogEnv) createPostViaAdmin(t *testing.T, topicID uint, slug, content string) (string, string) {
	t.Helper()
	resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts", map[string]any{
		"slug": slug, "title": "标题-" + slug, "topicId": topicID,
		"contentMd": content, "tags": "测试, 博客", "volNo": 1,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建文章失败: %d", resp.StatusCode)
	}
	out := decode[struct {
		Post struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"post"`
	}](t, resp)
	return out.Post.ID, out.Post.Slug
}

type postListOut struct {
	Posts []map[string]any `json:"posts"`
	Total int64            `json:"total"`
}

type postDetailOut struct {
	ContentHTML string         `json:"contentHtml"`
	TOC         []blog.TocItem `json:"toc"`
	Prev        struct {
		Slug string `json:"slug"`
	} `json:"prev"`
	Next struct {
		Slug string `json:"slug"`
	} `json:"next"`
	CommentCount int `json:"commentCount"`
}

func TestPostLifecycleAndPublicRead(t *testing.T) {
	e := newBlogEnv(t, nil)
	topicID := e.createTopicViaAdmin(t, "ai-workflow")
	postID, slug := e.createPostViaAdmin(t, topicID, "canvas-in-browser", sampleContent)

	// 草稿对公共读不可见
	w := testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("公共列表应 200: %d", w.Code)
	}
	list := decode[postListOut](t, w.Result())
	if list.Total != 0 {
		t.Fatalf("草稿不应出现在公共列表: %d", list.Total)
	}
	if w := testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("草稿详情应 404: %d", w.Code)
	}

	// 发布 → 列表可见、详情含渲染产物
	if resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts/"+postID+"/publish", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("发布失败: %d", resp.StatusCode)
	}
	w = testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts", "", nil)
	list = decode[postListOut](t, w.Result())
	if list.Total != 1 || list.Posts[0]["topicName"] != "栏目-ai-workflow" {
		t.Fatalf("发布后列表异常: %+v", list)
	}
	w = testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug, "", nil)
	detail := decode[postDetailOut](t, w.Result())
	for _, want := range []string{`class="callout"`, "scale", "footnotes", `<pre tabindex="0" style="`} {
		if !strings.Contains(detail.ContentHTML, want) {
			t.Fatalf("渲染 HTML 缺少 %q", want)
		}
	}
	if len(detail.TOC) != 2 || detail.TOC[0].ID == "" {
		t.Fatalf("目录应两项且带锚点: %+v", detail.TOC)
	}

	// 第二篇发布更晚：第一篇的 next 指向第二篇
	postID2, slug2 := e.createPostViaAdmin(t, topicID, "second-post", "## 第二篇\n\n内容。")
	if resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts/"+postID2+"/publish", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("第二篇发布失败: %d", resp.StatusCode)
	}
	w = testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug, "", nil)
	detail = decode[postDetailOut](t, w.Result())
	if detail.Prev.Slug != "" || detail.Next.Slug != slug2 {
		t.Fatalf("上下篇指向错误: prev=%q next=%q", detail.Prev.Slug, detail.Next.Slug)
	}

	// 下架 → 详情 404
	if resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts/"+postID+"/unpublish", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("下架失败: %d", resp.StatusCode)
	}
	if w := testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("下架后详情应 404: %d", w.Code)
	}
}

func TestSlugConflict409(t *testing.T) {
	e := newBlogEnv(t, nil)
	topicID := e.createTopicViaAdmin(t, "t1")
	e.createPostViaAdmin(t, topicID, "same-slug", "内容。")
	resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts", map[string]any{
		"slug": "same-slug", "title": "重复", "topicId": topicID, "contentMd": "内容。",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("slug 冲突应 409: %d", resp.StatusCode)
	}
}

func TestTopicDeleteInUse409(t *testing.T) {
	e := newBlogEnv(t, nil)
	topicID := e.createTopicViaAdmin(t, "in-use")
	e.createPostViaAdmin(t, topicID, "pinned-post", "内容。")
	if resp := e.adminJSON(t, http.MethodDelete, fmt.Sprintf("/api/admin/blog/topics/%d", topicID), nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("被引用栏目删除应 409: %d", resp.StatusCode)
	}
}

type commentsOut struct {
	Comments []struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		ReplyToName string `json:"replyToName"`
		Author      struct {
			Name    string `json:"name"`
			IsAdmin bool   `json:"isAdmin"`
		} `json:"author"`
	} `json:"comments"`
	Total int `json:"total"`
}

func TestInteractionsLifecycle(t *testing.T) {
	e := newBlogEnv(t, nil)
	topicID := e.createTopicViaAdmin(t, "interact")
	postID, slug := e.createPostViaAdmin(t, topicID, "interact-post", sampleContent)
	e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts/"+postID+"/publish", nil)

	// 点赞幂等：两次点赞计数只加一次
	for i := 0; i < 2; i++ {
		resp := e.userJSON(t, http.MethodPost, "/api/v1/blog/reactions", map[string]any{
			"targetType": "post", "targetId": postID,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("点赞失败: %d", resp.StatusCode)
		}
		out := decode[struct {
			Count int `json:"count"`
		}](t, resp)
		if out.Count != 1 {
			t.Fatalf("点赞幂等失败, count=%d", out.Count)
		}
	}

	// 发评（可见）→ 回复 → 列表两条、回复带昵称
	resp := e.userJSON(t, http.MethodPost, "/api/v1/blog/posts/"+slug+"/comments", map[string]any{
		"content": "这是一条评论，超过两个字。",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("发评失败: %d", resp.StatusCode)
	}
	created := decode[struct {
		Comment struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"comment"`
	}](t, resp)
	if created.Comment.Status != model.BlogCommentStatusVisible {
		t.Fatalf("默认评论应可见: %s", created.Comment.Status)
	}
	if resp := e.userJSON(t, http.MethodPost, "/api/v1/blog/posts/"+slug+"/comments", map[string]any{
		"content": "这是回复内容，也超过两个字。", "replyToId": created.Comment.ID,
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("回复失败: %d", resp.StatusCode)
	}
	w := testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug+"/comments", "", nil)
	comments := decode[commentsOut](t, w.Result())
	if comments.Total != 2 || comments.Comments[1].ReplyToName == "" {
		t.Fatalf("评论列表异常: code=%d body=%s", w.Code, w.Body.String())
	}

	// 收藏幂等 + 我的收藏出现
	for i := 0; i < 2; i++ {
		if resp := e.userJSON(t, http.MethodPut, "/api/v1/blog/posts/"+slug+"/bookmark", nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("收藏失败: %d", resp.StatusCode)
		}
	}
	if w := testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/bookmarks", e.userTk, nil); w.Code != http.StatusOK {
		t.Fatalf("我的收藏应 200: %d", w.Code)
	}

	// 本人删评 → 计数回落；删他人评论 404
	if resp := e.userJSON(t, http.MethodDelete, "/api/v1/blog/comments/"+created.Comment.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("本人删评失败: %d", resp.StatusCode)
	}
	resp = e.userJSON(t, http.MethodPost, "/api/v1/blog/posts/"+slug+"/comments", map[string]any{"content": "再补一条评论内容。"})
	reborn := decode[struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}](t, resp)
	// 管理员不是评论作者：走用户面删除应 404（管理面删除走 admin 端点）
	if resp := e.adminJSON(t, http.MethodDelete, "/api/v1/blog/comments/"+reborn.Comment.ID, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删除他人评论应 404: %d", resp.StatusCode)
	}
}

// TestCommentQuarantinedByChecker 钩子命中即拒：评论进隔离、公共列表不可见、
// 管理员恢复后可见且计数回升。
func TestCommentQuarantinedByChecker(t *testing.T) {
	e := newBlogEnv(t, func(ctx context.Context, userID uuid.UUID, text string) (bool, error) {
		return false, nil
	})
	topicID := e.createTopicViaAdmin(t, "quarantine")
	postID, slug := e.createPostViaAdmin(t, topicID, "quarantine-post", sampleContent)
	e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts/"+postID+"/publish", nil)

	resp := e.userJSON(t, http.MethodPost, "/api/v1/blog/posts/"+slug+"/comments", map[string]any{
		"content": "这条评论会被送审命中。",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("隔离评论仍应创建成功: %d", resp.StatusCode)
	}
	created := decode[struct {
		Comment struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"comment"`
	}](t, resp)
	if created.Comment.Status != model.BlogCommentStatusQuarantined {
		t.Fatalf("命中钩子应进隔离: %s", created.Comment.Status)
	}
	afterCreate := decode[commentsOut](t,
		testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug+"/comments", "", nil).Result())
	if afterCreate.Total != 0 {
		t.Fatalf("隔离评论不应出现在公共列表: %d", afterCreate.Total)
	}
	// 管理员恢复放行
	if resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/comments/"+created.Comment.ID+"/restore", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("恢复失败: %d", resp.StatusCode)
	}
	afterRestore := decode[commentsOut](t,
		testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug+"/comments", "", nil).Result())
	if afterRestore.Total != 1 {
		t.Fatalf("恢复后应可见: %d", afterRestore.Total)
	}
}

func TestCommentAdminModerationFlow(t *testing.T) {
	e := newBlogEnv(t, nil)
	topicID := e.createTopicViaAdmin(t, "mod")
	postID, slug := e.createPostViaAdmin(t, topicID, "mod-post", sampleContent)
	e.adminJSON(t, http.MethodPost, "/api/admin/blog/posts/"+postID+"/publish", nil)

	resp := e.userJSON(t, http.MethodPost, "/api/v1/blog/posts/"+slug+"/comments", map[string]any{
		"content": "待管理的一条评论内容。",
	})
	created := decode[struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}](t, resp)
	commentID := created.Comment.ID

	if resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/comments/"+commentID+"/hide", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("隐藏失败: %d", resp.StatusCode)
	}
	afterHide := decode[commentsOut](t,
		testutil.DoAuthJSON(e.r, http.MethodGet, "/api/v1/blog/posts/"+slug+"/comments", "", nil).Result())
	if afterHide.Total != 0 {
		t.Fatalf("隐藏后公共列表应不可见: %d", afterHide.Total)
	}
	if resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/comments/"+commentID+"/restore", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("恢复失败: %d", resp.StatusCode)
	}
	if resp := e.adminJSON(t, http.MethodPut, "/api/admin/blog/comments/"+commentID+"/pinned", map[string]any{"pinned": true}); resp.StatusCode != http.StatusOK {
		t.Fatalf("置顶失败: %d", resp.StatusCode)
	}

	// 非管理员访问管理面 403
	if resp := e.userJSON(t, http.MethodGet, "/api/admin/blog/posts", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无权限访问管理面应 403: %d", resp.StatusCode)
	}
}

func TestPreviewEndpoint(t *testing.T) {
	e := newBlogEnv(t, nil)
	resp := e.adminJSON(t, http.MethodPost, "/api/admin/blog/preview", map[string]any{
		"markdown": "## 标题\n\n> [!WARNING]\n> 注意内容。\n",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("预览失败: %d", resp.StatusCode)
	}
	out := decode[struct {
		HTML string `json:"html"`
	}](t, resp)
	if !strings.Contains(out.HTML, "callout warn") || !strings.Contains(out.HTML, "WARNING") {
		t.Fatalf("预览应渲染 warn callout: %s", out.HTML)
	}
}
