import type { ComponentType, ReactNode } from "react";

import type { CanvasAgentOp } from "@/lib/canvas/canvas-agent-ops";
import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CanvasConnection, CanvasNodeData, CanvasNodeMetadata } from "@/types/canvas";
import type { CanvasResourceKind } from "@/lib/canvas/canvas-resource-references";

// 插件系统类型契约：宿主注入的能力（CanvasNodeContext/CanvasPluginHost）
// 与插件包导出的定义（CanvasPlugin），SDK 与宿主两侧共用。

// 节点作为上游输入被消费时产出的资源。
export type CanvasNodeResource = { kind: CanvasResourceKind; text?: string; url?: string };

// 宿主注入的 AI 生成能力：复用宿主的模型与密钥配置，插件无需自带凭据。
export type GenerateOptions = { signal?: AbortSignal; references?: string[]; model?: string };
export type GenerateImageOptions = GenerateOptions & { count?: number; size?: string };
export type GenerateImageResult = { images: string[] };
export type GenerateVideoOptions = GenerateOptions & { size?: string; seconds?: string };
export type GenerateVideoResult = { url: string; mimeType: string; width?: number; height?: number; durationMs?: number };
export type GenerateTextOptions = { signal?: AbortSignal; model?: string; system?: string; onDelta?: (text: string) => void };
export type GenerateTextResult = { text: string };
export type PluginModelCapability = "image" | "video" | "text" | "audio";
export type ModelOption = { value: string; label: string };

export type CanvasPluginAi = {
    generateImage: (prompt: string, options?: GenerateImageOptions) => Promise<GenerateImageResult>;
    generateVideo: (prompt: string, options?: GenerateVideoOptions) => Promise<GenerateVideoResult>;
    generateText: (prompt: string, options?: GenerateTextOptions) => Promise<GenerateTextResult>;
    listModels: (capability?: PluginModelCapability) => ModelOption[];
    defaultModel: (capability: PluginModelCapability) => string;
};

// 节点悬停工具栏上追加的自定义按钮。
export type CanvasNodeToolbarItem = {
    id: string;
    title: string;
    label: string;
    icon: ReactNode;
    onClick: () => void;
    active?: boolean;
    danger?: boolean;
};

// 渲染每个节点时注入的上下文：插件与画布交互的主接口。
export type CanvasNodeContext = {
    node: CanvasNodeData;
    theme: CanvasTheme;
    scale: number;
    isSelected: boolean; // 是否选中：按需开启 iframe 交互等场景使用。
    // 节点数据读写。
    updateMetadata: (patch: CanvasNodeMetadata) => void;
    updateNode: (patch: Partial<Pick<CanvasNodeData, "title" | "width" | "height">>) => void;
    // 图谱访问。
    getNode: (id: string) => CanvasNodeData | null;
    getNodes: () => CanvasNodeData[];
    getConnections: () => CanvasConnection[];
    getUpstream: () => CanvasNodeData[];
    getDownstream: () => CanvasNodeData[];
    // 画布操作：复用 Agent 指令集，可改节点/连线/选中/视口并触发生成。
    applyOps: (ops: CanvasAgentOp[]) => void;
    // 节点间与插件间的事件通信。
    emit: (event: string, payload?: unknown) => void;
    on: (event: string, handler: (payload: unknown) => void) => () => void;
    // 图像/视频/文本生成，走宿主模型配置。
    ai: CanvasPluginAi;
    // 打开/关闭本节点下方的自定义面板；定义需提供 Panel。
    openPanel: () => void;
    closePanel: () => void;
    // 插件私有持久化（按插件 id 隔离命名空间）。
    storage: PluginStorage;
};

/** 插件私有存储：底层是 IndexedDB，按插件 id 隔离，值可为任意可结构化克隆对象。 */
export type PluginStorage = {
    get: <T = unknown>(key: string) => Promise<T | null>;
    set: (key: string, value: unknown) => Promise<void>;
    remove: (key: string) => Promise<void>;
};

