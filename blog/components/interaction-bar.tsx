"use client";

import { useState } from "react";

import { authFetch } from "@/lib/auth-client";

// 点赞 / 收藏操作栏：走同域代理 /api/v1/blog/*（cookie 落 blog 子域）。
// 未登录（401）时提示登录；重复点击幂等，服务端按主键去重。
// 计数初值来自文章详情接口；liked/marked 的个人状态详情接口不含，
// 首次交互前一律按未选中渲染，以服务端返回值纠正。
type Props = {
  postId: string; // 点赞目标（reactions 端点按 id 定位）
  postSlug: string; // 收藏与登录回跳按 slug 定位
  likeCount: number;
  bookmarkCount: number;
};

export default function InteractionBar({ postId, postSlug, likeCount, bookmarkCount }: Props) {
  const [liked, setLiked] = useState(false);
  const [marked, setMarked] = useState(false);
  const [likes, setLikes] = useState(likeCount);
  const [marks, setMarks] = useState(bookmarkCount);
  const [hint, setHint] = useState("");

  const call = async (method: string, path: string, body?: object) => {
    const res = await authFetch(`/api/v1/blog/${path}`, { method, body });
    if (res.status === 401) {
      setHint("登录后才能互动");
      return null;
    }
    if (!res.ok) {
      setHint("操作失败，稍后再试");
      return null;
    }
    return (await res.json()) as { liked?: boolean; bookmarked?: boolean; count: number };
  };

  const toggleLike = async () => {
    const out = await call(liked ? "DELETE" : "POST", "reactions", { targetType: "post", targetId: postId });
    if (out) {
      setLiked(!!out.liked);
      setLikes(out.count);
      setHint("");
    }
  };

  // 收藏走文章维度的 PUT 开关端点，与点赞的 reactions 端点不同
  const toggleBookmark = async () => {
    const out = await call(marked ? "DELETE" : "PUT", `posts/${postSlug}/bookmark`);
    if (out) {
      setMarked(!!out.bookmarked);
      setMarks(out.count);
      setHint("");
    }
  };

  return (
    <div className="actions mono">
      <button className={"act" + (liked ? " on" : "")} onClick={toggleLike}>
        ♥ 点赞 <b>{likes}</b>
      </button>
      <button className={"act" + (marked ? " on" : "")} onClick={toggleBookmark}>
        ★ 收藏 <b>{marks}</b>
      </button>
      {hint ? (
        <span className="hint mono">
          {hint}（
          <a href={`/login?next=/posts/${postSlug}`}>去登录</a>
          ）
        </span>
      ) : null}
    </div>
  );
}
