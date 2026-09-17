// Runtime configuration access layer.
// Priority: window.__RUNTIME_CONFIG__ (injected by the container entrypoint) > build-time VITE_ variables > defaults.
// This supports both configuring the same image with docker run -e and injecting values during custom builds.
//
// Each analytics provider has its own variable; configured providers are enabled independently and all are disabled by default.
// Only GA4 and Baidu are supported. Both accept IDs only, and script URLs are assembled in code without arbitrary scripts or inline JavaScript.

type RuntimeConfig = {
    ANALYTICS_GA4_ID?: string; // GA4 measurement ID (G-XXXX)
    ANALYTICS_BAIDU_ID?: string; // Baidu Analytics site ID
    API_BASE_URL?: string; // API origin for cross-origin deployments; empty means same-origin /api
    ADMIN_BASE_URL?: string; // Admin console origin; empty hides the admin entry in the user menu
    SITE_ENV?: string; // production | test | development
};

declare global {
    interface Window {
        __RUNTIME_CONFIG__?: RuntimeConfig;
    }
}

const runtime: RuntimeConfig = (typeof window !== "undefined" && window.__RUNTIME_CONFIG__) || {};

function read(key: keyof RuntimeConfig, buildTime: string | undefined, fallback = ""): string {
    const value = runtime[key];
    if (typeof value === "string" && value.trim()) return value.trim();
    if (typeof buildTime === "string" && buildTime.trim()) return buildTime.trim();
    return fallback;
}

export const ANALYTICS_GA4_ID = read("ANALYTICS_GA4_ID", import.meta.env.VITE_ANALYTICS_GA4_ID);
export const ANALYTICS_BAIDU_ID = read("ANALYTICS_BAIDU_ID", import.meta.env.VITE_ANALYTICS_BAIDU_ID);
export const API_BASE_URL = read("API_BASE_URL", import.meta.env.VITE_API_BASE_URL);

// 管理后台的对外地址（主站与后台是独立域名，后台入口必须用绝对地址）。
// 只从运行期配置读：它必须和实际部署的后台域名一致，不做构建期 VITE_ 兜底。
// 为空表示当前环境没有独立后台，用户菜单里直接隐藏该入口。
export const ADMIN_BASE_URL = read("ADMIN_BASE_URL", undefined);

// 部署环境标识：只有 production 算正式环境；test / development 或任何自定义值都会在界面顶栏显示标识，
// 避免在测试站上误当成正式环境操作。
export const SITE_ENV = read("SITE_ENV", import.meta.env.VITE_SITE_ENV, "development");
export const IS_PRODUCTION_SITE = SITE_ENV === "production";
