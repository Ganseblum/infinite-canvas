import type { Template } from "@/data/site";

const APP_LABEL = { ppt: "POWERPOINT", word: "WORD", excel: "EXCEL" } as const;

/** 三件套模板卡：应用品牌色顶线 + 档案编号 + 「颜色+风格+场景」标题 + 粗粒度下载量。 */
export default function FileCard({ template }: { template: Template }) {
  return (
    <article className={`file f-${template.app}`}>
      <div className="file-row mono">
        <span className="app">
          {APP_LABEL[template.app]} — NO.{template.no}
        </span>
        <span className="dl">↓ {template.downloads}</span>
      </div>
      <h3>{template.title}</h3>
      <p>{template.desc}</p>
      <a className="file-cta mono" href="#" aria-label={`下载模板：${template.title}`}>
        下载模板 →
      </a>
    </article>
  );
}
