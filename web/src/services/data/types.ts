import type { CanvasBackgroundMode } from "@/lib/canvas-theme";
import type { CanvasAssistantSession, CanvasConnection, CanvasNodeData, ViewportTransform } from "@/types/canvas";

// 服务端资源的数据契约类型：画布、素材、生成记录与媒体对象。
// 前端与服务端共用同一形状，字段语义见各资源客户端注释。

// 画布 data 与服务端同构：服务端只做体积与字段统计，不解析内部结构。
// 服务端可能在缺省 data 时存 `{}`，因此详情里的 data 一律按可选字段处理。
export type CanvasData = {
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    chatSessions: CanvasAssistantSession[];
    activeChatId: string | null;
    backgroundMode: CanvasBackgroundMode;
    showImageInfo: boolean;
    viewport: ViewportTransform;
};

/** 列表排序值：带 - 前缀表示倒序。 */
export type CanvasSort = "updatedAt" | "-updatedAt" | "createdAt" | "-createdAt" | "title" | "-title";

export type CanvasListParams = {
    page?: number;
    size?: number;
    q?: string;
    sort?: CanvasSort;
};

/** 画布列表项摘要：不含 data，取完整内容需再请求详情。 */
export type CanvasSummary = {
    id: string;
    title: string;
    nodeCount: number;
    connectionCount: number;
    coverKey: string;
    updatedAt: string;
};

export type CanvasDetail = CanvasSummary & {
    data: Partial<CanvasData> | null;
    revision: number;
    createdAt: string;
};

export type CanvasListResult = {
    items: CanvasSummary[];
    total: number;
    page: number;
    size: number;
};

export type CanvasUpdateResult = {
    revision: number;
    updatedAt: string;
};

export type CanvasPatchResult = {
    id: string;
    title: string;
    updatedAt: string;
};

export type AssetKind = "text" | "image" | "video";

/** 素材条目：媒体类素材的二进制在服务端（storageKey），文本内容放 data.content。 */
export type AssetItem = {
    id: string;
    kind: AssetKind;
    title: string;
    tags: string[];
    data: Record<string, unknown>;
    storageKey: string;
    bytes: number;
    createdAt: string;
    updatedAt: string;
};

export type AssetSort = CanvasSort;

export type AssetListParams = {
    page?: number;
    size?: number;
    q?: string;
    kind?: AssetKind;
    tag?: string[];
    sort?: AssetSort;
};

export type AssetListResult = {
    items: AssetItem[];
    total: number;
    page: number;
    size: number;
    // 只在第一页下发，调用方不能假设每页都有。
    tags?: string[];
};

export type GenerationKind = "image" | "video";
export type GenerationStatus = "pending" | "success" | "failed";

/** 服务端生成记录：由生成接口写入，前端只读（可删）。result/config 为服务端保留的原始结构。 */
export type GenerationItem = {
    id: string;
    kind: GenerationKind;
    status: GenerationStatus;
    prompt: string;
    model: string;
    config: Record<string, unknown> | null;
    result: Record<string, unknown> | null;
    durationMs: number;
    createdAt: string;
};

export type GenerationListParams = {
    kind?: GenerationKind;
    status?: GenerationStatus;
    cursor?: string;
    size?: number;
};

export type GenerationListResult = {
    items: GenerationItem[];
    nextCursor: string | null;
};

/** 服务端媒体对象元信息：storageKey 形如 "image:xxx"，是画布引用二进制的统一句柄。 */
export type MediaObject = {
    storageKey: string;
    bytes: number;
    checksum: string;
    mimeType: string;
};

export type MediaHead = {
    bytes: number;
    mimeType: string;
    checksum: string;
};