// 与节点无关的宿主能力：由画布页构造并注入渲染链。
export type CanvasPluginHost = {
    getNode: (id: string) => CanvasNodeData | null;
    getNodes: () => CanvasNodeData[];
    getConnections: () => CanvasConnection[];
    getUpstream: (nodeId: string) => CanvasNodeData[];
    getDownstream: (nodeId: string) => CanvasNodeData[];
    updateNode: (nodeId: string, patch: Partial<Pick<CanvasNodeData, "title" | "width" | "height">>) => void;
    updateMetadata: (nodeId: string, patch: CanvasNodeMetadata) => void;
    applyOps: (ops: CanvasAgentOp[]) => void;
    // 使用宿主模型与密钥配置的 AI 生成。
    ai: CanvasPluginAi;
    // 打开/关闭指定节点下方的自定义面板。
    openPanel: (nodeId: string) => void;
    closePanel: () => void;
};

// 复用宿主内置生成面板的配置（SDK 侧见 CanvasBuiltinPanelConfig）。
export type CanvasBuiltinPanelConfig = {
    mode: "image" | "video" | "text" | "audio";
    promptPrefix?: string;
    writeBackToSelf?: boolean;
};

// 节点定义：内置节点与插件节点共用。
export type CanvasNodeDefinition = {
    type: string; // 内置节点如 "image"；插件节点须用 "<pluginId>:<name>"。
    title: string;
    icon: ReactNode;
    description?: string;
    defaultSize: { width: number; height: number };
    defaultMetadata?: CanvasNodeMetadata;
    minimapColor?: string;
    showInCreateMenu?: boolean; // 默认 true。
    hasSourceHandle?: boolean; // 右侧输出手柄；默认 true。
    hidePanel?: boolean; // 禁止点击/创建时打开下方面板，适合纯展示节点。
    transparentBackground?: boolean; // 节点卡片透明，让 SVG/矢量内容融入画布。
    autoOpenPanel?: boolean; // 点击即打开自定义 Panel；不开时自动打开面板只对内置节点生效。
    useBuiltinPanel?: CanvasBuiltinPanelConfig; // 复用内置生成面板，而非自定义 Panel。
    // 交互/移动开关：由宿主提供工具栏切换，并通过 metadata.interactive 控制指针事件。
    interactionToggle?: boolean;
    // 配合 interactionToggle：返回 true 强制可交互，忽略 metadata.interactive 并隐藏开关。
    forceInteractive?: (node: CanvasNodeData) => boolean;
    keepAspectRatio?: (node: CanvasNodeData) => boolean;
    resource?: (node: CanvasNodeData) => CanvasNodeResource | null;
    // 内置节点使用 canvas-node 内部渲染器，可省略 Content。
    Content?: ComponentType<{ ctx: CanvasNodeContext }>;
    Panel?: ComponentType<{ ctx: CanvasNodeContext; onClose: () => void }>;
    toolbar?: (ctx: CanvasNodeContext) => CanvasNodeToolbarItem[];
    onDoubleClick?: (ctx: CanvasNodeContext) => boolean; // 返回 true 表示已处理。
};

// 插件启动时可用的应用级能力。
export type CanvasPluginApp = {
    version: string;
    emit: (event: string, payload?: unknown) => void;
    on: (event: string, handler: (payload: unknown) => void) => () => void;
    // 注入插件样式并返回清理函数；同 key 会替换此前样式。
    injectCSS: (css: string, key?: string) => () => void;
};

// 插件包的默认导出。
export type CanvasPlugin = {
    id: string;
    name: string;
    version: string;
    description?: string;
    minAppVersion?: string;
    css?: string; // 启用时注入，卸载或禁用时移除。
    nodes: CanvasNodeDefinition[];
    setup?: (app: CanvasPluginApp) => void | (() => void);
};
