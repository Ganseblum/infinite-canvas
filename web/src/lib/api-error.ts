import i18n from "@/i18n";

// 后端失败统一响应 `{ error: { code, message, ...data } }` 的前端映射。
// 后续各期新增带数据的错误码时只回填这里的可选字段，调用方永远只认 ApiError 一种形状。
export type ApiErrorInit = {
    code: string;
    message?: string;
    status?: number;
    fields?: Record<string, string>;
    revision?: number;
    retryAfter?: number;
    requiredMicros?: number;
    shortfallMicros?: number;
    param?: string;
    allowed?: unknown;
    upstreamStatus?: number;
    phase?: string;
};

/**
 * 前端统一的接口错误类型：任何请求失败都抛它，调用方按 code 分支处理，
 * 展示文案走 getApiErrorMessage 三级解析。
 */
export class ApiError extends Error {
    readonly code: string;
    readonly status: number;
    readonly fields?: Record<string, string>;
    readonly revision?: number;
    readonly retryAfter?: number;
    readonly requiredMicros?: number;
    readonly shortfallMicros?: number;
    readonly param?: string;
    readonly allowed?: unknown;
    readonly upstreamStatus?: number;
    readonly phase?: string;

    constructor(init: ApiErrorInit) {
        super(init.message || init.code);
        this.name = "ApiError";
        this.code = init.code;
        this.status = init.status ?? 0;
        this.fields = init.fields;
        this.revision = init.revision;
        this.retryAfter = init.retryAfter;
        this.requiredMicros = init.requiredMicros;
        this.shortfallMicros = init.shortfallMicros;
        this.param = init.param;
        this.allowed = init.allowed;
        this.upstreamStatus = init.upstreamStatus;
        this.phase = init.phase;
    }
}

// 后端错误码是 UPPER_SNAKE，apiErrors 文案按小驼峰一一对应。
export function apiErrorCodeToI18nKey(code: string) {
    return `apiErrors.${code.toLowerCase().replace(/_([a-z])/g, (_, char: string) => char.toUpperCase())}`;
}

// 点数的内部单位是微元，1 点 = 1 微元，展示时只加千分位、不再换算货币单位。
function formatAmount(micros?: number) {
    if (typeof micros !== "number" || !Number.isFinite(micros)) return undefined;
    return Math.round(micros).toLocaleString();
}

// 文案解析固定三级：apiErrors.<code> 本地化文案 → 后端 message → 通用兜底。
export function getApiErrorMessage(error: unknown): string {
    if (error instanceof ApiError) {
        // 429 带 Retry-After 时展示等待秒数；没有该响应头时退回不带秒数的文案。
        const key = error.code === "RATE_LIMITED" && !error.retryAfter ? "apiErrors.rateLimitedLater" : apiErrorCodeToI18nKey(error.code);
        if (i18n.exists(key)) {
            return i18n.t(key, {
                ...error.fields,
                param: error.param,
                allowed: Array.isArray(error.allowed) ? error.allowed.join(", ") : error.allowed,
                retryAfter: error.retryAfter,
                revision: error.revision,
                requiredMicros: error.requiredMicros,
                shortfallMicros: error.shortfallMicros,
                required: formatAmount(error.requiredMicros),
                shortfall: formatAmount(error.shortfallMicros),
            });
        }
        if (error.message) return error.message;
    }
    return i18n.t("apiErrors.operationFailed");
}
