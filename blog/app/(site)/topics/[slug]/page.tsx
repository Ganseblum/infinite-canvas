import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { fetchPostList, fetchTopics, REVALIDATE_FALLBACK } from "@/lib/blog-api";

type Props = { params: Promise<{ slug: string }>; searchParams: Promise<{ page?: string }> };

// 逐页 head；栏目不存在只给占位标题，真正的 404 由页面本体负责。
export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const topics = await fetchTopics().catch(() => null);
  const topic = topics?.topics.find((tp) => tp.slug === slug);
  if (!topic) return { title: "栏目不存在" };
  return {
    title: `${topic.name} — 栏目`,
    description: topic.description,
    alternates: { canonical: `/topics/${topic.slug}` },
  };
}

/**
 * 栏目详情页（/topics/[slug]）：该栏目文章分页列表 + 其他栏目索引。
 * 栏目列表拉取失败或 slug 不存在时转 404；文章列表失败降级为空列表，
 * 页头栏目信息仍可渲染（topics 已在前一步拿到）。
 */
export default async function TopicPage({ params, searchParams }: Props) {
  const { slug } = await params;
  const { page: pageParam } = await searchParams;
  const page = Math.max(1, Number(pageParam ?? "1") || 1);
  const topics = await fetchTopics().catch(() => null);
  const topic = topics?.topics.find((tp) => tp.slug === slug);
  if (!topic) notFound();
  const list = await fetchPostList({ topic: slug, page }).catch(() => null);
  const posts = list?.posts ?? [];
  const pages = list ? Math.max(1, Math.ceil(list.total / list.size)) : 1;

  return (
    <main id="view-topics">
      <div className="pagehero wrap">
        <p className="kick mono">TOPIC — 栏目页 · /topics/{topic.slug}</p>
        <h2 className="anton">{topic.enName || topic.name}</h2>
        <p className="zh">{topic.description || `${topic.name} — 专栏连载中。`}</p>
        <div className="nums mono">
          <span>
            <b>{topic.postCount}</b>篇
          </span>
          <span>最近更新 {posts[0]?.updatedAt.slice(0, 10) ?? "—"}</span>
        </div>
      </div>

      <section className="sec wrap">
        <div className="frags">
          {posts.map((p, i) => (
            <a className="frag" key={p.id} href={`/posts/${p.slug}`}>
              <span className="num">{String(p.volNo || posts.length - i).padStart(3, "0")}</span>
              <div>
                <h3>{p.title}</h3>
                <p>{p.summary}</p>
              </div>
              <time className="mono">
                {p.publishedAt?.slice(0, 10)} · {p.wordCount.toLocaleString()} 字
              </time>
            </a>
          ))}
          {posts.length === 0 ? <p className="zh-sub">该栏目下暂时还没有已发布的文章。</p> : null}
        </div>

        {pages > 1 ? (
          <div className="pager mono">
            {page > 1 ? <a href={`/topics/${slug}?page=${page - 1}`}>← 上一页</a> : null}
            <span className="pager-now">
              {page} / {pages}
            </span>
            {page < pages ? <a href={`/topics/${slug}?page=${page + 1}`}>下一页 →</a> : null}
          </div>
        ) : null}

        <div className="sec-head" style={{ marginTop: "clamp(56px,9vh,90px)" }}>
          <div>
            <h2 className="anton">TOPICS</h2>
            <span className="zh-sub">其他栏目 — 换个车间接着逛</span>
          </div>
          <a className="more mono" href="/topics">
            全部栏目 →
          </a>
        </div>
        <div className="topics">
          {topics?.topics.map((tp) => (
            <a key={tp.id} className="topic" href={`/topics/${tp.slug}`}>
              <span className="en">
                {tp.enName || tp.name}
                <em>{tp.postCount} 篇</em>
              </span>
              <span className="zh">{tp.name}</span>
              <span className="last">{tp.description}</span>
            </a>
          ))}
        </div>
      </section>
    </main>
  );
}
