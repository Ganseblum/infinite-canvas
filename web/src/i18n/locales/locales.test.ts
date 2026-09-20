// 语言包一致性测试：校验中英包 key 集合完全对称、顶层无重复 key。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import enUS from "./en-US";
import zhCN from "./zh-CN";

function flattenKeys(value: unknown, prefix = ""): string[] {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return [prefix];
    return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) => flattenKeys(child, prefix ? `${prefix}.${key}` : key));
}

function topLevelKeys(source: string) {
    return [...source.matchAll(/^ {4}([A-Za-z_$][\w$]*):/gm)].map((match) => match[1]);
}

describe("i18n 语言包", () => {
    it("zh-CN 与 en-US 的 key 集合完全对称", () => {
        const zhKeys = flattenKeys(zhCN);
        const enKeys = flattenKeys(enUS);
        expect(zhKeys.filter((key) => !enKeys.includes(key))).toEqual([]);
        expect(enKeys.filter((key) => !zhKeys.includes(key))).toEqual([]);
    });

    it("语言包顶层没有重复 key", () => {
        for (const file of ["zh-CN.ts", "en-US.ts"]) {
            const source = readFileSync(new URL(file, import.meta.url), "utf8");
            const keys = topLevelKeys(source);
            const duplicates = keys.filter((key, index) => keys.indexOf(key) !== index);
            expect(duplicates, `${file} 顶层存在重复 key`).toEqual([]);
        }
    });
});
