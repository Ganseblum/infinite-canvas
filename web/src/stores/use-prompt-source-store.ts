import { create } from "zustand";
import { persist } from "zustand/middleware";

import { DEFAULT_PROMPT_SOURCES, createPromptSource, type PromptSource } from "@/services/api/prompt-source-presets";

/** 自动刷新调度配置：intervalMinutes 为 0 表示关闭定时刷新，lastFetchedAt 记录最近一轮成功时间。 */
export type PromptSourceSchedule = {
    intervalMinutes: number;
    lastFetchedAt: string;
};

const PROMPT_SOURCE_STORE_KEY = "infinite-canvas:prompt_source_store_v2";

const defaultSchedule: PromptSourceSchedule = {
    intervalMinutes: 30,
    lastFetchedAt: "",
};

/** 设置页可选的自动刷新间隔（分钟），0 为手动。 */
export const PROMPT_SOURCE_INTERVALS = [0, 30, 60, 360, 1440];

type PromptSourceStore = {
    sources: PromptSource[];
    schedule: PromptSourceSchedule;
    addSource: () => PromptSource;
    saveSource: (source: PromptSource) => void;
    removeSource: (id: string) => void;
    toggleSource: (id: string, enabled: boolean) => void;
    updateSchedule: <K extends keyof PromptSourceSchedule>(key: K, value: PromptSourceSchedule[K]) => void;
};

/**
 * 提示词源 store：管用户订阅的提示词源列表与自动刷新调度，
 * 供设置页编辑、use-prompt-source-scheduler 定时刷新、prompts 页按源分类。
 * persist 到 localStorage（键见 PROMPT_SOURCE_STORE_KEY），只持久化 sources 与 schedule。
 */
export const usePromptSourceStore = create<PromptSourceStore>()(
    persist(
        (set) => ({
            sources: DEFAULT_PROMPT_SOURCES,
            schedule: defaultSchedule,
            addSource: () => createPromptSource(),
            saveSource: (source) =>
                set((state) => ({
                    sources: state.sources.some((item) => item.id === source.id)
                        ? state.sources.map((item) => (item.id === source.id && !item.builtIn ? createPromptSource(source) : item))
                        : [...state.sources, createPromptSource(source)],
                })),
            removeSource: (id) => set((state) => ({ sources: state.sources.filter((item) => item.id !== id || item.builtIn) })),
            toggleSource: (id, enabled) => set((state) => ({ sources: state.sources.map((item) => (item.id === id ? { ...item, enabled } : item)) })),
            updateSchedule: (key, value) => set((state) => ({ schedule: { ...state.schedule, [key]: value } })),
        }),
        {
            name: PROMPT_SOURCE_STORE_KEY,
            partialize: (state) => ({ sources: state.sources, schedule: state.schedule }),
            // 合并策略：内置源始终以最新预设为准（只保留用户改过的 enabled 开关），
            // 自定义源按当前数据结构归一化，避免旧持久化里字段缺失导致脏数据。
            merge: (persisted, current) => {
                const persistedState = (persisted || {}) as Partial<PromptSourceStore>;
                const savedSources = Array.isArray(persistedState.sources) ? persistedState.sources : [];
                const enabledById = new Map(savedSources.map((source) => [source.id, source.enabled]));
                const builtIn = DEFAULT_PROMPT_SOURCES.map((source) => ({ ...source, enabled: enabledById.get(source.id) ?? source.enabled }));
                const custom = savedSources.filter((source) => !source.builtIn).map((source) => createPromptSource(source));
                return { ...current, sources: [...builtIn, ...custom], schedule: { ...defaultSchedule, ...(persistedState.schedule || {}) } };
            },
        },
    ),
);
