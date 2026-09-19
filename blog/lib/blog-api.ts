// 博客内容 API 客户端：Next 服务端经内网拉取 Go 域 /api/v1/blog/*。
// 缓存策略：所有内容页带缓存标签，Go 侧发布/下架/删除后回调 /api/revalidate
// 按需失效；REVALIDATE_FALLBACK 是回调失败时的兜底过期窗口（秒）。
const API_BASE = process.env.BLOG_API_BASE ?? "http://127.0.0.1:8080/api/v1/blog";

export const REVALIDATE_FALLBACK = 300;

export type BlogPost = {
  id: string;
  slug: string;
  title: string;
  summary: string;
  topicId: number;
  topicName: string;
  topicEnName: string;
  volNo: number;
  coverSeed: string;
  isAigcCover: boolean;
  isPinned: boolean;
  originUrl: string;
  tags: string[];
  wordCount: number;
  status: string;
  publishedAt: string | null;
  createdAt: string;
  updatedAt: string;
};

export type BlogTopic = {
  id: number;
  slug: string;
  name: string;
  enName: string;
  description: string;
  postCount: number;
};

export type TocItem = { id: string; text: string; level: number };

export type PostDetail = {
  post: BlogPost;
  contentHtml: string;
  toc: TocItem[];
  likeCount: number;
  bookmarkCount: number;
  commentCount: number;
  prev: { slug: string; title: string };
  next: { slug: string; title: string };
};

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

export function fetchPostDetail(slug: string, init: FetchInit = {}) {
  return fetchBlog<PostDetail>(`/posts/${slug}`, ["posts", `post:${slug}`], init);
}

export function fetchTopics() {
  return fetchBlog<{ topics: BlogTopic[] }>("/topics", ["topics"]);
}

// siteUrl 站点绝对地址（canonical / sitemap / RSS / OG 用）。
export function siteUrl(): string {
  return (process.env.BLOG_SITE_URL ?? "http://localhost:3101").replace(/\/$/, "");
}
