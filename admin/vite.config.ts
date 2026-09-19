import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

import { WEB_SRC_RELATIVE, WEB_WHITELIST } from "./scripts/web-whitelist.mjs";

const adminDir = dirname(fileURLToPath(import.meta.url));
const webSrcDir = resolve(adminDir, WEB_SRC_RELATIVE);
const globalsCss = resolve(adminDir, "src/styles/globals.css");

// import 边界与 Tailwind 扫描范围共用 admin/scripts/web-whitelist.mjs 这一份清单。
// admin 与 web 是兄弟目录，Tailwind 不会自动扫到 web/src 的类名，漏扫是静默失败（构建成功、样式丢失），
// 所以按白名单显式注入 @source；白名单加一个路径，扫描范围同时跟着变。
function webSourceBoundary(): Plugin {
    return {
        name: "admin-web-source-boundary",
        // 必须在 vite:css（内部跑 PostCSS/Tailwind）之前改写源码，所以是 pre。
        enforce: "pre",
        transform(code, id) {
            if (id.split("?")[0] !== globalsCss) return null;
            const sources = WEB_WHITELIST.map((path) => `@source ${JSON.stringify(relative(dirname(globalsCss), resolve(webSrcDir, path)))};`);
            return { code: `${code}\n${sources.join("\n")}\n` };
        },
    };
}

export default defineConfig({
    plugins: [webSourceBoundary(), react()],
    resolve: {
        alias: {
            // @/* 指向 web/src，是为了让复用的外壳文件内部的 @/ 引用原样工作。
            // 它是别名、不是边界：边界由 scripts/check-import-boundary.mjs 在构建前断言。
            "@": webSrcDir,
            // admin 自己的代码用 @admin/*，与 web 的 @/* 分开，避免同名路径（如 services/api/admin）互相顶掉。
            "@admin": resolve(adminDir, "src"),
        },
        // admin 与 web 各有一份 node_modules，不 dedupe 会把 React/antd 打成两份（实测产物里
        // "Minified React error" 出现两次），运行时会出现重复 context、hook 报错这类问题。
        dedupe: ["react", "react-dom", "antd", "@ant-design/icons", "react-i18next", "i18next", "zustand"],
    },
    server: {
        proxy: {
            // admin 跑在 localhost:5174、API 在 127.0.0.1:8080，属于跨站：Secure cookie 在 http 下写不进浏览器，
            // 登录会「看起来不工作」。保留同源 /api 代理是本地开发的唯一正确形态，不要改成直连 8080。
            "/api": {
                target: "http://127.0.0.1:8090",
                changeOrigin: true,
            },
        },
    },
});
