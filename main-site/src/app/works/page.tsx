import type { Metadata } from "next";
import Header from "@/components/Header";
import Reveal from "@/components/Reveal";
import CoverCard from "@/components/CoverCard";
import { works } from "@/data/site";

export const metadata: Metadata = {
  title: "影像作品",
  description:
    "航的图像作品集：AI 生成与摄影作品 128 张，人像、城市、插画、商业渲染与实验影像。",
  alternates: { canonical: "/works/" },
};

const TAGS = ["全部", "人像", "城市", "插画", "商业", "风景", "实验"];

export default function WorksPage() {
  return (
    <>
      <Header active="/works/" />
      <main id="main">
        <section className="page-hero wrap">
          <p className="tagline mono">CARGO 01 — 图像作品</p>
          <h1 className="unb">
            STILL<span className="ol">/128</span>
          </h1>
          <p className="sub">
            筛选与详情页待接图库数据；AI 生成的作品按《人工智能生成合成内容标识办法》带
            AI 角标。
          </p>
        </section>
        <section className="sec wrap">
          <div className="chips mono" aria-label="分类筛选（待接数据）">
            {TAGS.map((t, i) => (
              <button key={t} type="button" className={`chip${i === 0 ? " on" : ""}`}>
                {t}
              </button>
            ))}
          </div>
          <Reveal>
            <div className="cover-grid">
              {works.map((w) => (
                <CoverCard key={w.id} work={w} />
              ))}
            </div>
          </Reveal>
        </section>
      </main>
    </>
  );
}
