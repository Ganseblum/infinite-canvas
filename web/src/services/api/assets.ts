import { apiRequest } from "@/services/api/client";
import type { AssetItem, AssetKind, AssetListParams, AssetListResult } from "@/services/data/types";

// 素材资源客户端：列表分页、筛选与标签全集都由服务端完成，第一页响应带 tags。
/** 分页查询素材（关键词/类型/标签/排序），第一页响应带标签全集。GET /api/v1/assets。 */
export function listAssets(params: AssetListParams = {}, signal?: AbortSignal) {
    return apiRequest<AssetListResult>("/assets", { query: params, signal });
}

/** 新建素材：媒体二进制应先经 media-ingest 上传取得 storageKey。POST /api/v1/assets。 */
export function createAsset(payload: { kind: AssetKind; title: string; tags?: string[]; data: Record<string, unknown>; storageKey?: string; bytes?: number; isAIGC?: boolean }) {
    return apiRequest<AssetItem>("/assets", { method: "POST", body: payload });
}

/** 读取单个素材。GET /api/v1/assets/{id}。 */
export function getAsset(id: string, signal?: AbortSignal) {
    return apiRequest<AssetItem>(`/assets/${id}`, { signal });
}

/** 局部更新标题/标签/data。PATCH /api/v1/assets/{id}。 */
export function patchAsset(id: string, patch: { title?: string; tags?: string[]; data?: Record<string, unknown>; storageKey?: string; bytes?: number }) {
    return apiRequest<AssetItem>(`/assets/${id}`, { method: "PATCH", body: patch });
}

/** 删除素材（不删除已上传的服务端媒体）。DELETE /api/v1/assets/{id}。 */
export function deleteAsset(id: string) {
    return apiRequest<void>(`/assets/${id}`, { method: "DELETE" });
}
