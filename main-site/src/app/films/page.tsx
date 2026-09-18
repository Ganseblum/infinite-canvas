import type { Metadata } from "next";
import Header from "@/components/Header";
import Reveal from "@/components/Reveal";
import FilmCard from "@/components/FilmCard";
import { films } from "@/data/site";

export const metadata: Metadata = {
  title: "视频短片",
  description: "航的视频作品：延时摄影、商业广告片、手作纪录与 AI 实验影像。",
  alternates: { canonical: "/films/" },
};

export default function FilmsPage() {
  const featured = films[0];
  return (
    <>
      <Header active="/films/" />
      <main id="main">
        <section className="page-hero wrap">
          <p className="tagline mono">CARGO 02 — 视频短片</p>
          <h1 className="unb">
            MOTION<span className="ol">/04</span>
          </h1>
          <p className="sub">延时、广告与实验影像。播放页待接视频源，当前为封面预览。</p>
        </section>
        <section className="sec wrap">
          <Reveal>
            <div className="films-split">
              <div className="player">
                <img
                  src={`https://picsum.photos/seed/${featured.seed}/1100/620`}
                  alt={`《${featured.title}》封面帧`}
                />
                <span className="ring">▶</span>
                <span className="tl">
                  <i />
                </span>
              </div>
              <div className="mlist">
                {films.map((f) => (
                  <div className="mrow" key={f.id}>
                    <div>
                      <b>
                        {f.title}
                        {f.aigc ? <sup className="aigc-inline mono">AI</sup> : null}
                      </b>
                      <small className="mono">
                        {f.type} · {f.year}
                      </small>
                    </div>
                    <span className="m-dur mono">{f.duration}</span>
                  </div>
                ))}
              </div>
            </div>
          </Reveal>
          <Reveal>
            <div className="rail" style={{ marginTop: 34 }}>
              {films.map((f) => (
                <FilmCard key={f.id} film={f} />
              ))}
            </div>
          </Reveal>
        </section>
      </main>
    </>
  );
}
