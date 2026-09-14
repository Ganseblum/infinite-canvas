import { apiRequest } from "@/services/api/client";
import type { AssetItem, AssetKind, AssetListParams, AssetListResult } from "@/services/data/types";

// 素材资源客户端：列表分页、筛选与标签全集都由服务端完成，第一页响应带 tags。
export function listAssets(params: AssetListParams = {}, signal?: AbortSignal) {
    return apiRequest<AssetListResult>("/assets", { query: params, signal });
}

export function createAsset(payload: { kind: AssetKind; title: string; tags?: string[]; data: Record<string, unknown>; storageKey?: string; bytes?: number }) {
    return apiRequest<AssetItem>("/assets", { method: "POST", body: payload });
}

export function getAsset(id: string, signal?: AbortSignal) {
    return apiRequest<AssetItem>(`/assets/${id}`, { signal });
}

export function patchAsset(id: string, patch: { title?: string; tags?: string[]; data?: Record<string, unknown>; storageKey?: string; bytes?: number }) {
    return apiRequest<AssetItem>(`/assets/${id}`, { method: "PATCH", body: patch });
}

export function deleteAsset(id: string) {
    return apiRequest<void>(`/assets/${id}`, { method: "DELETE" });
}
