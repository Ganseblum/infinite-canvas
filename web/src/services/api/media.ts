import { ApiError } from "@/lib/api-error";
import { API_BASE_URL, apiRequest, refreshSession } from "@/services/api/client";
import type { MediaHead, MediaObject } from "@/services/data/types";
import { useAuthStore } from "@/stores/use-auth-store";

// 媒体地址就是可直接放进 <img src> 的同源 URL：GET/HEAD 用 ic_media cookie 鉴权，
// 地址上不带令牌，缓存交给浏览器。
export function mediaUrl(storageKey?: string) {
    return storageKey ? `${API_BASE_URL}/api/v1/media/${storageKey}` : "";
}

// 下载申请响应：url 指向取件地址——免费档/无干净原件为 /api/v1/media/{key}，付费档有干净原件
// 为 5 分钟有效的签名地址 /api/v1/media-download/{token}；返回哪一档由服务端决定，前端不判档位。
export type MediaDownload = { url: string; expiresAt: string | null };

// 申请下载：先向服务端要一个取件地址，再用 fetchMediaDownload 凭据取件。
export async function requestDownload(storageKey: string): Promise<MediaDownload> {
    return apiRequest<MediaDownload>(`/media/${storageKey}/download`, { method: "POST" });
}

// 写路径（PUT/DELETE）、导出取数与下载取件都走带 Bearer 的原始请求，不能沿用 client.ts 的 JSON 封装。
async function mediaFetch(url: string, init: RequestInit, canRetry: boolean): Promise<Response> {
    const headers = new Headers(init.headers);
    const accessToken = useAuthStore.getState().accessToken;
    if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);

    let response: Response;
    try {
        response = await fetch(url, { ...init, headers, credentials: "include" });
    } catch (error) {
        if (init.signal?.aborted) throw error;
        throw new ApiError({ code: "NETWORK_ERROR", status: 0 });
    }
    if (response.ok) return response;

    const payload = await response.json().catch(() => null);
    const errorBody = payload && typeof payload === "object" && "error" in payload ? ((payload as { error: unknown }).error as Record<string, unknown>) : {};
    const code = typeof errorBody?.code === "string" ? errorBody.code : `HTTP_${response.status}`;
    if (code === "TOKEN_EXPIRED" && canRetry && (await refreshSession())) return mediaFetch(url, init, false);
    throw new ApiError({ code, message: typeof errorBody?.message === "string" ? errorBody.message : "", status: response.status });
}

// 服务端返回的取件 url 可能是相对路径（/api/...），与 mediaUrl 同口径补 API_BASE_URL。
function absoluteMediaUrl(url: string) {
    return url.startsWith("/") ? `${API_BASE_URL}${url}` : url;
}

// 取件：按申请到的 url 拉 blob，凭据口径与 getMediaBlob 完全一致（Bearer + ic_media cookie + 过期刷新重试）。
export async function fetchMediaDownload(url: string, signal?: AbortSignal): Promise<Blob> {
    const response = await mediaFetch(absoluteMediaUrl(url), { method: "GET", signal }, true);
    return response.blob();
}

/** 读取媒体元信息（字节数/类型/校验和），404 时返回 null。HEAD /api/v1/media/{storageKey}。 */
export async function headMedia(storageKey: string, signal?: AbortSignal): Promise<MediaHead | null> {
    const response = await mediaFetch(mediaUrl(storageKey), { method: "HEAD", signal }, true).catch((error: unknown) => {
        if (error instanceof ApiError && error.code === "NOT_FOUND") return null;
        throw error;
    });
    if (!response) return null;
    return {
        bytes: Number(response.headers.get("Content-Length") || 0),
        mimeType: response.headers.get("Content-Type") || "application/octet-stream",
        checksum: response.headers.get("X-Checksum") || "",
    };
}

/** 拉取媒体二进制，404 返回 null。GET /api/v1/media/{storageKey}。 */
export async function getMediaBlob(storageKey: string, signal?: AbortSignal): Promise<Blob | null> {
    const response = await mediaFetch(mediaUrl(storageKey), { method: "GET", signal }, true).catch((error: unknown) => {
        if (error instanceof ApiError && error.code === "NOT_FOUND") return null;
        throw error;
    });
    if (!response) return null;
    return response.blob();
}

/** 上传/覆盖媒体二进制，服务端返回规范化的 MediaObject。PUT /api/v1/media/{storageKey}。 */
export async function putMedia(storageKey: string, blob: Blob, signal?: AbortSignal): Promise<MediaObject> {
    const response = await mediaFetch(mediaUrl(storageKey), { method: "PUT", body: blob, headers: { "Content-Type": blob.type || "application/octet-stream" }, signal }, true);
    return (await response.json()) as MediaObject;
}

/** 删除服务端媒体。DELETE /api/v1/media/{storageKey}。 */
export async function deleteMedia(storageKey: string) {
    await mediaFetch(mediaUrl(storageKey), { method: "DELETE" }, true);
}
