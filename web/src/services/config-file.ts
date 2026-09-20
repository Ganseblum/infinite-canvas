import { saveAs } from "file-saver";

import i18n from "@/i18n";
import { useConfigStore, type AiConfig } from "@/stores/use-config-store";
import { usePromptSourceStore, type PromptSourceSchedule } from "@/stores/use-prompt-source-store";
import type { PromptSource } from "@/services/api/prompt-source-presets";

// 应用配置导入导出：把 AI 配置与提示词源打包成一个 JSON 文件，便于迁移与分享。

type AppConfigFile = {
    app: "infinite-canvas";
    version: 1;
    exportedAt: string;
    config: AiConfig;
    promptSources: {
        sources: PromptSource[];
        schedule: PromptSourceSchedule;
    };
};

/** 导出当前配置为 infinite-canvas-config.json 并触发下载。 */
export function exportAppConfig() {
    const { config } = useConfigStore.getState();
    const { sources, schedule } = usePromptSourceStore.getState();
    const data: AppConfigFile = { app: "infinite-canvas", version: 1, exportedAt: new Date().toISOString(), config, promptSources: { sources, schedule } };
    saveAs(new Blob([JSON.stringify(data, null, 2)], { type: "application/json;charset=utf-8" }), "infinite-canvas-config.json");
}

/** 导入配置文件：校验 app 标识与版本，只回填白名单字段（旧文件里的密钥/渠道被丢弃）。 */
export async function importAppConfig(file: File) {
    let data: AppConfigFile;
    try {
        data = JSON.parse(await file.text()) as AppConfigFile;
    } catch {
        throw new Error(i18n.t("config.invalidFile"));
    }
    if (data.app !== "infinite-canvas" || data.version !== 1 || !data.config || !data.promptSources) throw new Error(i18n.t("config.invalidFile"));
    // 只按白名单回填偏好，旧文件里可能残留的渠道与密钥字段会被丢弃。
    useConfigStore.getState().replaceConfig(data.config);
    usePromptSourceStore.setState(data.promptSources);
}
