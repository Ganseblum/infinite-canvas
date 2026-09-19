import { ApiError } from "@/lib/api-error";
import { API_BASE_URL as RUNTIME_API_BASE_URL } from "@/constant/runtime-config";
import type { SessionPayload } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";

type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD";

type QueryValue = string | number | boolean | undefined | null;
type ApiRequestOptions = {
    method?: HttpMethod;
    body?: unknown;
    headers?: Record<string, string>;
    // 数组按重复参数发送（tag=a&tag=b），与后端 QueryArray 的取值方式一致。
    query?: Record<string, QueryValue | QueryValue[]>;
    signal?: AbortSignal;
};

// 默认空串，走同源 /api/v1：生产由 nginx 反代，开发由 vite proxy 转发。
// 容器部署时可由入口脚本注入 API_BASE_URL，同一份镜像可指向不同环境的接口。
export const API_BASE_URL = RUNTIME_API_BASE_URL.trim().replace(/\/+$/, "");

function buildUrl(path: string, query?: ApiRequestOptions["query"]) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query || {})) {
        if (value === undefined || value === null) continue;
        if (Array.isArray(value)) {
            for (const item of value) {
                if (item === undefined || item === null) continue;
                params.append(key, String(item));
            }
            continue;
        }
        params.set(key, String(value));
    }
    const search = params.toString();
    return `${API_BASE_URL}/api/v1${path.startsWith("/") ? path : `/${path}`}${search ? `?${search}` : ""}`;
}

function buildBody(body: unknown, headers: Headers) {
    if (body === undefined || body === null) return undefined;
    if (typeof FormData !== "undefined" && body instanceof FormData) return body;
    if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    return JSON.stringify(body);
}

async function readPayload(response: Response): Promise<unknown> {
    if (response.status === 204) return undefined;
    const text = await response.text();
    if (!text) return undefined;
    try {
        return JSON.parse(text);
    } catch {
        return text;
    }
}

function toRecord(value: unknown): Record<string, unknown> {
    return value !== null && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

async function execute<T>(path: string, options: ApiRequestOptions, canRetry: boolean): Promise<T> {
    const headers = new Headers(options.headers);
    const accessToken = useAuthStore.getState().accessToken;
    if (accessToken && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${accessToken}`);

    let response: Response;
    try {
        response = await fetch(buildUrl(path, options.query), {
            method: options.method ?? "GET",
            headers,
            body: buildBody(options.body, headers),
            credentials: "include",
            signal: options.signal,
        });
    } catch (error) {
        if (options.signal?.aborted) throw error;
        throw new ApiError({ code: "NETWORK_ERROR", status: 0 });
    }

    const payload = await readPayload(response);
    if (response.ok) return payload as T;

    const errorBody = "error" in toRecord(payload) ? toRecord(toRecord(payload).error) : {};
    const code = typeof errorBody.code === "string" ? errorBody.code : `HTTP_${response.status}`;
    const fields = toRecord(errorBody.fields);
    const retryAfterHeader = Number(response.headers.get("Retry-After"));
    const error = new ApiError({
        code,
        message: typeof errorBody.message === "string" ? errorBody.message : "",
        status: response.status,
        fields: Object.keys(fields).length > 0 ? (fields as Record<string, string>) : undefined,
        revision: typeof errorBody.revision === "number" ? errorBody.revision : undefined,
        retryAfter: Number.isFinite(retryAfterHeader) && retryAfterHeader > 0 ? retryAfterHeader : undefined,
        requiredMicros: typeof errorBody.requiredMicros === "number" ? errorBody.requiredMicros : undefined,
        shortfallMicros: typeof errorBody.shortfallMicros === "number" ? errorBody.shortfallMicros : undefined,
        param: typeof errorBody.param === "string" ? errorBody.param : undefined,
        allowed: errorBody.allowed,
        upstreamStatus: typeof errorBody.upstreamStatus === "number" ? errorBody.upstreamStatus : undefined,
        phase: typeof errorBody.phase === "string" ? errorBody.phase : undefined,
    });

    // access token 过期：单飞刷新后重放原请求；刷新失败时 clearSession 会清空登录态，
    // RequireAuth 随之把业务路由重定向到登录页。
    if (code === "TOKEN_EXPIRED" && canRetry) {
        const session = await refreshSession();
        if (session) return execute<T>(path, options, false);
    }
    throw error;
}

export async function apiRequest<T>(path: string, options?: ApiRequestOptions): Promise<T> {
    return execute<T>(path, options || {}, true);
}

let refreshInFlight: Promise<SessionPayload | null> | null = null;

async function performRefresh(): Promise<SessionPayload | null> {
    try {
        const response = await fetch(`${API_BASE_URL}/api/v1/auth/refresh`, { method: "POST", credentials: "include" });
        if (!response.ok) {
            useAuthStore.getState().clearSession();
            return null;
        }
        const payload = (await response.json()) as SessionPayload;
        if (!payload?.accessToken || !payload.user) {
            useAuthStore.getState().clearSession();
            return null;
        }
        useAuthStore.getState().setSession(payload);
        return payload;
    } catch {
        useAuthStore.getState().clearSession();
        return null;
    }
}

// 同一时刻只允许一个刷新在飞，其余并发 401 排队等同一个结果。
export function refreshSession(): Promise<SessionPayload | null> {
    if (!refreshInFlight) {
        refreshInFlight = performRefresh().finally(() => {
            refreshInFlight = null;
        });
    }
    return refreshInFlight;
}
