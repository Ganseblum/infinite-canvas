import type { Film } from "@/data/site";

/** 视频卡：16:9 带时长角标与悬停播放环，下方标题与类型元信息。 */
export default function FilmCard({ film }: { film: Film }) {
  return (
    <article className="film">
      <div className="film-box">
        <img
          src={`https://picsum.photos/seed/${film.seed}/940/529`}
          alt={`《${film.title}》封面帧`}
          loading="lazy"
        />
        <span className="ring">▶</span>
        <span className="dur mono">{film.duration}</span>
      </div>
      <div className="film-meta">
        <b>
          {film.title}
          {film.aigc ? <sup className="aigc-inline mono">AI</sup> : null}
        </b>
        <span className="mono">
          {film.type} · {film.year}
        </span>
      </div>
    </article>
  );
}
