"use client";

import { useState } from "react";

import { saveToken } from "@/lib/auth-client";

// 博客登录页：走同域代理调 Go 认证域（POST /api/v1/auth/login），
// accessToken 存 localStorage 供评论/点赞/收藏带 Bearer 使用。
export default function LoginPage() {
  const [account, setAccount] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const res = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ account, password }),
      });
      const data = (await res.json()) as {
        accessToken?: string;
        user?: { displayName?: string; username?: string };
        error?: { message?: string };
      };
      if (!res.ok || !data.accessToken) {
        setError(data.error?.message ?? "登录失败，请检查邮箱与密码");
        return;
      }
      saveToken(data.accessToken);
      const params = new URLSearchParams(window.location.search);
      const next = params.get("next");
      window.location.href = next && next.startsWith("/") ? next : "/";
    } catch {
      setError("网络异常，稍后再试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <main id="view-about">
      <div className="pagehero wrap">
        <p className="kick mono">LOGIN — 登录排字房</p>
        <h2 className="anton">
          SIGN <span style={{ color: "var(--acc)" }}>IN.</span>
        </h2>
        <p className="zh">评论、点赞与收藏需要平台账号；账号与主站通用。</p>
      </div>
      <section className="wrap" style={{ paddingBottom: "clamp(60px,10vh,110px)" }}>
        <div className="cmt-form" style={{ maxWidth: 480 }}>
          <p className="tip">{error || "登录后即可参与互动；密码与主站一致。"}</p>
          <input
            className="login-input mono"
            placeholder="邮箱或用户名"
            value={account}
            onChange={(ev) => setAccount(ev.target.value)}
            autoComplete="username"
          />
          <input
            className="login-input mono"
            type="password"
            placeholder="密码"
            value={password}
            onChange={(ev) => setPassword(ev.target.value)}
            autoComplete="current-password"
            onKeyDown={(ev) => ev.key === "Enter" && submit()}
          />
          <div className="foot">
            <span className="hint mono">登录即表示同意站点的社区规范</span>
            <button className="btn solid" onClick={submit} disabled={busy || !account || !password}>
              {busy ? "登录中…" : "登录"}
            </button>
          </div>
        </div>
      </section>
    </main>
  );
}
