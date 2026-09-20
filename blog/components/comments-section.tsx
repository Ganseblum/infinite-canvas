"use client";

import { useCallback, useEffect, useState } from "react";

import { authFetch } from "@/lib/auth-client";

// 评论区：列表公开可读（游客可见 visible），发评/回复/点赞需登录（401 提示登录）。
// 评论正文是短 Markdown，服务端渲染为 contentHtml（goldmark 同源管线）。
// 评论条目：服务端渲染好的视图模型，正文直接吃 contentHtml。
type CommentItem = {
  id: string;
  replyToId?: string; // 被回复评论 id，有值即楼中楼
  replyToName?: string; // 被回复人昵称，仅展示用
  contentHtml: string; // 服务端 goldmark 渲染后的 HTML（输入是短 Markdown）
  status: string; // visible / hidden（管理员隐藏占位）/ quarantined（审核隔离待放行）
  pinned: boolean;
  likeCount: number;
  liked: boolean;
  author: { id: string; name: string; avatarUrl: string; isAdmin: boolean };
  createdAt: string;
};

// 主站注册入口（博客子域不承载注册）；未配置时退化为 "#"。
const LOGIN_URL = process.env.NEXT_PUBLIC_LOGIN_URL ?? "#";

/**
 * 评论区组件。所有写操作（发评/点赞/删除）成功后都整表重拉而不是本地插入/改写：
 * 先发后审、置顶、隐藏占位与排序都以服务端状态为唯一权威；
 * 401 统一落在底部提示行引导登录，接口失败不阻塞列表展示。
 */
export default function CommentsSection({ postSlug, initialTotal }: { postSlug: string; initialTotal: number }) {
  const [items, setItems] = useState<CommentItem[] | null>(null);
  const [total, setTotal] = useState(initialTotal);
  const [content, setContent] = useState("");
  const [replyTo, setReplyTo] = useState<CommentItem | null>(null);
  const [hint, setHint] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const res = await fetch(`/api/v1/blog/posts/${postSlug}/comments`);
    if (!res.ok) return;
    const data = (await res.json()) as { comments: CommentItem[]; total: number };
    setItems(data.comments);
    setTotal(data.total);
  }, [postSlug]);

  useEffect(() => {
    void load();
  }, [load]);

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    const res = await authFetch(`/api/v1/blog/posts/${postSlug}/comments`, {
      method: "POST",
      body: JSON.stringify({ content, replyToId: replyTo?.id }),
    });
    setBusy(false);
    if (res.status === 401) {
      setHint("登录后才能评论");
      return;
    }
    if (!res.ok) {
      const data = (await res.json().catch(() => null)) as { error?: { message?: string } } | null;
      setHint(data?.error?.message ?? "发表失败，稍后再试");
      return;
    }
    const out = (await res.json()) as { comment: { status: string } };
    setContent("");
    setReplyTo(null);
    // 命中审核进隔离时不本地插入新评论，提示等待放行；无论哪种状态都重拉列表对齐服务端
    setHint(out.comment.status === "quarantined" ? "评论已提交，正在等待审核放行。" : "");
    void load();
  };

  // 评论点赞与文章点赞共用 reactions 端点，按 targetType 区分目标
  const likeComment = async (cm: CommentItem) => {
    const res = await authFetch(`/api/v1/blog/reactions`, {
      method: cm.liked ? "DELETE" : "POST",
      body: JSON.stringify({ targetType: "comment", targetId: cm.id }),
    });
    if (res.status === 401) {
      setHint("登录后才能点赞");
      return;
    }
    void load();
  };

  // 删除入口对每条评论都渲染，能否删除由服务端校验；非 401 的失败（如无权限）静默忽略
  const deleteComment = async (cm: CommentItem) => {
    const res = await authFetch(`/api/v1/blog/comments/${cm.id}`, { method: "DELETE" });
    if (res.status === 401) {
      setHint("登录后才能操作");
      return;
    }
    void load();
  };

  return (
    <section className="cmts">
      <h2>
        评论 <small>{total} 条 · 登录后可评论 · 徽章 = 平台角色</small>
      </h2>

      {items === null ? (
        <p className="zh-sub">评论加载中…</p>
      ) : items.length === 0 ? (
        <p className="zh-sub">还没有评论，坐第一条吧。</p>
      ) : (
        items.map((cm) => (
          <div className="cmt" key={cm.id}>
            <div className="av" aria-hidden="true" />
            <div>
              <div className="who">
                {cm.author.name}
                {cm.author.isAdmin ? <span className="badge admin">管理员</span> : null}
                {cm.pinned ? <span className="badge">置顶</span> : null}
                <time>{cm.createdAt.slice(0, 16).replace("T", " ")}</time>
              </div>
              <div className="cmt-content" dangerouslySetInnerHTML={{ __html: cm.contentHtml }} />
              {cm.replyToName ? <div className="reply-cmt">↳ 回复 {cm.replyToName}</div> : null}
              <div className="ops mono">
                <a onClick={() => likeComment(cm)}>{cm.liked ? "♥" : "♡"} {cm.likeCount}</a>
                <a onClick={() => setReplyTo(cm)}>回复</a>
                <a onClick={() => deleteComment(cm)}>删除</a>
              </div>
            </div>
          </div>
        ))
      )}

      {replyTo ? (
        <p className="zh-sub">
          正在回复 {replyTo.author.name} ·{" "}
          <a onClick={() => setReplyTo(null)}>取消</a>
        </p>
      ) : null}
      <div className="cmt-form">
        <p className="tip">评论支持 Markdown（粗体 / 行内代码 / 链接）· 先发后审，命中风险进隔离待放行</p>
        <textarea
          value={content}
          onChange={(ev) => setContent(ev.target.value)}
          placeholder="写下你的想法…（登录后可评论）"
        />
        <div className="foot">
          <span className="hint mono">{hint || "评论走平台账号 · 30 秒一条 · 单篇 2000 字以内"}</span>
          <button className="btn solid" onClick={submit} disabled={busy || content.trim().length < 2}>
            发表评论
          </button>
        </div>
        <p className="hint mono">
          没有账号？<a href={LOGIN_URL} target="_blank" rel="noreferrer">去主站注册 ↗</a>（登录请点右上角）
        </p>
      </div>
    </section>
  );
}
