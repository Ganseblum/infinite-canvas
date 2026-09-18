import type { Post } from "@/data/site";

/** 博客手记行：编号 + 标题摘要 + 日期 + 状态（可读 / 起飞前）。 */
export default function LogRow({ post, index }: { post: Post; index: number }) {
  return (
    <article className="log">
      <span className="num unb">{String(index + 1).padStart(2, "0")}</span>
      <div>
        <h3>{post.title}</h3>
        <p>{post.excerpt}</p>
      </div>
      <time className="mono">{post.date}</time>
      <span className={`st mono${post.status === "live" ? " live" : ""}`}>
        {post.status === "live" ? "可读" : "起飞前"}
      </span>
    </article>
  );
}
