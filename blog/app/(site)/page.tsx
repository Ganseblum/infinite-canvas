import { fetchPostList, fetchTopics, siteUrl, type BlogPost } from "@/lib/blog-api";
import PostCard from "@/components/post-card";

export const revalidate = 300; // 兜底过期；正常由 Go 的再验证回调秒级失效

export const metadata = {
  alternates: { canonical: "/" },
};

// JSON-LD：WebSite + Blog 结构化数据（SEO 方案 §4.2）。
function HomeJsonLd({ total }: { total: number }) {
  const data = {
    "@context": "https://schema.org",
    "@graph": [
      {
        "@type": "WebSite",
        name: "航的手记",
        url: siteUrl(),
        inLanguage: "zh-CN",
      },
      {
        "@type": "Blog",
        name: "航的手记",
        url: siteUrl(),
        blogPostCount: total,
      },
    ],
  };
  return <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }} />;
}

/**
 * 首页（/）：Hero 头条 + 手记列表（分页 / 栏目筛选）+ 栏目索引。
 * 数据经 fetchPostList / fetchTopics 从 Go 内网拉取，ISR 300s 兜底过期，
 * Go 发布/下架后回调 /api/revalidate 失效 posts / topics 标签；分页与筛选由 searchParams 驱动。
 */
export default async function HomePage({ searchParams }: { searchParams: Promise<{ page?: string; topic?: string }> }) {
  const params = await searchParams;
  const page = Math.max(1, Number(params.page ?? "1") || 1);
  const topic = params.topic;

  const [list, topics] = await Promise.all([fetchPostList({ page, topic }), fetchTopics()]);
  // 列表第一条作为本期头条（Hero 引导 + 大卡），其余进网格
  const [featured, ...rest] = list.posts;
  const totalPages = Math.max(1, Math.ceil(list.total / list.size));

  return (
    <>
      <HomeJsonLd total={list.total} />
      <main id="view-home">
        {/* Hero：大标题 + 数据 + 本期头条引导 */}
        <div className="hero">
          <div className="wrap inner">
            <div style={{ flex: 1 }}>
              <p className="kicker mono">HANG&apos;S NOTEBOOK — 自 2024 · 每月排印一期</p>
              <h1 className="anton">
                <span>WRITING</span>
                <span className="stroke">IN THE</span>
                <span className="acc">DARK.</span>
              </h1>
              <p className="zh">
                放映厅开灯之后，把过程写下来。<b>AI 工作流、独立开发、模板配色</b>
                ，和一些不成体系的念头——在这里排版成册。
              </p>
              {featured ? (
                <a className="latest mono" href={`/posts/${featured.slug}`}>
                  本期 VOL.{String(featured.volNo).padStart(3, "0")} — {featured.title} · 点开读全文
                </a>
              ) : null}
            </div>
            <div className="side">
              <span className="vertical mono">SCROLL — 翻开本期</span>
              <div className="stats">
                <div>
                  <div className="n">{list.total} 篇</div>
                  <div className="l mono">手记 POSTS</div>
                </div>
                <div>
                  <div className="l mono">MONTHLY — 月更 · 从不烂尾</div>
                </div>
              </div>
              <div className="cta-row">
                <a className="btn solid" href="#log">
                  从第一篇读起
                </a>
              </div>
            </div>
          </div>
        </div>

        <div className="marquee" aria-hidden="true">
          <div className="track">
            {Array.from({ length: 2 }).map((_, i) => (
              <span key={i}>
                手记 LOGS ✺ AI 工作流 ✺ 独立开发 ✺ 模板配色 ✺ 创作方法 ✺ 碎念 ✺ 手记 LOGS ✺ AI 工作流 ✺ 独立开发 ✺
                模板配色 ✺ 创作方法 ✺ 碎念 ✺&nbsp;
              </span>
            ))}
          </div>
        </div>

        <section className="sec wrap" id="log">
          <div className="sec-head">
            <div>
              <h2 className="anton">LOGS</h2>
              <span className="zh-sub">手记 — 按栏目挑，或从头往下翻；AI 生成封面均带角标</span>
            </div>
            <span className="more mono">全部 {list.total} 篇</span>
          </div>

          <div className="chips">
            <a className={'chip' + (topic ? '' : ' on')} href={topic ? '/' : '#log'}>
              全部<em>{list.total}</em>
            </a>
            {topics.topics.map((tp) => (
              <a key={tp.id} className={"chip" + (topic === tp.slug ? " on" : "")} href={`/?topic=${tp.slug}`}>
                {tp.name}
                <em>{tp.postCount}</em>
              </a>
            ))}
          </div>

          {topic && list.posts.length === 0 ? (
            <p className="zh-sub" style={{ margin: '0 0 24px' }}>该栏目下暂时还没有已发布的文章。</p>
          ) : null}

          {featured ? (
            <a className="feat" href={`/posts/${featured.slug}`}>
              <div className="cover">
                <img src={`https://picsum.photos/seed/${featured.coverSeed || "hang-blog-feat"}/1200/750`} alt="" />
                {featured.isAigcCover ? <span className="aigc mono">AI 封面</span> : null}
              </div>
              <div className="txt">
                <span className="cat mono">
                  {featured.isPinned ? "置顶头条 · " : "本期头条 · "}
                  {featured.topicName}
                </span>
                <h3>{featured.title}</h3>
                <p className="ex">{featured.summary}</p>
                <p className="meta mono">
                  {featured.publishedAt?.slice(0, 10)} · {featured.wordCount.toLocaleString()} 字 · 约
                  {Math.max(1, Math.round(featured.wordCount / 400))} 分钟
                </p>
                <span className="go mono">读这篇文章 →</span>
              </div>
            </a>
          ) : null}

          <div className="grid">
            {rest.map((p) => (
              <PostCard key={p.id} post={p} />
            ))}
          </div>

          {totalPages > 1 ? (
            <div className="pager mono">
              {page > 1 ? <a href={`/?page=${page - 1}${topic ? `&topic=${topic}` : ""}`}>← 上一页</a> : null}
              <span className="pager-now">
                {page} / {totalPages}
              </span>
              {page < totalPages ? (
                <a href={`/?page=${page + 1}${topic ? `&topic=${topic}` : ""}`}>下一页 →</a>
              ) : null}
            </div>
          ) : null}
        </section>

        <section className="sec wrap" id="topics">
          <div className="sec-head">
            <div>
              <h2 className="anton">TOPICS</h2>
              <span className="zh-sub">栏目索引 — 每个栏目都在连载，挑一个追</span>
            </div>
            <a className="more mono" href="/topics">
              全部栏目 →
            </a>
          </div>
          <div className="topics">
            {topics.topics.map((tp) => (
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
    </>
  );
}
