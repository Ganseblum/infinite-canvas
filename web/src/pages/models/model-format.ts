import type { ModelConstraintOption, ModelParameterValue } from "@/services/api/catalog";

/**
 * 把模型目录里的约束选项格式化成可直接展示的字符串。
 * 约束值可能是原始标量，也可能是带 label 的对象，统一兜底转成字符串。
 * @param value 目录接口返回的单个约束选项（标量或 {label, value} 对象）
 */
export function formatConstraintValue(value: ModelConstraintOption): string {
    if (typeof value === "object" && value !== null) return String(value.label ?? value.value);
    return String(value);
}

/**
 * 把模型当前选中的定价参数拼成一行摘要文本，用于卡片上展示。
 * @param params 参数名到取值的映射
 * @param labels 参数名的展示名映射，缺失时回退用原始参数名
 * @returns 形如「分辨率 1024x1024 · 质量 high」的单行文本
 */
export function formatPriceParams(params: Record<string, ModelParameterValue>, labels: Record<string, string>): string {
    return Object.entries(params)
        .map(([key, value]) => `${labels[key] ?? key} ${String(value)}`)
        .join(" · ");
}
