// 构建前的 import 边界断言：admin/src 只允许引用白名单里的 web 模块。
//
// 路径别名本身不构成边界——@/* 指向 web/src 之后，admin 里任何文件都能 import web 的任何模块，
// 而 TypeScript 与 Vite 都不会因此报错。所以边界只能由这份断言守住，挂在 package.json 的 prebuild 上。
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { WEB_WHITELIST } from "./web-whitelist.mjs";

const adminDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = join(adminDir, "src");
// 白名单里写带扩展名的真实路径（Tailwind 注入 @source 时要用），
// 而 import 语句可能不带扩展名、目录入口还可能省掉 index：三种写法都算命中。
const allowed = new Set(
    WEB_WHITELIST.flatMap((path) => [path, path.replace(/\.(ts|tsx|css)$/, ""), path.replace(/\/index\.(ts|tsx)$/, "")]),
);
// 覆盖 from "x"、import "x"、import("x") 三种写法。
const SPECIFIER = /(?:from|import)\s*\(?\s*["']([^"']+)["']/g;

function* sourceFiles(dir) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const full = join(dir, entry.name);
        if (entry.isDirectory()) yield* sourceFiles(full);
        else if (/\.(ts|tsx)$/.test(entry.name)) yield full;
    }
}

const violations = [];
let scanned = 0;
for (const file of sourceFiles(srcDir)) {
    scanned += 1;
    const lines = readFileSync(file, "utf8").split("\n");
    lines.forEach((line, index) => {
        for (const [, specifier] of line.matchAll(SPECIFIER)) {
            const at = { file: relative(adminDir, file), line: index + 1, specifier };
            if (specifier.startsWith("@/")) {
                if (!allowed.has(specifier.slice(2))) violations.push({ ...at, reason: "不在 web 白名单里" });
            } else if (specifier.startsWith("@admin/")) {
                if (relative(srcDir, resolve(srcDir, specifier.slice("@admin/".length))).startsWith("..")) violations.push({ ...at, reason: "越出 admin/src" });
            } else if (specifier.startsWith(".")) {
                if (relative(srcDir, resolve(dirname(file), specifier)).startsWith("..")) violations.push({ ...at, reason: "越出 admin/src" });
            }
            // 其余是裸包名，由 package.json 约束，这里不管。
        }
    });
}

if (violations.length > 0) {
    console.error(`admin import 边界检查失败：@/ 只能指向白名单里的 web 模块（admin/scripts/web-whitelist.mjs）。`);
    for (const { file, line, specifier, reason } of violations) console.error(`  ${file}:${line}  ${specifier}  (${reason})`);
    process.exit(1);
}
console.log(`admin import 边界检查通过：扫描 ${scanned} 个文件，白名单 ${WEB_WHITELIST.length} 个路径。`);
