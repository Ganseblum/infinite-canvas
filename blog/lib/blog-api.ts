// 博客内容 API 客户端：Next 服务端经内网拉取 Go 域 /api/v1/blog/*。
// 缓存策略：所有内容页带缓存标签，Go 侧发布/下架/删除后回调 /api/revalidate
// 按需失效；REVALIDATE_FALLBACK 是回调失败时的兜底过期窗口（秒）。
const API_BASE = process.env.BLOG_API_BASE ?? "http://127.0.0.1:8080/api/v1/blog";

export const REVALIDATE_FALLBACK = 300;

/** 文章概要模型（列表与详情共用），字段与 Go blog 域的 JSON 输出一一对应。 */
export type BlogPost = {
  id: string;
  slug: string;
  title: string;
  summary: string;
  topicId: number;
  topicName: string;
  topicEnName: string;
  volNo: number; // 期号，站内排序与 VOL.xxx 展示用
  coverSeed: string; // picsum 封面种子，空 = 无封面的纯文字卡
  isAigcCover: boolean; // AI 参与生成的封面，前台必须带角标
  isPinned: boolean; // 置顶头条
  originUrl: string; // 转载原文链接，空 = 原创
  tags: string[];
  wordCount: number; // 字数，阅读时长按 400 字/分钟估算
  status: string; // draft / published，公共接口只出 published
  publishedAt: string | null; // 首发时间，草稿为 null
  createdAt: string;
  updatedAt: string;
};

/** 栏目模型；postCount 为该栏目下已发布文章数（供栏目 chip 徽标）。 */
export type BlogTopic = {
  id: number;
  slug: string;
  name: string;
  enName: string; // 英文刊名，栏目页大标题优先用
  description: string;
  postCount: number;
};

/** 目录项：锚点 id 由 Go 侧 goldmark 标题 id 生成，level 只收 2/3 级标题。 */
export type TocItem = { id: string; text: string; level: number };

/** 文章详情聚合：概要 + 正文 HTML + 目录 + 互动计数 + 前后篇导航。 */
export type PostDetail = {
  post: BlogPost;
  contentHtml: string;
  toc: TocItem[];
  likeCount: number;
  bookmarkCount: number;
  commentCount: number;
  prev: { slug: string; title: string }; // 按发布时间相邻；无值时 slug 为空，前端渲染禁用态
  next: { slug: string; title: string };
};

// RequestInit 扩展：tags 为 Next 缓存标签，供 /api/revalidate 按需失效。
type FetchInit = RequestInit & { tags?: string[] };

// fetchBlog 内容 API 统一入口：带缓存标签与兜底过期；非 2xx 抛错由页面转 404/500。
export async function fetchBlog<T>(path: string, tags: string[] = [], init: FetchInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    next: { tags, revalidate: REVALIDATE_FALLBACK, ...init.next },
  });
  if (!res.ok) {
    throw new Error(`blog api ${res.status}: ${path}`);
  }
  return (await res.json()) as T;
}

/** 文章列表：按页与栏目筛选，缓存标签 posts（Go 侧按发布时间倒序）。 */
export function fetchPostList(params: { page?: number; topic?: string } = {}) {
  const qs = new URLSearchParams();
  if (params.page && params.page > 1) qs.set("page", String(params.page));
  if (params.topic) qs.set("topic", params.topic);
  const q = qs.toString();
  return fetchBlog<{ posts: BlogPost[]; total: number; page: number; size: number }>(
    `/posts${q ? `?${q}` : ""}`,
    ["posts"],
  );
}

/** 文章详情：额外带 post:<slug> 标签，下架/更新时按篇精确失效。 */
export function fetchPostDetail(slug: string, init: FetchInit = {}) {
  return fetchBlog<PostDetail>(`/posts/${slug}`, ["posts", `post:${slug}`], init);
}

/** 栏目列表（含每栏目文章计数），缓存标签 topics。 */
export function fetchTopics() {
  return fetchBlog<{ topics: BlogTopic[] }>("/topics", ["topics"]);
}

// siteUrl 站点绝对地址（canonical / sitemap / RSS / OG 用）。
export function siteUrl(): string {
  return (process.env.BLOG_SITE_URL ?? "http://localhost:3101").replace(/\/$/, "");
}
