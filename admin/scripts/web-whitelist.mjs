// admin 允许从 web/ 复用的模块白名单（路径相对 web/src）。
//
// 这是「唯一一份清单」，两处共用：
//   1. scripts/check-import-boundary.mjs —— 构建前断言 admin/src 里没有白名单外的 @/ 引用；
//   2. vite.config.ts —— 按同一份清单给 Tailwind 注入 @source，决定扫描范围。
// 改一处（加/删路径）两处一起生效，不会出现「import 通过了但样式没生成」这类静默失败。
export const WEB_SRC_RELATIVE = "../web/src";

export const WEB_WHITELIST = [
    // —— 架构门圈定的最小复用外壳 ——
    "services/api/client.ts", // 401 单飞刷新与请求重放，复制两份必然漂移
    "stores/use-auth-store.ts",
    "lib/app-theme.ts",
    "lib/api-error.ts",
    "constant/runtime-config.ts",
    "styles/globals.css",
    // —— 外壳的传递闭包 ——
    "i18n/index.ts", // api-error.ts 直接引用这个实例，admin 只能复用、不能再 init 一份
    "i18n/locales/zh-CN.ts",
    "i18n/locales/en-US.ts",
    "services/api/auth.ts", // client.ts / use-auth-store.ts 的 SessionPayload 与登录接口
    // —— 迁移管理页面时带过来的纯工具与类型 ——
    "stores/use-theme-store.ts", // 与 index.html 主题防闪烁脚本共用 infinite-canvas:theme_store 的键与结构
    "lib/credits-format.ts", // 页面里的点数/金额格式化，无依赖
    "lib/image-utils.ts", // 页面只用 formatBytes，但整份复用优于复制
    "types/image.ts", // image-utils.ts 的类型依赖
    "services/api/catalog.ts", // 后台服务与模型页只取其类型（import type，运行时不会加载）
];
