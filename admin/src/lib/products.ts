import { API_BASE_URL } from "@/constant/runtime-config";

// 多产品共用后台的唯一产品清单：以后加产品只改这份注册表（名称等文案进 admin i18n 的
// products.items，key 与这里的 key 对应），切换器与占位页都会自动跟着长出来。
//
// status 含义：
//   connected —— 已接入统一后台，后台接口就是当前 admin 应用调用的这套 /api/admin/*；
//   planned   —— 仅有规划，切换器里可点，但只落到占位页，不渲染任何假的管理功能。
export type AdminProductStatus = "connected" | "planned";

export type AdminProduct = {
    key: string;
    // 产品显示名的 i18n key（admin 命名空间），文案不写死在注册表里。
    nameKey: string;
    status: AdminProductStatus;
    // 产品自己的后端 API 基址。已接入产品复用现有运行时 API_BASE_URL；
    // 规划中产品留空，等后端实现同一套 /api/admin/* 契约后再填。
    apiBase: string;
    // 规划中产品的接入条件说明（i18n key，admin 命名空间），占位页展示用。
    noteKey?: string;
};

export const CURRENT_PRODUCT_KEY = "youc-canvas";

// nameKey / noteKey 都是 admin 命名空间内相对根的 key（与 envBadge.* 等既有键同一写法），
// 渲染时统一 t(key, { ns: "admin" })。
export const ADMIN_PRODUCTS: AdminProduct[] = [
    {
        key: "youc-canvas",
        nameKey: "products.items.youc-canvas.name",
        status: "connected",
        apiBase: API_BASE_URL,
    },
    {
        key: "blog",
        nameKey: "products.items.blog.name",
        status: "planned",
        apiBase: "",
        noteKey: "products.integrationRequirement",
    },
    {
        key: "office",
        nameKey: "products.items.office.name",
        status: "planned",
        apiBase: "",
        noteKey: "products.integrationRequirement",
    },
];

export function getAdminProduct(key: string | undefined): AdminProduct | undefined {
    return ADMIN_PRODUCTS.find((product) => product.key === key);
}
