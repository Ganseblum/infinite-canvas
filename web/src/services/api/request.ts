// API 查询参数的小工具：值类型受控，避免拼 URL 时出现 "undefined" 字符串。
/** 支持的查询参数值类型：undefined/空串/空数组会在序列化时被剔除。 */
export type ApiParams = Record<string, string | string[] | number | number[] | undefined>;

/** 剔除空串、undefined 与空数组，用于构造干净的请求参数对象。 */
export function compactApiParams(params: ApiParams) {
    return Object.fromEntries(Object.entries(params).filter(([, value]) => value !== "" && value !== undefined && (!Array.isArray(value) || value.length > 0))) as ApiParams;
}

/** 序列化成 URLSearchParams：数组按重复键展开，undefined 直接跳过。 */
export function serializeApiParams(params?: ApiParams) {
    const queryParams = new URLSearchParams();
    for (const [key, value] of Object.entries(params || {})) {
        if (value === undefined) continue;
        if (Array.isArray(value)) value.forEach((item) => queryParams.append(key, String(item)));
        else queryParams.set(key, String(value));
    }
    return queryParams;
}
