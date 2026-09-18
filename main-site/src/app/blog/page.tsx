import type { Metadata } from "next";
import Header from "@/components/Header";
import Reveal from "@/components/Reveal";
import LogRow from "@/components/LogRow";
import { posts, site } from "@/data/site";

export const metadata: Metadata = {
  title: "手记 · 博客",
  description: "航的手记与博客：AI 视频工作流、模板配色系统、独立开发与创作方法。",
  alternates: { canonical: "/blog/" },
};

export default function BlogPage() {
  return (
    <>
      <Header active="/blog/" />
      <main id="main">
        <section className="page-hero wrap">
          <p className="tagline mono">TOWER — 博客筹备中 · 手记先行</p>
          <h1 className="unb">
            FLIGHT<span className="ol">LOG</span>
          </h1>
          <p className="sub">
            博客系统接入前，先放样稿与进度。开张后这里按 Markdown 文章驱动。
          </p>
        </section>
        <section className="sec wrap">
          <Reveal>
            <div className="logs">
              {posts.map((p, i) => (
                <LogRow key={p.date} post={p} index={i} />
              ))}
            </div>
          </Reveal>
          <Reveal>
            <div className="subscribe">
              <div>
                <h3>落地之前，保持联络</h3>
                <p>上新和文章更新时发一封邮件，一月一封，绝不涝着，随时退订。</p>
              </div>
              <div className="subscribe-form">
                <a
                  className="btn btn-solid"
                  href={`mailto:${site.email}?subject=订阅上新提醒`}
                >
                  邮件订阅
                </a>
                <p className="mono" style={{ margin: "12px 0 0", fontSize: 11.5, color: "var(--soft)" }}>
                  RSS / 站内订阅表单随博客系统一并接入
                </p>
              </div>
            </div>
          </Reveal>
        </section>
      </main>
    </>
  );
}
