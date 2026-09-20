import type { ReactNode } from "react";
import { Brush, Camera, Copy, FileText, Grid2x2, Lock, LockOpen, Maximize2, Scissors, Sparkles, Upload, ZoomIn } from "lucide-react";

import type { CanvasNodeData } from "@/types/canvas";
import i18n from "@/i18n";
import { modelSupportsFeature } from "@/lib/model-constraints";
import { resolveModelForCapability } from "@/stores/use-model-catalog-store";
import { useConfigStore } from "@/stores/use-config-store";

/** 图片节点动作工具的 id 集合（悬浮工具栏可配置显示的工具）。 */
export type ImageNodeActionToolId = "copyPrompt" | "reversePrompt" | "replace" | "resize" | "maskEdit" | "crop" | "split" | "upscale" | "superResolve" | "angle" | "view";
/** 快捷工具条 id：固定项（信息/删除/存素材/下载）+ 可配置的动作工具。 */
export type ImageQuickToolId = "info" | "delete" | "saveAsset" | "download" | ImageNodeActionToolId;

/** 各动作工具的实际执行回调，由工具栏宿主注入（打开对应弹窗或直接动作）。 */
export type ImageToolHandlers = {
    onUpload: (node: CanvasNodeData) => void;
    onToggleFreeResize: (node: CanvasNodeData) => void;
    onMaskEdit: (node: CanvasNodeData) => void;
    onCrop: (node: CanvasNodeData) => void;
    onSplit: (node: CanvasNodeData) => void;
    onUpscale: (node: CanvasNodeData) => void;
    onSuperResolve: (node: CanvasNodeData) => void;
    onAngle: (node: CanvasNodeData) => void;
    onViewImage: (node: CanvasNodeData) => void;
    onCopyPrompt: (node: CanvasNodeData) => void;
    onReversePrompt: (node: CanvasNodeData) => void;
};

/** 单个工具的定义：文案/图标可按节点动态计算，active 表达开关型工具的选中态。 */
export type ImageToolDefinition = {
    id: ImageNodeActionToolId;
    defaultVisible: boolean; // 快捷工具条未自定义时的默认可见性。
    label: string | ((node: CanvasNodeData) => string);
    title: string | ((node: CanvasNodeData) => string);
    icon: (node: CanvasNodeData) => ReactNode;
    active?: (node: CanvasNodeData) => boolean;
    run: (node: CanvasNodeData, handlers: ImageToolHandlers) => void;
};

/** 快捷工具条的用户自定义配置（持久化到 localStorage）。 */
export type ImageQuickToolsConfig = {
    ids: ImageQuickToolId[]; // 显示的工具及顺序。
    showLabels: boolean; // 是否显示文字标签（false 仅图标）。
};

// localStorage 键：v7 表示结构版本，字段变化时换新键而不是兼容旧数据。
export const IMAGE_QUICK_TOOLS_STORAGE_KEY = "canvas-image-quick-tools-v7";

// 固定快捷项：信息/删除/存素材/下载，始终排在可配置工具之前。
const defaultBaseToolIds: ImageQuickToolId[] = ["info", "delete", "saveAsset", "download"];

