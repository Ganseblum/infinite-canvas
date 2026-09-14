import { apiRequest } from "@/services/api/client";
import type { CanvasData, CanvasDetail, CanvasListParams, CanvasListResult, CanvasPatchResult, CanvasUpdateResult } from "@/services/data/types";

// 画布资源客户端：只负责发请求与类型转换，不含缓存与业务判断。
export function listCanvases(params: CanvasListParams = {}, signal?: AbortSignal) {
    return apiRequest<CanvasListResult>("/canvases", { query: params, signal });
}

export function createCanvas(payload: { title: string; data?: Partial<CanvasData> }) {
    return apiRequest<CanvasDetail>("/canvases", { method: "POST", body: payload });
}

export function getCanvas(id: string, signal?: AbortSignal) {
    return apiRequest<CanvasDetail>(`/canvases/${id}`, { signal });
}

// 整体覆盖 data 并带 revision 乐观锁，冲突时后端返回 409 REVISION_CONFLICT。
export function updateCanvas(id: string, payload: { data: CanvasData; revision: number }) {
    return apiRequest<CanvasUpdateResult>(`/canvases/${id}`, { method: "PUT", body: payload });
}

// 只改标题，不递增 revision。
export function patchCanvas(id: string, title: string) {
    return apiRequest<CanvasPatchResult>(`/canvases/${id}`, { method: "PATCH", body: { title } });
}

export function deleteCanvas(id: string) {
    return apiRequest<void>(`/canvases/${id}`, { method: "DELETE" });
}
