import type { ModelConstraintOption } from "@/services/api/catalog";
import { catalogModel } from "@/stores/use-model-catalog-store";

export type ConstraintOption = { value: string; label: string };

export function constraintOptions(values?: ModelConstraintOption[]): ConstraintOption[] {
    return (values || []).map((item) => (item !== null && typeof item === "object" ? { value: String(item.value), label: item.label || String(item.value) } : { value: String(item), label: String(item) }));
}

export function constraintValues(values?: ModelConstraintOption[]) {
    return (values || []).map((item) => String(item !== null && typeof item === "object" ? item.value : item));
}

/** 当前取值不在约束里时回落到第一个合法值。 */
export function pickConstraintValue(values: ModelConstraintOption[] | undefined, current: string, fallback = "") {
    const options = constraintValues(values);
    if (options.includes(current)) return current;
    return options[0] ?? fallback;
}

export function modelSupportsFeature(model: string | undefined, feature: string) {
    const features = catalogModel(model)?.constraints?.features || [];
    return features.includes(feature);
}

export function modelMaxCount(model: string | undefined, fallback = 15) {
    const max = catalogModel(model)?.constraints?.n?.max;
    return max && max > 0 ? max : fallback;
}
