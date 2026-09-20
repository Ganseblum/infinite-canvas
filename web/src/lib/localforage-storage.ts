import localforage from "localforage";
import type { StateStorage } from "zustand/middleware";

// zustand persist 的 localforage 适配层：业务数据量大，localStorage 只作读写失败时的降级。
localforage.config({
    name: "infinite-canvas",
    storeName: "app_state",
});

/** 供 zustand persist 使用的异步 storage：优先 IndexedDB（localforage），异常时退回 localStorage。 */
export const localForageStorage: StateStorage = {
    getItem: async (name) => {
        if (typeof window === "undefined") return null;
        try {
            return (await localforage.getItem<string>(name)) || null;
        } catch {
            return window.localStorage.getItem(name);
        }
    },
    setItem: async (name, value) => {
        if (typeof window === "undefined") return;
        try {
            await localforage.setItem(name, value);
        } catch {
            window.localStorage.setItem(name, value);
        }
    },
    removeItem: async (name) => {
        if (typeof window === "undefined") return;
        try {
            await localforage.removeItem(name);
        } catch {
            window.localStorage.removeItem(name);
        }
    },
};
