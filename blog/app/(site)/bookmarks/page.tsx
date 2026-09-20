"use client";

import { useEffect, useState } from "react";

import type { BlogPost } from "@/lib/blog-api";
import { authFetch } from "@/lib/auth-client";

// 我的收藏：登录后可见自己的收藏列表（401 提示登录）。纯 client 页。
/**
 * 收藏页三态：unauthorized（401）引导登录、posts === null 加载中、其余渲染收藏列表。
 * 纯 client 页，只在挂载后经同域代理拉一次自己的收藏，不做翻页与自动刷新。
 */
export default function BookmarksPage() {
  const [posts, setPosts] = useState<BlogPost[] | null>(null);
  const [total, setTotal] = useState(0);
  const [unauthorized, setUnauthorized] = useState(false);

  useEffect(() => {
    void (async () => {
      const res = await authFetch("/api/v1/blog/bookmarks");
      if (res.status === 401) {
        setUnauthorized(true);
        return;
      }
      if (!res.ok) return;
      const data = (await res.json()) as { posts: BlogPost[]; total: number };
      setPosts(data.posts);
      setTotal(data.total);
    })();
  }, []);

  return (
    <main id="view-topics">
      <div className="pagehero wrap">
        <p className="kick mono">BOOKMARKS — 我的收藏</p>
        <h2 className="anton">BOOKMARKS.</h2>
        <p className="zh">{total} 篇收藏 · 仅自己可见。</p>
      </div>
      <section className="sec wrap">
        {unauthorized ? (
          <p className="zh-sub">
            收藏需要登录 —{" "}
            <a href="/login?next=/bookmarks">去登录 ↗</a>
          </p>
        ) : posts === null ? (
          <p className="zh-sub">加载中…</p>
        ) : (
          <div className="frags">
            {posts.map((p) => (
              <a className="frag" key={p.id} href={`/posts/${p.slug}`}>
                <span className="num">{String(p.volNo).padStart(3, "0") || "LOG"}</span>
                <div>
                  <h3>{p.title}</h3>
                  <p>{p.summary}</p>
                </div>
                <time className="mono">{p.publishedAt?.slice(0, 10)}</time>
              </a>
            ))}
            {posts.length === 0 ? <p className="zh-sub">还没有收藏，去文章页点「★ 收藏」吧。</p> : null}
          </div>
        )}
      </section>
    </main>
  );
}
