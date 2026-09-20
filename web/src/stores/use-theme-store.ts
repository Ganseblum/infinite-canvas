import { create } from "zustand";
import { persist } from "zustand/middleware";

/** 界面主题名：浅色 / 深色，画布与全局界面共用。 */
export type ThemeName = "light" | "dark";

type ThemeStore = {
    theme: ThemeName;
    setTheme: (theme: ThemeName) => void;
};

/**
 * 全局主题 store：只管 light/dark 一个开关，顶层布局与画布页据此切换主题。
 * 通过 zustand persist 持久化到 localStorage（键 infinite-canvas:theme_store）。
 */
export const useThemeStore = create<ThemeStore>()(
    persist(
        (set) => ({
            theme: "light",
            setTheme: (theme) => set({ theme }),
        }),
        { name: "infinite-canvas:theme_store" },
    ),
);