/** 全部图片动作工具的注册表：工具栏与设置面板共用同一份定义。 */
export const imageToolDefinitions: ImageToolDefinition[] = [
    {
        id: "copyPrompt",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.copyPrompt"),
        title: () => i18n.t("canvas.imageTools.copyPromptTitle"),
        icon: () => <Copy className="size-4" />,
        run: (node, handlers) => handlers.onCopyPrompt(node),
    },
    {
        id: "reversePrompt",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.reversePrompt"),
        title: () => i18n.t("canvas.imageTools.reversePromptTitle"),
        icon: () => <FileText className="size-4" />,
        run: (node, handlers) => handlers.onReversePrompt(node),
    },
    {
        id: "replace",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.replace"),
        title: () => i18n.t("canvas.imageTools.replace"),
        icon: () => <Upload className="size-4" />,
        run: (node, handlers) => handlers.onUpload(node),
    },
    {
        id: "resize",
        defaultVisible: false,
        label: (node) => i18n.t(node.metadata?.freeResize ? "canvas.imageTools.free" : "canvas.imageTools.locked"),
        title: (node) => i18n.t(node.metadata?.freeResize ? "canvas.imageTools.lockTitle" : "canvas.imageTools.freeTitle"),
        icon: (node) => (node.metadata?.freeResize ? <LockOpen className="size-4" /> : <Lock className="size-4" />),
        active: (node) => Boolean(node.metadata?.freeResize),
        run: (node, handlers) => handlers.onToggleFreeResize(node),
    },
    {
        id: "maskEdit",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.mask"),
        title: () => i18n.t("canvas.imageTools.maskTitle"),
        icon: () => <Brush className="size-4" />,
        run: (node, handlers) => handlers.onMaskEdit(node),
    },
    {
        id: "crop",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.crop"),
        title: () => i18n.t("canvas.imageTools.cropTitle"),
        icon: () => <Scissors className="size-4" />,
        run: (node, handlers) => handlers.onCrop(node),
    },
    {
        id: "split",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.split"),
        title: () => i18n.t("canvas.imageTools.splitTitle"),
        icon: () => <Grid2x2 className="size-4" />,
        run: (node, handlers) => handlers.onSplit(node),
    },
    {
        id: "upscale",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.upscale"),
        title: () => i18n.t("canvas.imageTools.upscaleTitle"),
        icon: () => <ZoomIn className="size-4" />,
        run: (node, handlers) => handlers.onUpscale(node),
    },
    {
        id: "superResolve",
        defaultVisible: false,
        label: () => i18n.t("canvas.imageTools.superResolve"),
        title: () => i18n.t("canvas.imageTools.superResolveTitle"),
        icon: () => <Sparkles className="size-4" />,
        run: (node, handlers) => handlers.onSuperResolve(node),
    },
    {
        id: "angle",
        defaultVisible: false,
        label: () => i18n.t("canvas.imageTools.angle"),
        title: () => i18n.t("canvas.imageTools.angleTitle"),
        icon: () => <Camera className="size-4" />,
        run: (node, handlers) => handlers.onAngle(node),
    },
    {
        id: "view",
        defaultVisible: true,
        label: () => i18n.t("canvas.imageTools.view"),
        title: () => i18n.t("canvas.imageTools.viewTitle"),
        icon: () => <Maximize2 className="size-4" />,
        run: (node, handlers) => handlers.onViewImage(node),
    },
];

/** 默认快捷工具顺序：固定项 + 所有 defaultVisible 的动作工具。 */
export const defaultImageQuickToolIds: ImageQuickToolId[] = [...defaultBaseToolIds, ...imageToolDefinitions.filter((tool) => tool.defaultVisible).map((tool) => tool.id)];

/**
 * 构建图片节点悬浮工具栏的工具列表：过滤掉蒙版能力（当前生图模型不支持时隐藏蒙版入口），
 * 解析按节点动态计算的文案/图标。
 */
export function buildImageToolbarTools(node: CanvasNodeData, handlers: ImageToolHandlers) {
    // 蒙版入口只在当前生图模型声明 mask 能力时出现。
    const config = useConfigStore.getState().config;
    const canEditMask = modelSupportsFeature(resolveModelForCapability(node.metadata?.model, "image", config.imageModel), "mask");
    return imageToolDefinitions
        .filter((tool) => tool.id !== "maskEdit" || canEditMask)
        .map((tool) => ({
            id: tool.id,
            label: resolveToolText(tool.label, node),
            title: resolveToolText(tool.title, node),
            icon: tool.icon(node),
            active: tool.active?.(node),
            onClick: () => tool.run(node, handlers),
        }));
}

/** 清洗用户自定义的工具 id 列表：只保留合法 id，并按注册表顺序输出。 */
export function normalizeImageQuickToolIds(value: unknown[]) {
    const allIds: ImageQuickToolId[] = [...defaultBaseToolIds, ...imageToolDefinitions.map((tool) => tool.id)];
    const ids = new Set(allIds);
    return allIds.filter((id) => value.includes(id) && ids.has(id));
}

/** 解析 localStorage 读出的配置：兼容旧版纯数组格式与损坏值，均回落默认。 */
export function readImageQuickToolsConfig(value: unknown): ImageQuickToolsConfig {
    if (Array.isArray(value)) return { ids: normalizeImageQuickToolIds(value), showLabels: false };
    if (!value || typeof value !== "object") return { ids: defaultImageQuickToolIds, showLabels: false };
    const data = value as Partial<ImageQuickToolsConfig>;
    return {
        ids: Array.isArray(data.ids) ? normalizeImageQuickToolIds(data.ids) : defaultImageQuickToolIds,
        showLabels: data.showLabels === true,
    };
}

/** 文案字段解析：函数形式按节点计算，否则直接用字符串。 */
function resolveToolText(value: string | ((node: CanvasNodeData) => string), node: CanvasNodeData) {
    return typeof value === "function" ? value(node) : value;
}
