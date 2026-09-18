/** 无缝跑马灯（内容渲染两份，CSS 平移 50% 实现循环）。 */
export default function Marquee({ items }: { items: string[] }) {
  const line = items.map((t) => `✦ ${t}`).join("　") + "　";
  return (
    <div className="marquee" aria-hidden="true">
      <div className="track">
        <span>{line}</span>
        <span>{line}</span>
      </div>
    </div>
  );
}
