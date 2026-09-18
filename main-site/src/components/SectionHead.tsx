import Link from "next/link";

/** 暗色放映厅的分区标题行：kicker + Anton 大标题 + 中文副题 + 右侧「全部」链接。 */
export default function SectionHead({
  kicker,
  title,
  accent,
  sub,
  moreHref,
  moreLabel,
}: {
  kicker: string;
  title: string;
  /** 标题里的描边部分，如 "/128" */
  accent?: string;
  sub?: string;
  moreHref?: string;
  moreLabel?: string;
}) {
  return (
    <div className="sec-head-row">
      <div>
        <p className="tagline mono">{kicker}</p>
        <h2 className="sec-title unb">
          {title}
          {accent ? <span className="ol">{accent}</span> : null}
        </h2>
        {sub ? <span className="zh-sub">{sub}</span> : null}
      </div>
      {moreHref ? (
        <Link className="more mono" href={moreHref}>
          {moreLabel ?? "查看全部"} →
        </Link>
      ) : null}
    </div>
  );
}
