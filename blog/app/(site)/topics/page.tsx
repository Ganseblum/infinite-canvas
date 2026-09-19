import { fetchTopics } from "@/lib/blog-api";

export const metadata = {
  title: "栏目索引",
  description: "航的手记全部栏目：AI 工作流、独立开发、模板配色、创作方法与碎念。",
  alternates: { canonical: "/topics" },
};

export default async function TopicsIndexPage() {
  const topics = await fetchTopics().catch(() => null);
  return (
    <main id="view-topics">
      <div className="pagehero wrap">
        <p className="kick mono">TOPICS — 全部栏目</p>
        <h2 className="anton">TOPICS.</h2>
        <p className="zh">每个栏目都在连载，挑一个追。</p>
      </div>
      <section className="sec wrap">
        <div className="topics">
          {topics?.topics.map((tp) => (
            <a key={tp.id} className="topic" href={`/topics/${tp.slug}`}>
              <span className="en">
                {tp.enName || tp.name}
                <em>{tp.postCount} 篇</em>
              </span>
              <span className="zh">{tp.name}</span>
              <span className="last">{tp.description}</span>
            </a>
          ))}
          {topics && topics.topics.length === 0 ? <p className="zh-sub">栏目还没有建立，去后台排字吧。</p> : null}
        </div>
      </section>
    </main>
  );
}
