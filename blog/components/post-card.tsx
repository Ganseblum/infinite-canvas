import type { BlogPost } from "@/lib/blog-api";

// 文章卡：竖版封面 / 无封面纯文字卡两种形态，与设计稿一一对应。
export default function PostCard({ post }: { post: BlogPost }) {
  if (!post.coverSeed) {
    return (
      <a className="tcard" href={`/posts/${post.slug}`}>
        <span className="no">{String(post.volNo).padStart(3, "0") || "LOG"}</span>
        <span className="cat mono">{post.topicName}</span>
        <h3>{post.title}</h3>
        <p className="ex">{post.summary}</p>
        <p className="meta mono">
          {post.publishedAt?.slice(0, 10)} · {post.wordCount.toLocaleString()} 字
        </p>
      </a>
    );
  }
  return (
    <a className="card p" href={`/posts/${post.slug}`}>
      <div className="ph">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={`https://picsum.photos/seed/${post.coverSeed}/720/960`} alt="" />
        {post.isAigcCover ? <span className="aigc mono">AI</span> : null}
      </div>
      <div className="body">
        <span className="cat mono">{post.topicName}</span>
        <h3>{post.title}</h3>
        <p className="ex">{post.summary}</p>
        <p className="meta mono">
          {post.publishedAt?.slice(0, 10)} · {post.wordCount.toLocaleString()} 字
        </p>
      </div>
    </a>
  );
}
