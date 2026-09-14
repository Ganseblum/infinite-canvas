import { apiRequest } from "@/services/api/client";

export type ModelCapability = "image" | "video" | "text" | "audio";

export type ModelConstraintOption = string | number | { value: string | number; label?: string };

export type ModelConstraints = {
    size?: ModelConstraintOption[];
    ratio?: ModelConstraintOption[];
    resolution?: ModelConstraintOption[];
    quality?: ModelConstraintOption[];
    duration?: ModelConstraintOption[];
    n?: { max?: number };
    features?: string[];
};

export type ModelParameterValue = string | number;

export type ModelPriceEntry = {
    params: Record<string, ModelParameterValue>;
    costMicros: number;
};

export type ModelCreditCost = {
    version?: number;
    dimensions?: string[];
    prices?: ModelPriceEntry[];
};

export type ModelPriceDiscount = {
    name: string;
    discountBps: number;
    endsAt: string;
};

export type ModelEffectivePrice = {
    params: Record<string, ModelParameterValue>;
    baseCostMicros: number;
    finalCostMicros: number;
    discount?: ModelPriceDiscount | null;
};

export type CatalogModel = {
    id: string;
    name: string;
    displayName: string;
    capability: ModelCapability;
    provider: string;
    constraints?: ModelConstraints;
    creditCost?: ModelCreditCost;
    effectivePrices?: ModelEffectivePrice[];
    nextPricingChangeAt?: string | null;
    freeTrialEligible?: boolean;
};

export function listModels(params: { capability?: ModelCapability } = {}, signal?: AbortSignal) {
    return apiRequest<{ items: CatalogModel[] }>("/models", {
        query: { capability: params.capability },
        signal,
    });
}
