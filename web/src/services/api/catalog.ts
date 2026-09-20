import { apiRequest } from "@/services/api/client";

// 平台模型目录接口客户端：浏览器不持有渠道与密钥，模型/约束/计价全部由服务端下发。

export type ModelCapability = "image" | "video" | "text" | "audio";

// 约束项可能是裸值（"1024x1024"）或带展示名的对象，消费方统一经 constraintOptions 归一化。
export type ModelConstraintOption = string | number | { value: string | number; label?: string };

/** 模型参数约束：size/ratio/resolution/quality/duration 为合法取值表，n 为单次张数上限。 */
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

/** 价格矩阵的一行：params 命中该参数组合时按 costMicros 计价。 */
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

/** 折扣后的实际价格行：baseCostMicros × 折扣 = finalCostMicros。 */
export type ModelEffectivePrice = {
    params: Record<string, ModelParameterValue>;
    baseCostMicros: number;
    finalCostMicros: number;
    discount?: ModelPriceDiscount | null;
};

/** 目录里的一个模型：含能力、约束、计价与免费试用标记。 */
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

/** 拉取模型目录，可按能力过滤。GET /api/v1/models。 */
export function listModels(params: { capability?: ModelCapability } = {}, signal?: AbortSignal) {
    return apiRequest<{ items: CatalogModel[] }>("/models", {
        query: { capability: params.capability },
        signal,
    });
}
