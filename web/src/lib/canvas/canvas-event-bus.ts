import localforage from "localforage";

import type { PluginStorage } from "@/types/canvas-plugin";

// Lightweight canvas event bus for communication between nodes and plugins.
type Handler = (payload: unknown) => void;
const handlers = new Map<string, Set<Handler>>();

/** 广播事件；某个 handler 抛错只记日志，不影响其它订阅者。 */
export function emitCanvasEvent(event: string, payload?: unknown) {
    handlers.get(event)?.forEach((handler) => {
        try {
            handler(payload);
        } catch (error) {
            console.error(`[canvas-event] handler for "${event}" failed`, error);
        }
    });
}

/** 订阅事件，返回取消订阅函数；同一 handler 重复订阅只登记一次（Set 去重）。 */
export function onCanvasEvent(event: string, handler: Handler) {
    let set = handlers.get(event);
    if (!set) {
        set = new Set();
        handlers.set(event, set);
    }
    set.add(handler);
    return () => set!.delete(handler);
}

// Private plugin storage isolated by pluginId namespace.
const stores = new Map<string, LocalForage>();

export function createPluginStorage(pluginId: string): PluginStorage {
    let store = stores.get(pluginId);
    if (!store) {
        store = localforage.createInstance({ name: "infinite-canvas-plugins", storeName: pluginId });
        stores.set(pluginId, store);
    }
    return {
        get: (key) => store!.getItem(key),
        set: async (key, value) => {
            await store!.setItem(key, value);
        },
        remove: async (key) => {
            await store!.removeItem(key);
        },
    };
}
