package model

import (
	"time"

	"github.com/google/uuid"
)

// BlogTopic 博客栏目：数量级为个位数，全量缓存于前台；被文章引用时禁止删除。
type BlogTopic struct {
	ID          uint      `gorm:"primaryKey;autoIncrement;comment:栏目自增主键"`
	Slug        string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_blog_topics_slug;comment:栏目 slug，前台 URL /topics/{slug}"`
	Name        string    `gorm:"type:varchar(64);not null;comment:栏目名，如 AI 工作流"`
	EnName      string    `gorm:"type:varchar(64);not null;default:'';comment:英文名，前台大字展示，如 AI WORKFLOW"`
	Description string    `gorm:"type:varchar(255);not null;default:'';comment:栏目描述"`
	Sort        int       `gorm:"not null;default:0;comment:排序，越小越靠前"`
	CreatedAt   time.Time `gorm:"comment:创建时间"`
	UpdatedAt   time.Time `gorm:"comment:最近更新时间"`
}

func (BlogTopic) TableName() string { return "blog_topics" }

// BlogPost 博客文章：正文存 Markdown，元数据全部在列；公开读接口按 slug 访问。
// 状态 draft 草稿、published 已发布；published_at 仅在 draft→publish 时写入，下架不清空。
type BlogPost struct {
	ID           uuid.UUID  `gorm:"type:char(36);primaryKey;comment:文章主键"`
	Slug         string     `gorm:"type:varchar(128);not null;uniqueIndex:idx_blog_posts_slug;comment:文章 slug，前台 URL /posts/{slug}"`
	Title        string     `gorm:"type:varchar(255);not null;comment:标题"`
	Summary      string     `gorm:"type:varchar(500);not null;default:'';comment:摘要，列表与 SEO 用，空则服务端截取正文"`
	TopicID      uint       `gorm:"not null;index:idx_blog_posts_topic_status,priority:1;comment:所属栏目"`
	VolNo        int        `gorm:"not null;default:0;comment:期号 VOL.N，0 表示未编号"`
	CoverSeed    string     `gorm:"type:varchar(64);not null;default:'';comment:封面占位 seed，空 = 无封面纯文字卡"`
	IsAIGCCover  bool       `gorm:"not null;default:false;comment:封面是否 AI 生成（合规角标）"`
	IsPinned     bool       `gorm:"not null;default:false;index;comment:置顶，列表排序置顶优先于发布时间"`
	OriginURL    string     `gorm:"type:varchar(500);not null;default:'';comment:转载原文链接，空 = 原创"`
	Tags         string     `gorm:"type:varchar(255);not null;default:'';comment:标签，逗号分隔"`
	ContentMD    string     `gorm:"type:mediumtext;not null;comment:正文 Markdown，渲染在 Go 侧完成"`
	WordCount    int        `gorm:"not null;default:0;comment:字数，保存时服务端统计"`
	LikeCount    int        `gorm:"not null;default:0;comment:点赞数冗余计数，写路径事务内自增，夜间对账兜底"`
	CommentCount int        `gorm:"not null;default:0;comment:评论数冗余计数（visible 状态）"`
	BookmarkCount int       `gorm:"not null;default:0;comment:收藏数冗余计数，写路径事务内自增"`
	Status       string     `gorm:"type:varchar(16);not null;default:draft;index:idx_blog_posts_topic_status,priority:2;comment:状态 draft 草稿、published 已发布"`
	PublishedAt  *time.Time `gorm:"comment:首发时间"`
	CreatedAt    time.Time  `gorm:"comment:创建时间"`
	UpdatedAt    time.Time  `gorm:"comment:最近更新时间，前台展示「最后更新于」并与 SEO dateModified 同源"`
}

func (BlogPost) TableName() string { return "blog_posts" }

// 博客文章状态白名单与取值。
const (
	BlogPostStatusDraft     = "draft"
	BlogPostStatusPublished = "published"
)

func BlogPostStatusValid(s string) bool {
	return s == BlogPostStatusDraft || s == BlogPostStatusPublished
}

// BlogComment 文章评论：一层面 + reply_to_id 扁平回复；status visible 可见、
// hidden 管理员隐藏（前台显示占位）、quarantined 审核隔离待放行。
type BlogComment struct {
	ID         uuid.UUID `gorm:"type:char(36);primaryKey;comment:评论主键"`
	PostID     uuid.UUID `gorm:"type:char(36);not null;index:idx_blog_comments_post_created,priority:1;comment:所属文章"`
	UserID     uuid.UUID `gorm:"type:char(36);not null;index;comment:评论人平台账号，登录评论"`
	ReplyToID  *uuid.UUID `gorm:"type:char(36);index;comment:回复目标评论，扁平展示时 @ 对方昵称"`
	ContentMD  string    `gorm:"type:varchar(2000);not null;comment:评论正文，短 Markdown（粗体/行内代码/链接）"`
	Status     string    `gorm:"type:varchar(16);not null;default:visible;index:idx_blog_comments_post_created,priority:2;comment:状态 visible 可见、hidden 管理员隐藏、quarantined 审核隔离"`
	Pinned     bool      `gorm:"not null;default:false;index;comment:管理员置顶，置顶评论在列表最前"`
	LikeCount  int       `gorm:"not null;default:0;comment:点赞数冗余计数，写路径事务内自增"`
	CreatedAt  time.Time `gorm:"comment:发表时间"`
	UpdatedAt  time.Time `gorm:"comment:最近更新时间"`
}

func (BlogComment) TableName() string { return "blog_comments" }

// 博客评论状态白名单。
const (
	BlogCommentStatusVisible     = "visible"
	BlogCommentStatusHidden      = "hidden"
	BlogCommentStatusQuarantined = "quarantined"
)

func BlogCommentStatusValid(s string) bool {
	return s == BlogCommentStatusVisible || s == BlogCommentStatusHidden || s == BlogCommentStatusQuarantined
}

// BlogReaction 点赞：文章与评论通用，一人一物至多一条，主键去重保证幂等。
type BlogReaction struct {
	UserID     uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_blog_reactions_unique,priority:1;comment:点赞人平台账号"`
	TargetType string    `gorm:"type:varchar(16);not null;uniqueIndex:idx_blog_reactions_unique,priority:2;comment:目标类型 post 文章、comment 评论"`
	TargetID   uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_blog_reactions_unique,priority:3;index;comment:目标 ID"`
	CreatedAt  time.Time `gorm:"comment:点赞时间"`
}

func (BlogReaction) TableName() string { return "blog_reactions" }

// 博客点赞目标类型白名单。
const (
	BlogTargetPost    = "post"
	BlogTargetComment = "comment"
)

func BlogTargetTypeValid(s string) bool {
	return s == BlogTargetPost || s == BlogTargetComment
}

// BlogBookmark 收藏：一人一文章一条，主键去重保证幂等。
type BlogBookmark struct {
	UserID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_blog_bookmarks_unique,priority:1;comment:收藏人平台账号"`
	PostID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_blog_bookmarks_unique,priority:2;comment:收藏的文章"`
	CreatedAt time.Time `gorm:"comment:收藏时间"`
}

func (BlogBookmark) TableName() string { return "blog_bookmarks" }
