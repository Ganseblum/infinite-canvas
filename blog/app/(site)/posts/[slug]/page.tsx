import type { Metadata } from "next";
import { notFound } from "next/navigation";

import CommentsSection from "@/components/comments-section";
import InteractionBar from "@/components/interaction-bar";
import { fetchPostDetail, siteUrl } from "@/lib/blog-api";

export const revalidate = 300; // 兜底过期；Go 发布/下架/评论管理后按需再验证

type Props = { params: Promise<{ slug: string }> };

// generateMetadata 逐页 head：title/description/canonical/OG + BlogPosting JSON-LD。
export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const detail = await fetchPostDetail(slug).catch(() => null);
  if (!detail) return { title: "文章不存在" };
  const post = detail.post;
  return {
    title: post.title,
    description: post.summary,
    alternates: { canonical: `/posts/${post.slug}` },
    openGraph: {
      type: "article",
      title: post.title,
      description: post.summary,
      url: `${siteUrl()}/posts/${post.slug}`,
      images: post.coverSeed ? [`https://picsum.photos/seed/${post.coverSeed}/1200/750`] : undefined,
      publishedTime: post.publishedAt ?? undefined,
      modifiedTime: post.updatedAt,
      tags: post.tags,
    },
  };
}

export default async function PostPage({ params }: Props) {
  const { slug } = await params;
  const detail = await fetchPostDetail(slug).catch(() => null);
  if (!detail) notFound();
  const { post, contentHtml, toc, prev, next } = detail;

  const jsonLd = {
    "@context": "https://schema.org",
    "@type": "BlogPosting",
    headline: post.title,
    description: post.summary,
    datePublished: post.publishedAt,
    dateModified: post.updatedAt,
    inLanguage: "zh-CN",
    keywords: post.tags.join(", "),
    author: { "@type": "Person", name: "航", url: "https://youc.online" },
    mainEntityOfPage: `${siteUrl()}/posts/${post.slug}`,
    wordCount: post.wordCount,
  };

  const readMinutes = Math.max(1, Math.round(post.wordCount / 400));

  return (
    <main id="view-post">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />
      <div className="post wrap">
        <a className="back mono" href="/">
          返回手记列表
        </a>
        <div className="paper">
          <article className="art">
            <p className="crumb mono">
              手记 / <i>{post.topicName}</i> / VOL.{String(post.volNo).padStart(3, "0")}
            </p>
            <h1>{post.title}</h1>
            <div className="rule" />
            <p className="meta mono">
              {post.publishedAt?.slice(0, 10)} · {post.wordCount.toLocaleString()} 字 · 约 {readMinutes} 分钟 ·
              最后更新 {post.updatedAt.slice(0, 10)} ·{" "}
              {post.originUrl ? (
                <a href={post.originUrl} target="_blank" rel="noreferrer">
                  转载
                </a>
              ) : (
                "原创"
              )}
              {post.isAigcCover ? " · 封面 AI 生成" : ""}
            </p>
            {post.summary ? <p className="lead">{post.summary}</p> : null}

            {/* content_html 由 Go 侧 goldmark 渲染（raw HTML 默认转义），Next 只做展示层 */}
            <div className="content-html" dangerouslySetInnerHTML={{ __html: contentHtml }} />

            {post.tags.length > 0 ? (
              <div className="tags mono">
                {post.tags.map((tag) => (
                  <span key={tag}>{tag}</span>
                ))}
              </div>
            ) : null}

            <InteractionBar postId={post.id} postSlug={post.slug} likeCount={detail.likeCount} bookmarkCount={detail.bookmarkCount} />

            <CommentsSection postSlug={post.slug} initialTotal={detail.commentCount} />

            <div className="pn">
              <a href={prev.slug ? `/posts/${prev.slug}` : "#post"} className={prev.slug ? "" : "is-disabled"}>
                <span className="d mono">← 上一篇</span>
                <span className="ti">{prev.title || "没有更早的了"}</span>
              </a>
              <a href={next.slug ? `/posts/${next.slug}` : "#post"} className={next.slug ? "next" : "next is-disabled"}>
                <span className="d mono">下一篇 →</span>
                <span className="ti">{next.title || "没有更新的了"}</span>
              </a>
            </div>
          </article>
          <aside className="toc">
            <div className="t mono">目录 — TOC</div>
            {toc.map((item, i) => (
              <a key={item.id} href={`#${item.id}`} className={i === 0 ? "on" : ""} style={{ paddingLeft: item.level === 3 ? 28 : 14 }}>
                {item.text}
              </a>
            ))}
          </aside>
        </div>
      </div>
    </main>
  );
}
