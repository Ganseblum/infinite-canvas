import type { CanvasBackgroundMode } from "@/lib/canvas-theme";
import type { CanvasAssistantSession, CanvasConnection, CanvasNodeData, ViewportTransform } from "@/types/canvas";

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

export type CanvasSort = "updatedAt" | "-updatedAt" | "createdAt" | "-createdAt" | "title" | "-title";

export type CanvasListParams = {
    page?: number;
    size?: number;
    q?: string;
    sort?: CanvasSort;
};

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
