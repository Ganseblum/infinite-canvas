import Link from "next/link";
import Header from "@/components/Header";
import HeroVideo from "@/components/HeroVideo";
import Marquee from "@/components/Marquee";
import Reveal from "@/components/Reveal";
import SectionHead from "@/components/SectionHead";
import CoverCard from "@/components/CoverCard";
import FilmCard from "@/components/FilmCard";
import FileCard from "@/components/FileCard";
import LogRow from "@/components/LogRow";
import { works, films, templates, posts } from "@/data/site";

export default function HomePage() {
  return (
    <>
      <Header />
      <main id="main">
        {/* ===== Hero：背景放映视频 + Anton 大字 ===== */}
        <section className="hero">
          <HeroVideo
            src="https://mdn.github.io/shared-assets/videos/flower.mp4"
            poster="https://picsum.photos/seed/ms-hero/1600/900"
          />
          <div className="wrap hero-inner">
            <div>
              <p className="kicker mono">PORTFOLIO 2026 — 图像 / 视频 / 模板 / 手记</p>
              <h1 className="unb">
                <span>MAKING</span>
                <span className="stroke">IMAGES</span>
                <span>
                  &amp; <span className="acc">MOTION.</span>
                </span>
              </h1>
              <p className="zh">
                我是<b>航</b>，做图像，也做动态；排得了一页 PPT，也写得了长文。
                作品、模板和博客，全部收进这一站。
              </p>
              <div className="cta-row" style={{ marginTop: 28 }}>
                <Link className="btn btn-solid" href="/works/">
                  看作品
                </Link>
                <Link className="btn btn-ol" href="/templates/">
                  模板库
                </Link>
              </div>
            </div>
            <div className="side">
              <span className="vertical mono">SCROLL — 滚动进入正片</span>
            </div>
          </div>
        </section>

        <Marquee
          items={["图片作品", "视频短片", "OFFICE 模板", "博客手记"]}
        />

        {/* ===== 影像 · 静帧 ===== */}
        <section className="sec wrap">
          <Reveal>
            <SectionHead
              kicker="CARGO 01 — 图像作品"
              title="STILL"
              accent="/128"
              sub="精选 6 / 128，黑白渐进彩色，划过点亮"
              moreHref="/works/"
              moreLabel="全部 128 张"
            />
          </Reveal>
          <Reveal>
            <div className="rail">
              {works.slice(0, 6).map((w) => (
                <CoverCard key={w.id} work={w} />
              ))}
            </div>
          </Reveal>
        </section>

        {/* ===== 影片 ===== */}
        <section className="sec wrap">
          <Reveal>
            <SectionHead
              kicker="CARGO 02 — 视频短片"
              title="FILMS"
              accent="/04"
              sub="延时、广告与实验影像"
              moreHref="/films/"
              moreLabel="全部短片"
            />
          </Reveal>
          <Reveal>
            <div className="rail">
              {films.slice(0, 3).map((f) => (
                <FilmCard key={f.id} film={f} />
              ))}
            </div>
          </Reveal>
        </section>

        {/* ===== 模板 ===== */}
        <section className="sec wrap">
          <Reveal>
            <SectionHead
              kicker="CARGO 03 — 三件套模板 · 46 套"
              title="FILES"
              accent=".EXE"
              sub="PPT / Word / Excel，每月上新，按颜色找更快"
              moreHref="/templates/"
              moreLabel="全部 46 套"
            />
          </Reveal>
          <Reveal>
            <div className="files">
              {templates.slice(0, 3).map((t) => (
                <FileCard key={t.no} template={t} />
              ))}
            </div>
          </Reveal>
        </section>

        {/* ===== 手记 ===== */}
        <section className="sec wrap">
          <Reveal>
            <SectionHead
              kicker="TOWER — 博客筹备中 · 手记先行"
              title="NOTES"
              accent="/12"
              sub="先放三篇样稿"
              moreHref="/blog/"
              moreLabel="订阅更新"
            />
          </Reveal>
          <Reveal>
            <div className="logs">
              {posts.slice(0, 3).map((p, i) => (
                <LogRow key={p.date} post={p} index={i} />
              ))}
            </div>
          </Reveal>
        </section>
      </main>
    </>
  );
}
