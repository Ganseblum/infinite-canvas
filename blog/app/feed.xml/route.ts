import { fetchPostList } from "@/lib/blog-api";

// RSS 全文订阅：免费站愿意喂阅读器，输出已发布文章的完整 HTML 正文。
// 路由本身不缓存（每次生成），数据层带标签缓存 + 兜底过期。
export const dynamic = "force-dynamic";

/** XML 特殊字符转义，防止标题/摘要里的 &、< 破坏 RSS 结构。 */
function escapeXml(s: string) {
  return s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

export async function GET() {
  const site = (process.env.BLOG_SITE_URL ?? "http://localhost:3101").replace(/\/$/, "");
  const list = await fetchPostList({ page: 1 }).catch(() => null);
  // RSS 只输出最近 20 篇，完整历史靠站点与 sitemap
  const posts = list?.posts.slice(0, 20) ?? [];

  const items: string[] = [];
  for (const post of posts) {
    let html = "";
    try {
      const res = await fetch(`${process.env.BLOG_API_BASE ?? "http://127.0.0.1:8080/api/v1/blog"}/posts/${post.slug}`, {
        next: { tags: ["posts"], revalidate: 300 },
      });
      if (res.ok) {
        const detail = (await res.json()) as { contentHtml: string };
        html = detail.contentHtml;
      }
    } catch {
      // 详情拉取失败时退化为摘要
    }
    items.push(
      `<item>` +
        `<title>${escapeXml(post.title)}</title>` +
        `<link>${site}/posts/${post.slug}</link>` +
        `<guid isPermaLink="true">${site}/posts/${post.slug}</guid>` +
        `<pubDate>${new Date(post.publishedAt ?? post.createdAt).toUTCString()}</pubDate>` +
        `<description>${escapeXml(post.summary)}</description>` +
        `<content:encoded><![CDATA[${html || post.summary}]]></content:encoded>` +
        `</item>`,
    );
  }

  const xml =
    `<?xml version="1.0" encoding="UTF-8"?>` +
    `<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">` +
    `<channel>` +
    `<title>航的手记</title>` +
    `<link>${site}</link>` +
    `<description>放映厅开灯之后，把过程写下来。</description>` +
    `<language>zh-CN</language>` +
    `<lastBuildDate>${new Date().toUTCString()}</lastBuildDate>` +
    items.join("") +
    `</channel></rss>`;

  return new Response(xml, { headers: { "Content-Type": "application/xml; charset=utf-8" } });
}
