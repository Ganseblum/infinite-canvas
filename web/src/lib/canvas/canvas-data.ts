import type { CanvasData } from "@/services/data/types";

// 服务端只保管画布 data，缺省字段由前端在这里补齐，避免每个读取点各写一遍兜底。
export function normalizeCanvasData(data?: Partial<CanvasData> | null): CanvasData {
    return {
        nodes: data?.nodes || [],
        connections: data?.connections || [],
        chatSessions: data?.chatSessions || [],
        activeChatId: data?.activeChatId ?? null,
        backgroundMode: data?.backgroundMode || "lines",
        showImageInfo: data?.showImageInfo || false,
        viewport: data?.viewport || { x: 0, y: 0, k: 1 },
    };
}
