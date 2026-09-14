import type { ModelConstraintOption, ModelParameterValue } from "@/services/api/catalog";

export function formatConstraintValue(value: ModelConstraintOption): string {
    if (typeof value === "object" && value !== null) return String(value.label ?? value.value);
    return String(value);
}

export function formatPriceParams(params: Record<string, ModelParameterValue>, labels: Record<string, string>): string {
    return Object.entries(params)
        .map(([key, value]) => `${labels[key] ?? key} ${String(value)}`)
        .join(" · ");
}
