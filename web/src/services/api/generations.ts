import { apiRequest } from "@/services/api/client";
import type { GenerationItem, GenerationListParams, GenerationListResult } from "@/services/data/types";

// 生成记录由服务端写入，前端只有读取与删除；status=pending 一次返回全部、不分页。
export function listGenerations(params: GenerationListParams = {}, signal?: AbortSignal) {
    return apiRequest<GenerationListResult>("/generations", { query: params, signal });
}

export function getGeneration(id: string, signal?: AbortSignal) {
    return apiRequest<GenerationItem>(`/generations/${id}`, { signal });
}

export function deleteGeneration(id: string) {
    return apiRequest<void>(`/generations/${id}`, { method: "DELETE" });
}
