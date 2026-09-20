import { FileText, Group, Image as ImageIcon, Music2, Settings2, Video } from "lucide-react";

import i18n from "@/i18n";

import { NODE_SPECS } from "@/constant/canvas";
import { registerNodeDefinitions } from "@/lib/canvas/node-registry";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import type { CanvasNodeDefinition, CanvasNodeResource } from "@/types/canvas-plugin";

// Extensible metadata for built-in nodes, reusing NODE_SPECS for size and initial metadata.
// Rendering remains in canvas-node's internal renderer, so no Content component is provided.
// 从节点 metadata 读取资源引用，供引用条/生成流程等统一通过节点定义访问内容。
function builtinResource(node: CanvasNodeData): CanvasNodeResource | null {
    if (node.type === CanvasNodeType.Image && node.metadata?.content) return { kind: "image", url: node.metadata.content };
    if (node.type === CanvasNodeType.Video && node.metadata?.content) return { kind: "video", url: node.metadata.content };
    if (node.type === CanvasNodeType.Audio && node.metadata?.content) return { kind: "audio", url: node.metadata.content };
    if (node.type === CanvasNodeType.Text && (node.metadata?.content || node.metadata?.prompt)) return { kind: "text", text: node.metadata.content || node.metadata.prompt };
    return null;
}

const iconClass = "size-5";

// 内置节点定义：图标/默认尺寸/初始元数据来自 NODE_SPECS；图片与视频声明锁比例，
// Config 节点没有输出端（不可作为来源连线），图/视/音/文提供资源读取。
const BUILTIN_DEFINITIONS: CanvasNodeDefinition[] = [
    { type: CanvasNodeType.Text, title: i18n.t("assets.kinds.text"), icon: <FileText className={iconClass} />, minimapColor: undefined, resource: builtinResource },
    { type: CanvasNodeType.Image, title: i18n.t("assets.kinds.image"), icon: <ImageIcon className={iconClass} />, minimapColor: "#10b981", keepAspectRatio: (node: CanvasNodeData) => !node.metadata?.freeResize, resource: builtinResource },
    { type: CanvasNodeType.Video, title: i18n.t("assets.kinds.video"), icon: <Video className={iconClass} />, minimapColor: "#f97316", keepAspectRatio: () => true, resource: builtinResource },
    { type: CanvasNodeType.Audio, title: i18n.t("assets.kinds.audio"), icon: <Music2 className={iconClass} />, minimapColor: "#a855f7", resource: builtinResource },
    { type: CanvasNodeType.Config, title: i18n.t("canvas.configNode.title"), icon: <Settings2 className={iconClass} />, minimapColor: "#60a5fa", hasSourceHandle: false },
    { type: CanvasNodeType.Group, title: i18n.t("canvas.node.group"), icon: <Group className={iconClass} />, minimapColor: "#94a3b8" },
].map((def) => {
    const spec = NODE_SPECS[def.type];
    return { ...def, title: spec.title, defaultSize: { width: spec.width, height: spec.height }, defaultMetadata: spec.metadata };
});

// 幂等注册：插件加载等流程可能多次调用，只注册一次。
let registered = false;
/** 把内置节点类型注册进节点注册表（title/尺寸/初始 metadata 用 NODE_SPECS 覆盖）。 */
export function registerBuiltinNodes() {
    if (registered) return;
    registered = true;
    registerNodeDefinitions(BUILTIN_DEFINITIONS, "builtin");
}
