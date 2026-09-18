import type { Metadata } from "next";
import Header from "@/components/Header";
import Reveal from "@/components/Reveal";
import SectionHead from "@/components/SectionHead";
import FileCard from "@/components/FileCard";
import { templates, hotWords } from "@/data/site";

export const metadata: Metadata = {
  title: "三件套模板",
  description:
    "航的 Office 三件套模板库：PPT、Word、Excel 共 46 套，年终总结、毕业答辩、简历、甘特图，每月上新，免费下载。",
  alternates: { canonical: "/templates/" },
};

const GROUPS = [
  { app: "ppt" as const, kicker: "POWERPOINT — 演示文稿", title: "PPT", total: 18 },
  { app: "word" as const, kicker: "WORD — 文档", title: "WORD", total: 12 },
  { app: "excel" as const, kicker: "EXCEL — 表格", title: "EXCEL", total: 16 },
];

export default function TemplatesPage() {
  return (
    <>
      <Header active="/templates/" />
      <main id="main">
        <section className="page-hero wrap">
          <p className="tagline mono">CARGO 03 — 三件套模板 · 46 套</p>
          <h1 className="unb">
            FILES<span className="ol">.EXE</span>
          </h1>
          <p className="sub">
            每月 1 号上新，全部免费、可商用。下载与详情待接入模板站数据源。
          </p>
        </section>
        <section className="sec wrap">
          <div className="hotwords mono">
            <span className="lab">热搜词：</span>
            {hotWords.map((w) => (
              <span className="hw" key={w}>
                {w}
              </span>
            ))}
          </div>
          {GROUPS.map((g) => (
            <div key={g.app} style={{ marginBottom: 40 }}>
              <Reveal>
                <SectionHead
                  kicker={g.kicker}
                  title={g.title}
                  accent={`/${g.total}`}
                  sub="按颜色找更快：标题即「颜色 + 风格 + 场景」"
                />
              </Reveal>
              <Reveal>
                <div className="files">
                  {templates
                    .filter((t) => t.app === g.app)
                    .map((t) => (
                      <FileCard key={t.no} template={t} />
                    ))}
                </div>
              </Reveal>
            </div>
          ))}
        </section>
      </main>
    </>
  );
}
