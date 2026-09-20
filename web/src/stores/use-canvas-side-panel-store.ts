import { create } from "zustand";

// 侧栏展开/收起动效时长：closePanel 延迟等动画播完才卸载 DOM。
export const CANVAS_SIDE_PANEL_MOTION_MS = 500;
export const CANVAS_SIDE_PANEL_MIN_WIDTH = 220;
export const CANVAS_SIDE_PANEL_MAX_WIDTH = 480;
export const CANVAS_SIDE_PANEL_DEFAULT_WIDTH = 280;

// 侧栏宽度与开合状态直接存 localStorage：配置值极小，不值得引入持久化中间件。
const WIDTH_KEY = "canvas-side-panel-width";
const OPEN_KEY = "canvas-side-panel-open";

// 初始宽度：读取历史值并钳制到 [MIN, MAX]，防止旧数据超界。

function initialWidth() {
    if (typeof window === "undefined") return CANVAS_SIDE_PANEL_DEFAULT_WIDTH;
    const stored = Number(localStorage.getItem(WIDTH_KEY));
    if (!stored) return CANVAS_SIDE_PANEL_DEFAULT_WIDTH;
    return Math.min(CANVAS_SIDE_PANEL_MAX_WIDTH, Math.max(CANVAS_SIDE_PANEL_MIN_WIDTH, stored));
}

function initialOpen() {
    if (typeof window === "undefined") return true;
    return localStorage.getItem(OPEN_KEY) !== "0";
}

type CanvasSidePanelStore = {
    width: number;
    panelOpen: boolean;
    panelMounted: boolean;
    panelClosing: boolean;
    setWidth: (width: number) => void;
    openPanel: () => void;
    closePanel: () => void;
    togglePanel: () => void;
};

/**
 * 画布左侧面板 store：管面板宽度与开合状态（SSR 安全的 localStorage 初始化）。
 * panelMounted/panelClosing 配合收起动画：先播 500ms 动效再卸载节点，避免动画被截断。
 */
export const useCanvasSidePanelStore = create<CanvasSidePanelStore>((set, get) => ({
    width: initialWidth(),
    panelOpen: initialOpen(),
    panelMounted: initialOpen(),
    panelClosing: false,
    setWidth: (width) => set({ width }),
    openPanel: () => {
        if (typeof window !== "undefined") localStorage.setItem(OPEN_KEY, "1");
        set({ panelOpen: true, panelMounted: true, panelClosing: false });
    },
    closePanel: () => {
        if (!get().panelMounted || get().panelClosing) return;
        if (typeof window !== "undefined") localStorage.setItem(OPEN_KEY, "0");
        set({ panelOpen: false, panelClosing: true });
        // 动画播完才真正卸载面板；期间再点开会被 panelClosing 拦截后走 openPanel 恢复。
        setTimeout(() => {
            if (get().panelClosing) set({ panelMounted: false, panelClosing: false });
        }, CANVAS_SIDE_PANEL_MOTION_MS);
    },
    togglePanel: () => (get().panelOpen ? get().closePanel() : get().openPanel()),
}));
