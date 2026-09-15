import { useMemo } from "react";
import { create } from "zustand";
import { persist } from "zustand/middleware";

import { catalogModel } from "@/stores/use-model-catalog-store";

export type ModelCapability = "image" | "video" | "text" | "audio";
export type ReasoningEffort = "auto" | "low" | "medium" | "high" | "xhigh";

// 浏览器不再持有任何用户渠道与密钥；模型来源只有平台目录 /api/models。
export type AiConfig = {
    model: string;
    imageModel: string;
    videoModel: string;
    textModel: string;
    audioModel: string;
    audioVoice: string;
    audioFormat: string;
    audioSpeed: string;
    audioInstructions: string;
    videoSeconds: string;
    vquality: string;
    videoGenerateAudio: string;
    videoWatermark: string;
    videoMode: string;
    systemPrompt: string;
    reasoningEffort: ReasoningEffort;
    quality: string;
    size: string;
    background: string;
    count: string;
    canvasImageCount: string;
    proxyEnabled: boolean;
    proxyUrl: string;
};

export type ConfigTabKey = "local-proxy" | "preferences" | "prompt-sources" | "local-storage";

export const CONFIG_STORE_KEY = "infinite-canvas:ai_config_store";
export const LOCAL_PROXY_PACKAGE = "@basketikun/canvas-proxy";
export const DEFAULT_LOCAL_PROXY_URL = "http://127.0.0.1:23210";

export const defaultConfig: AiConfig = {
    model: "",
    imageModel: "",
    videoModel: "",
    textModel: "",
    audioModel: "",
    audioVoice: "alloy",
    audioFormat: "mp3",
    audioSpeed: "1",
    audioInstructions: "",
    videoSeconds: "6",
    vquality: "720",
    videoGenerateAudio: "true",
    videoWatermark: "false",
    videoMode: "frames",
    systemPrompt: "",
    reasoningEffort: "auto",
    quality: "auto",
    size: "auto",
    background: "",
    count: "1",
    canvasImageCount: "3",
    proxyEnabled: false,
    proxyUrl: DEFAULT_LOCAL_PROXY_URL,
};

type ConfigStore = {
    config: AiConfig;
    isConfigOpen: boolean;
    configTab: ConfigTabKey;
    shouldPromptContinue: boolean;
    updateConfig: <K extends keyof AiConfig>(key: K, value: AiConfig[K]) => void;
    replaceConfig: (config: Partial<AiConfig>) => void;
    openConfigDialog: (shouldPromptContinue?: boolean, tab?: ConfigTabKey) => void;
    setConfigDialogOpen: (isOpen: boolean) => void;
    clearPromptContinue: () => void;
};

export function boolConfig(value: string, fallback: boolean) {
    return value ? value === "true" : fallback;
}

export const useConfigStore = create<ConfigStore>()(
    persist(
        (set) => ({
            config: defaultConfig,
            isConfigOpen: false,
            configTab: "preferences",
            shouldPromptContinue: false,
            updateConfig: (key, value) =>
                set((state) => ({
                    config: {
                        ...state.config,
                        [key]: value,
                    },
                })),
            replaceConfig: (config) => set({ config: sanitizeConfig(config) }),
            openConfigDialog: (shouldPromptContinue = false, configTab = "preferences") => set({ isConfigOpen: true, shouldPromptContinue, configTab }),
            setConfigDialogOpen: (isConfigOpen) => set({ isConfigOpen }),
            clearPromptContinue: () => set({ shouldPromptContinue: false }),
        }),
        {
            name: CONFIG_STORE_KEY,
            version: 2,
            partialize: (state) => ({ config: state.config }),
            // 旧版本持久化里可能残留 baseUrl / apiKey / channels，重新水合时只保留白名单字段。
            migrate: (persisted) => ({ config: sanitizeConfig((persisted as { config?: Partial<AiConfig> } | undefined)?.config) }),
            merge: (persisted, current) => ({ ...current, config: sanitizeConfig((persisted as { config?: Partial<AiConfig> } | undefined)?.config) }),
        },
    ),
);

function sanitizeConfig(persisted: Partial<AiConfig> | undefined): AiConfig {
    const source = persisted || {};
    const config = { ...defaultConfig };
    (Object.keys(defaultConfig) as Array<keyof AiConfig>).forEach((key) => {
        const value = source[key];
        if (value === undefined || value === null) return;
        (config as Record<string, unknown>)[key] = value;
    });
    config.reasoningEffort = config.reasoningEffort || "auto";
    config.videoMode = config.videoMode === "reference" ? "reference" : "frames";
    config.proxyEnabled = Boolean(config.proxyEnabled);
    config.proxyUrl = config.proxyUrl || DEFAULT_LOCAL_PROXY_URL;
    return config;
}

export function useEffectiveConfig() {
    const config = useConfigStore((state) => state.config);
    return useMemo(() => config, [config]);
}

/** 模型展示名来自平台目录，目录缺失时退回原始 id。 */
export function modelOptionLabel(_config: AiConfig, value: string) {
    return catalogModel(value)?.displayName || value;
}

export function normalizeLocalProxyUrl(value: string) {
    const trimmed = value.trim().replace(/\/+$/, "");
    if (!trimmed) return "";
    return /^https?:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
}

/** Prefix an outgoing request with the local forwarding proxy so the browser is not blocked by CORS. */
export function withLocalProxy(url: string) {
    const { proxyEnabled, proxyUrl } = useConfigStore.getState().config;
    if (!proxyEnabled || !/^https?:\/\//i.test(url)) return url;
    const base = normalizeLocalProxyUrl(proxyUrl);
    if (!base || url.startsWith(`${base}/`)) return url;
    return `${base}/${url}`;
}
