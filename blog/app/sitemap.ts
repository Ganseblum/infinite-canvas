import type { MetadataRoute } from "next";

import { fetchPostList, fetchTopics, siteUrl } from "@/lib/blog-api";

export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const base = siteUrl();
  const entries: MetadataRoute.Sitemap = [
    { url: `${base}/`, changeFrequency: "monthly", priority: 1 },
    { url: `${base}/topics`, changeFrequency: "monthly", priority: 0.6 },
    { url: `${base}/about`, changeFrequency: "yearly", priority: 0.4 },
  ];
  try {
    const topics = await fetchTopics();
    for (const tp of topics.topics) {
      entries.push({ url: `${base}/topics/${tp.slug}`, changeFrequency: "monthly", priority: 0.6 });
    }
    let page = 1;
    for (;;) {
      const list = await fetchPostList({ page });
      for (const post of list.posts) {
        entries.push({
          url: `${base}/posts/${post.slug}`,
          lastModified: new Date(post.updatedAt),
          changeFrequency: "monthly",
          priority: 0.8,
        });
      }
      if (list.posts.length < list.size || page >= 50) break;
      page++;
    }
  } catch {
    // 内容 API 不可用时至少给出静态页
  }
  return entries;
}
