import { useEffect, useMemo } from "react";

import type { CatalogModel, ModelCapability } from "@/services/api/catalog";
import { useAuthStore } from "@/stores/use-auth-store";
import { useModelCatalogStore } from "@/stores/use-model-catalog-store";

export function useModelCatalog() {
    const isAuthenticated = useAuthStore((state) => state.status === "authenticated");
    const items = useModelCatalogStore((state) => state.items);
    const status = useModelCatalogStore((state) => state.status);
    const error = useModelCatalogStore((state) => state.error);
    const load = useModelCatalogStore((state) => state.load);

    useEffect(() => {
        if (isAuthenticated && status === "idle") void load();
    }, [isAuthenticated, load, status]);

    return { items, status, error, isLoaded: status === "ready", reload: load };
}

export function useModelOptions(capability?: ModelCapability): CatalogModel[] {
    const { items } = useModelCatalog();
    return useMemo(() => (capability ? items.filter((item) => item.capability === capability) : items), [items, capability]);
}

export function useModelConstraints(model: string | undefined) {
    const { items } = useModelCatalog();
    return useMemo(() => {
        const id = (model || "").trim();
        return id ? items.find((item) => item.id === id)?.constraints : undefined;
    }, [items, model]);
}

/** 当前取值不在目录里时回落到该能力的第一个模型。 */
export function useResolvedModel(value: string | undefined, capability: ModelCapability) {
    const { items } = useModelCatalog();
    return useMemo(() => {
        const options = items.filter((item) => item.capability === capability);
        const current = (value || "").trim();
        return options.some((item) => item.id === current) ? current : options[0]?.id || "";
    }, [items, value, capability]);
}

/** 目录项上的预估点数：优先取历史最低的有效价，其次退回价格矩阵第一项。 */
export function estimateModelPoints(model: CatalogModel) {
    const prices = model.effectivePrices || [];
    if (prices.length) return Math.min(...prices.map((item) => item.finalCostMicros));
    return model.creditCost?.prices?.[0]?.costMicros;
}
