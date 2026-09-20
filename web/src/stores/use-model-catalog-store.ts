import { create } from "zustand";

import { listModels, type CatalogModel, type ModelCapability, type ModelConstraints } from "@/services/api/catalog";

// 平台模型目录是唯一的模型来源：浏览器不再持有渠道、Key 或 baseUrl。
type ModelCatalogState = {
    items: CatalogModel[];
    status: "idle" | "loading" | "ready" | "error";
    error?: unknown;
    load: () => Promise<void>;
};

/**
 * 平台模型目录 store：缓存 GET /api/v1/models 返回的模型清单与约束，
 * 工作台/画布/Agent 工具都从这里取模型选项；不持久化，登录后按需加载。
 */
export const useModelCatalogStore = create<ModelCatalogState>()((set, get) => ({
    items: [],
    status: "idle",
    load: async () => {
        if (get().status === "loading") return;
        set({ status: "loading", error: undefined });
        try {
            const { items } = await listModels();
            set({ items, status: "ready", error: undefined });
        } catch (error) {
            set({ status: "error", error });
        }
    },
}));

/** 按模型 id 查目录项；空值或未收录返回 undefined。 */
export function catalogModel(value: string | undefined) {
    const id = (value || "").trim();
    return id ? useModelCatalogStore.getState().items.find((item) => item.id === id) : undefined;
}

/** 模型的参数约束（尺寸/比例/时长/数量上限等），供表单钳制取值。 */
export function modelConstraints(value: string | undefined): ModelConstraints | undefined {
    return catalogModel(value)?.constraints;
}

/** 非 React 调用方（Agent 工具等）按需触发一次目录加载。 */
export function ensureModelCatalogLoaded() {
    const { status, load } = useModelCatalogStore.getState();
    if (status === "idle" || status === "error") void load();
}

export function catalogModelsFor(capability?: ModelCapability) {
    const items = useModelCatalogStore.getState().items;
    return capability ? items.filter((item) => item.capability === capability) : items;
}

/** 平台目录里某能力的模型 id 列表。 */
export function selectableModelsByCapability(capability?: ModelCapability) {
    return catalogModelsFor(capability).map((item) => item.id);
}

/** 当前取值不在目录里时回落到调用方给定的默认模型，最后回落到该能力的第一个模型。 */
export function resolveModelForCapability(currentModel: string | undefined, capability: ModelCapability, fallback?: string) {
    const options = catalogModelsFor(capability);
    const current = (currentModel || "").trim();
    if (options.some((item) => item.id === current)) return current;
    const preferred = (fallback || "").trim();
    if (options.some((item) => item.id === preferred)) return preferred;
    return options[0]?.id || "";
}

export function isModelCatalogReady(model?: string) {
    return Boolean((model || "").trim()) && useModelCatalogStore.getState().status === "ready";
}
