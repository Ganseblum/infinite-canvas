// 构建期注入的常量与环境变量入口：版本号走 vite define，其余走 VITE_ 前缀环境变量，
// 运行期可覆盖的配置见 constant/runtime-config.ts。

/** 应用版本号（vite define 注入 __APP_VERSION__），开发环境显示 dev。 */
export const APP_VERSION = __APP_VERSION__ || "dev";

/** 文档站地址。 */
export const DOCS_URL = import.meta.env.VITE_DOC_URL || "https://docs.canvas.best";

// Official plugin registry URL: CI publishes to plugins-dist for jsDelivr delivery; an environment variable may override it for self-hosting.
export const PLUGIN_REGISTRY_URL = import.meta.env.VITE_PLUGIN_REGISTRY_URL || "https://cdn.jsdelivr.net/gh/basketikun/infinite-canvas@plugins-dist/official-plugins.json";
