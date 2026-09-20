import { defaultConfig, type AiConfig } from "@/stores/use-config-store";
import { modelConstraints, resolveModelForCapability } from "@/stores/use-model-catalog-store";
import { constraintValues } from "@/lib/model-constraints";
import i18n from "@/i18n";
import { mediaUrl } from "@/services/api/media";
import { ensureImagePreview } from "@/services/image-preview";
import { referenceUrl } from "@/lib/canvas/canvas-node-factory";
import type { NodeGenerationInput } from "@/components/canvas/canvas-node-generation";
import type { CanvasNodeGenerationMode } from "@/components/canvas/canvas-node-prompt-panel";
import type { CanvasImageAngleParams } from "@/components/canvas/canvas-node-angle-dialog";
import type { ReferenceImage } from "@/types/image";
import { CanvasNodeType, type CanvasAssistantSession, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "@/types/canvas";

/** 从 data URL 或 mime 里推断图片扩展名，推断不出按 png。 */
export function imageExtension(dataUrl: string) {
    return dataUrl.match(/^data:image[/]([^;]+)/)?.[1] || dataUrl.match(/image[/]([^;]+)/)?.[1] || "png";
}

/** 音频 mime → 文件扩展名，默认 mp3。 */
export function audioExtension(mimeType?: string) {
    if (mimeType?.includes("wav")) return "wav";
    if (mimeType?.includes("opus")) return "opus";
    if (mimeType?.includes("aac")) return "aac";
    if (mimeType?.includes("flac")) return "flac";
    if (mimeType?.includes("pcm")) return "pcm";
    return "mp3";
}

/** 汇总各类型参考素材的可用地址：统一优先 storageKey，缺省退回原始 url。 */
export function generationReferenceUrls(context: { referenceImages: ReferenceImage[]; referenceVideos: Array<{ storageKey?: string; url?: string }>; referenceAudios?: Array<{ storageKey?: string; url?: string }> }) {
    return [
        ...context.referenceImages.map(referenceUrl).filter((url): url is string => Boolean(url)),
        ...context.referenceVideos.map((video) => video.storageKey || video.url).filter((url): url is string => Boolean(url)),
        ...(context.referenceAudios || []).map((audio) => audio.storageKey || audio.url).filter((url): url is string => Boolean(url)),
    ];
}

// 编辑模式下还原节点保存的参考图列表；任一 reference 失效返回 null 让调用方提示重新选择。
export function resolveMetadataReferences(metadata: CanvasNodeMetadata) {
    if (metadata.generationType !== "edit") return [];
    if (!metadata.references?.length) return null;
    const references = metadata.references.map((url, index) => {
        const resolved = url.startsWith("image:") ? mediaUrl(url) : url;
        return resolved ? { id: `${index}`, name: `reference-${index}.png`, type: "image/png", dataUrl: resolved, storageKey: url.startsWith("image:") ? url : undefined } : null;
    });
    return references.every(Boolean) ? (references as ReferenceImage[]) : null;
}

// 二进制都在服务端，还原画布只是把 storageKey 拼成可直接引用的地址，不再需要异步取 Blob；
// 画布图片按需后台补 768px WebP 预览（服务端拉原件生成，失败静默），节点渲染时按缩放选用。
export function hydrateCanvasImages(nodes: CanvasNodeData[]) {
    for (const node of nodes) {
        const metadata = node.metadata;
        if (node.type !== CanvasNodeType.Image || !metadata) continue;
        if (metadata.storageKey) void ensureImagePreview(metadata.storageKey);
        for (const image of metadata.images || []) {
            if (image.storageKey) void ensureImagePreview(image.storageKey);
        }
    }
    return nodes.map((node) => {
        const metadata = node.metadata;
        const content = metadata?.content;
        if ((node.type === CanvasNodeType.Video || node.type === CanvasNodeType.Audio) && metadata?.storageKey) return { ...node, metadata: { ...metadata, content: mediaUrl(metadata.storageKey) } };
        if (node.type !== CanvasNodeType.Image || !metadata || !content) return node;
        const images = (metadata.images || []).map((image) => (image.content && image.storageKey ? { ...image, content: mediaUrl(image.storageKey) } : image));
        if (metadata.storageKey) return { ...node, metadata: { ...metadata, content: mediaUrl(metadata.storageKey), images } };
        return images.some((image, index) => image !== metadata.images?.[index]) ? { ...node, metadata: { ...metadata, images } } : node;
    });
}

export function hydrateAssistantImages(sessions: CanvasAssistantSession[]) {
    return sessions.map((session) => ({
        ...session,
        messages: session.messages.map((message) => ({
            ...message,
            references: (message.references || []).map((item) => (item.storageKey ? { ...item, dataUrl: mediaUrl(item.storageKey) } : item)),
        })),
    }));
}

/** 张数字符串钳制到 1–15 并取整，空/非法值按 1 处理。 */
export function getGenerationCount(count: string) {
    return Math.max(1, Math.min(15, Math.floor(Math.abs(Number(count)) || 1)));
}

/** 统计上游输入的资源构成（组节点展开后按类型计数），供生成入口展示与提示词拼装。 */
export function getInputSummary(inputs: NodeGenerationInput[]) {
    const resources = [...new Map(inputs.flatMap((input) => (input.type === "group" ? input.children : [input])).map((input) => [input.nodeId, input])).values()];
    return {
        textCount: resources.filter((input) => input.type === "text").length,
        imageCount: resources.filter((input) => input.type === "image").length,
        videoCount: resources.filter((input) => input.type === "video").length,
        audioCount: resources.filter((input) => input.type === "audio").length,
    };
}

/**
 * 合成生成配置：节点上保存的参数优先，其次全局配置，最后默认值；
 * 模型按能力回落解析，且 size/分辨率/时长/张数都会按目录约束钳制到合法值。
 */
export function buildGenerationConfig(config: AiConfig, node: CanvasNodeData | undefined, mode: CanvasNodeGenerationMode): AiConfig {
    const defaultModel = mode === "image" ? config.imageModel : mode === "video" ? config.videoModel : mode === "audio" ? config.audioModel : config.textModel;
    const model = resolveModelForCapability(node?.metadata?.model, mode, defaultModel);
    const constraints = modelConstraints(model);
    const legalSizes = [...constraintValues(constraints?.size), ...constraintValues(constraints?.ratio)];
    const legalResolutions = constraintValues(constraints?.resolution);
    const legalDurations = constraintValues(constraints?.duration);
    const maxCount = constraints?.n?.max && constraints.n.max > 0 ? constraints.n.max : undefined;
    const size = node?.metadata?.size || config.size || defaultConfig.size;
    const vquality = node?.metadata?.vquality || config.vquality || defaultConfig.vquality;
    const videoSeconds = node?.metadata?.seconds || config.videoSeconds || defaultConfig.videoSeconds;
    const count = String(node?.metadata?.count || (mode === "image" ? config.canvasImageCount || config.count : config.count) || defaultConfig.count);
    return {
        ...config,
        model,
        reasoningEffort: node?.metadata?.reasoningEffort || config.reasoningEffort || defaultConfig.reasoningEffort,
        quality: node?.metadata?.quality || config.quality || defaultConfig.quality,
        size: legalSizes.length && !legalSizes.includes(size) ? legalSizes[0] : size,
        background: node?.metadata?.background ?? config.background ?? defaultConfig.background,
        videoSeconds: legalDurations.length && !legalDurations.includes(videoSeconds) ? legalDurations[0] : videoSeconds,
        vquality: legalResolutions.length && !legalResolutions.includes(vquality) ? legalResolutions[0] : vquality,
        videoGenerateAudio: node?.metadata?.generateAudio || config.videoGenerateAudio || defaultConfig.videoGenerateAudio,
        videoWatermark: node?.metadata?.watermark || config.videoWatermark || defaultConfig.videoWatermark,
        videoMode: node?.metadata?.videoMode || config.videoMode || defaultConfig.videoMode,
        audioVoice: node?.metadata?.audioVoice || config.audioVoice || defaultConfig.audioVoice,
        audioFormat: node?.metadata?.audioFormat || config.audioFormat || defaultConfig.audioFormat,
        audioSpeed: node?.metadata?.audioSpeed || config.audioSpeed || defaultConfig.audioSpeed,
        audioInstructions: node?.metadata?.audioInstructions || config.audioInstructions || defaultConfig.audioInstructions,
        count: maxCount ? String(Math.min(maxCount, Math.max(1, Math.floor(Math.abs(Number(count)) || 1)))) : count,
    };
}

export function hasResumableVideoTask(node: CanvasNodeData) {
    return node.type === CanvasNodeType.Video && Boolean(node.metadata?.videoTaskId) && !node.metadata?.content;
}

// 页面刷新/断线后把 loading 状态复位为错误；仅有进行中的视频任务（可轮询续跑）保持原状。
export function resetInterruptedGeneration(nodes: CanvasNodeData[]) {
    return nodes.map((node) =>
        node.metadata?.status === "loading"
            ? hasResumableVideoTask(node)
                ? node
                : {
                      ...node,
                      metadata: {
                          ...node.metadata,
                          status: "error" as const,
                          errorDetails: i18n.t("canvas.generation.interrupted"),
                          images: node.metadata.images?.map((image) => (image.status === "loading" ? { ...image, status: "error" as const, errorDetails: i18n.t("canvas.generation.interrupted") } : image)),
                          texts: node.metadata.texts?.map((text) => (text.status === "loading" ? { ...text, status: "error" as const, errorDetails: i18n.t("canvas.generation.interrupted") } : text)),
                      },
                  }
            : node,
    );
}

/** 判断错误是否为用户主动取消（AbortError 或统一取消文案）。 */
export function isGenerationCanceled(error: unknown) {
    return error instanceof Error && (error.message === i18n.t("common.requestCanceled") || error.name === "AbortError");
}

/** 沿连线向上游 BFS，找到第一个 config 节点（重试/续跑时借它的参数重新生成）。 */
export function findRetrySourceNode(nodeId: string, nodes: CanvasNodeData[], connections: CanvasConnection[]) {
    const queue = connections.filter((connection) => connection.toNodeId === nodeId).map((connection) => connection.fromNodeId);
    const visited = new Set<string>();
    while (queue.length) {
        const id = queue.shift()!;
        if (visited.has(id)) continue;
        visited.add(id);
        const node = nodes.find((item) => item.id === id);
        if (node?.type === CanvasNodeType.Config) return node;
        connections.filter((connection) => connection.toNodeId === id).forEach((connection) => queue.push(connection.fromNodeId));
    }
    return null;
}

/** 把某个图片节点包装成单张参考图（图生图「以上游结果为底图」场景），非图片节点返回空。 */
export function sourceNodeReferenceImages(node: CanvasNodeData | null) {
    if (!node || node.type !== CanvasNodeType.Image || !node.metadata?.content) return [];
    return [
        {
            id: node.id,
            name: `${node.title || node.id}.png`,
            type: node.metadata.mimeType || "image/png",
            dataUrl: node.metadata.content,
            storageKey: node.metadata.storageKey,
        },
    ];
}

export function isAudioFile(file: File) {
    return file.type.startsWith("audio/") || /\.(mp3|wav)$/i.test(file.name);
}

/** 生成视角描述文案（如「右转 30° 俯视 15° 距离 5.0 广角」），拼进提示词让模型理解视角。 */
export function buildAngleLabel(params: CanvasImageAngleParams) {
    const horizontal =
        params.horizontalAngle === 0
            ? i18n.t("canvas.generation.front")
            : params.horizontalAngle > 0
              ? i18n.t("canvas.generation.rotateRight", { angle: params.horizontalAngle })
              : i18n.t("canvas.generation.rotateLeft", { angle: Math.abs(params.horizontalAngle) });
    const pitch = params.pitchAngle === 0 ? i18n.t("canvas.generation.level") : params.pitchAngle > 0 ? i18n.t("canvas.generation.topDown", { angle: params.pitchAngle }) : i18n.t("canvas.generation.lowAngle", { angle: Math.abs(params.pitchAngle) });
    return i18n.t("canvas.generation.angleLabel", { horizontal, pitch, distance: params.cameraDistance.toFixed(1), lens: i18n.t(params.wideAngle ? "canvas.editors.wide" : "canvas.editors.standard") });
}

export function buildAnglePrompt(params: CanvasImageAngleParams) {
    return i18n.t("canvas.generation.anglePrompt", { angle: buildAngleLabel(params) });
}
