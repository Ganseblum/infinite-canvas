"use client";

// 博客侧登录态：accessToken 存 localStorage，互动请求带 Bearer 头。
// 会话与主应用同一套 Go 认证域；博客子域通过同域代理登录，cookie 也会落在博客域。
const KEY = "blog-access-token";

export function getToken(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function saveToken(token: string) {
  try {
    localStorage.setItem(KEY, token);
  } catch {}
}

export function clearToken() {
  try {
    localStorage.removeItem(KEY);
  } catch {}
}

// authFetch 互动请求统一入口：带 Bearer（如有）；401 时调用方可提示登录。
export async function authFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const token = getToken();
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  // 对象 body 统一序列化：fetch 不接受普通对象（会变成 "[object Object]"）
  const body = typeof init.body === "string" || init.body == null ? init.body : JSON.stringify(init.body);
  if (body) headers.set("Content-Type", "application/json");
  return fetch(path, { ...init, body, headers });
}
