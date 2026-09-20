import { apiRequest } from "@/services/api/client";
import type { GenerationItem, GenerationListParams, GenerationListResult } from "@/services/data/types";

// 生成记录由服务端写入，前端只有读取与删除；status=pending 一次返回全部、不分页。
/** 游标分页查询生成记录（按类型/状态过滤）。GET /api/v1/generations。 */
export function listGenerations(params: GenerationListParams = {}, signal?: AbortSignal) {
    return apiRequest<GenerationListResult>("/generations", { query: params, signal });
}

/** 读取单条生成记录（含服务端保留的 result/config 原始结构）。GET /api/v1/generations/{id}。 */
export function getGeneration(id: string, signal?: AbortSignal) {
    return apiRequest<GenerationItem>(`/generations/${id}`, { signal });
}

/** 删除生成记录。DELETE /api/v1/generations/{id}。 */
export function deleteGeneration(id: string) {
    return apiRequest<void>(`/generations/${id}`, { method: "DELETE" });
}
