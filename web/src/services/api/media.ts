import { ApiError } from "@/lib/api-error";
import { API_BASE_URL, refreshSession } from "@/services/api/client";
import type { MediaHead, MediaObject } from "@/services/data/types";
import { useAuthStore } from "@/stores/use-auth-store";

// 媒体地址就是可直接放进 <img src> 的同源 URL：GET/HEAD 用 ic_media cookie 鉴权，
// 地址上不带令牌，缓存交给浏览器。
export function mediaUrl(storageKey?: string) {
    return storageKey ? `${API_BASE_URL}/api/media/${storageKey}` : "";
}

// 写路径（PUT/DELETE）与导出取数走带 Bearer 的原始请求，不能沿用 client.ts 的 JSON 封装。
async function mediaFetch(storageKey: string, init: RequestInit, canRetry: boolean): Promise<Response> {
    const headers = new Headers(init.headers);
    const accessToken = useAuthStore.getState().accessToken;
    if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);

    let response: Response;
    try {
        response = await fetch(mediaUrl(storageKey), { ...init, headers, credentials: "include" });
    } catch (error) {
        if (init.signal?.aborted) throw error;
        throw new ApiError({ code: "NETWORK_ERROR", status: 0 });
    }
    if (response.ok) return response;

    const payload = await response.json().catch(() => null);
    const errorBody = payload && typeof payload === "object" && "error" in payload ? ((payload as { error: unknown }).error as Record<string, unknown>) : {};
    const code = typeof errorBody?.code === "string" ? errorBody.code : `HTTP_${response.status}`;
    if (code === "TOKEN_EXPIRED" && canRetry && (await refreshSession())) return mediaFetch(storageKey, init, false);
    throw new ApiError({ code, message: typeof errorBody?.message === "string" ? errorBody.message : "", status: response.status });
}

export async function headMedia(storageKey: string, signal?: AbortSignal): Promise<MediaHead | null> {
    const response = await mediaFetch(storageKey, { method: "HEAD", signal }, true).catch((error: unknown) => {
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

export async function getMediaBlob(storageKey: string, signal?: AbortSignal): Promise<Blob | null> {
    const response = await mediaFetch(storageKey, { method: "GET", signal }, true).catch((error: unknown) => {
        if (error instanceof ApiError && error.code === "NOT_FOUND") return null;
        throw error;
    });
    if (!response) return null;
    return response.blob();
}

export async function putMedia(storageKey: string, blob: Blob, signal?: AbortSignal): Promise<MediaObject> {
    const response = await mediaFetch(storageKey, { method: "PUT", body: blob, headers: { "Content-Type": blob.type || "application/octet-stream" }, signal }, true);
    return (await response.json()) as MediaObject;
}

export async function deleteMedia(storageKey: string) {
    await mediaFetch(storageKey, { method: "DELETE" }, true);
}
