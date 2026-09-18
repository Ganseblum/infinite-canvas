import type { Work } from "@/data/site";

/** 图片作品卡：2:3 静帧，黑白渐入彩色，AI 生成内容带标识角标。 */
export default function CoverCard({ work, size = 600 }: { work: Work; size?: number }) {
  return (
    <figure className="cover">
      <span className="cid mono">{work.id}</span>
      {work.aigc ? (
        <span className="aigc mono" title="内容由 AI 生成">
          AI
        </span>
      ) : null}
      <img
        src={`https://picsum.photos/seed/${work.seed}/${size}/${Math.round(size * 1.5)}`}
        alt={work.title}
        loading="lazy"
      />
      <figcaption className="cover-t">
        <b>{work.title}</b>
        <small className="mono">{work.tag}</small>
      </figcaption>
    </figure>
  );
}
