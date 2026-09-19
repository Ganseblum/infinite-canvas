"use client";

// 博客侧登录态：accessToken 存 localStorage，互动请求带 Bearer 头。
// access token 只有 15 分钟；过期后用登录时种下的 ic_refresh cookie（30 天、HttpOnly）静默续期，
// 博客侧不需要单独的登录态轮询。cookie 默认落博客域；服务端配置 COOKIE_DOMAIN 后
// 随主注册域共享，任一站登录全站生效。
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

// 同一时刻只允许一个刷新在飞，其余并发 401 排队等同一个结果
// （写法同 web/src/services/api/client.ts 的 refreshInFlight）。
let refreshInFlight: Promise<string | null> | null = null;

async function performRefresh(): Promise<string | null> {
  try {
    const res = await fetch("/api/v1/auth/refresh", { method: "POST", credentials: "include" });
    const data = (await res.json().catch(() => null)) as { accessToken?: string } | null;
    if (!res.ok || !data?.accessToken) {
      // 刷新失败即 refresh cookie 已失效：清掉本地 token（博客侧没有其它持久化登录态），
      // 让调用方按既有 401 分支提示登录。
      clearToken();
      return null;
    }
    saveToken(data.accessToken);
    return data.accessToken;
  } catch {
    clearToken();
    return null;
  }
}

function refreshSession(): Promise<string | null> {
  if (!refreshInFlight) {
    refreshInFlight = performRefresh().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

// authFetch 互动请求统一入口：带 Bearer（如有）；401 时调用方可提示登录。
// 401 且 code=TOKEN_EXPIRED（或错误体不可解析但本地有 token）时先静默续期，
// 成功后重放原请求且只重放一次。
// body 接受对象或字符串：对象统一序列化（fetch 不接受普通对象，会变成 "[object Object]"）。
type AuthFetchInit = Omit<RequestInit, "body"> & { body?: object | string | null };

async function execute(path: string, init: AuthFetchInit, canRetry: boolean): Promise<Response> {
  const token = getToken();
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const raw = init.body;
  const body = typeof raw === "string" || raw == null ? raw : JSON.stringify(raw);
  if (body) headers.set("Content-Type", "application/json");
  const res = await fetch(path, { ...init, body, headers });
  if (res.status !== 401 || !canRetry) return res;
  // 借 clone 读错误体判断是否 token 过期，不影响调用方随后读原响应；
  // 错误体不可解析时仅本地有 token 才视为过期，避免无效 401 也触发刷新。
  const data = (await res.clone().json().catch(() => null)) as { error?: { code?: string } } | null;
  const expired = data ? data.error?.code === "TOKEN_EXPIRED" : !!getToken();
  if (!expired) return res;
  const renewed = await refreshSession();
  if (!renewed) return res;
  return execute(path, init, false);
}

export async function authFetch(path: string, init: AuthFetchInit = {}): Promise<Response> {
  return execute(path, init, true);
}

// 冷加载 bootstrap：主站登录过的访客打开博客时，页面加载即用共享 refresh cookie
// 静默续期拿 accessToken，实现免登录。仅本地无 token 时执行；任何失败视为匿名
// 访客静默 no-op，绝不 clearToken。同一时刻只发一个请求。
let bootstrapInFlight: Promise<void> | null = null;

async function performBootstrap(): Promise<void> {
  if (getToken() !== null) return;
  try {
    const res = await fetch("/api/v1/auth/refresh", { method: "POST" });
    const data = (await res.json().catch(() => null)) as { accessToken?: string } | null;
    if (res.ok && data?.accessToken) saveToken(data.accessToken);
  } catch {}
}

export function bootstrapSession(): Promise<void> {
  if (!bootstrapInFlight) {
    bootstrapInFlight = performBootstrap().finally(() => {
      bootstrapInFlight = null;
    });
  }
  return bootstrapInFlight;
}
