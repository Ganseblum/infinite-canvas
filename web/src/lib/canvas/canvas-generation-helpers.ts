import { defaultConfig, type AiConfig } from "@/stores/use-config-store";
import { modelConstraints, resolveModelForCapability } from "@/stores/use-model-catalog-store";
import { constraintValues } from "@/lib/model-constraints";
import i18n from "@/i18n";
import { mediaUrl } from "@/services/api/media";
import { referenceUrl } from "@/lib/canvas/canvas-node-factory";
import type { NodeGenerationInput } from "@/components/canvas/canvas-node-generation";
import type { CanvasNodeGenerationMode } from "@/components/canvas/canvas-node-prompt-panel";
import type { CanvasImageAngleParams } from "@/components/canvas/canvas-node-angle-dialog";
import type { ReferenceImage } from "@/types/image";
import { CanvasNodeType, type CanvasAssistantSession, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "@/types/canvas";

export function imageExtension(dataUrl: string) {
    return dataUrl.match(/^data:image[/]([^;]+)/)?.[1] || dataUrl.match(/image[/]([^;]+)/)?.[1] || "png";
}

export function audioExtension(mimeType?: string) {
    if (mimeType?.includes("wav")) return "wav";
    if (mimeType?.includes("opus")) return "opus";
    if (mimeType?.includes("aac")) return "aac";
    if (mimeType?.includes("flac")) return "flac";
    if (mimeType?.includes("pcm")) return "pcm";
    return "mp3";
}

export function generationReferenceUrls(context: { referenceImages: ReferenceImage[]; referenceVideos: Array<{ storageKey?: string; url?: string }>; referenceAudios?: Array<{ storageKey?: string; url?: string }> }) {
    return [
        ...context.referenceImages.map(referenceUrl).filter((url): url is string => Boolean(url)),
        ...context.referenceVideos.map((video) => video.storageKey || video.url).filter((url): url is string => Boolean(url)),
        ...(context.referenceAudios || []).map((audio) => audio.storageKey || audio.url).filter((url): url is string => Boolean(url)),
    ];
}

export function resolveMetadataReferences(metadata: CanvasNodeMetadata) {
    if (metadata.generationType !== "edit") return [];
    if (!metadata.references?.length) return null;
    const references = metadata.references.map((url, index) => {
        const resolved = url.startsWith("image:") ? mediaUrl(url) : url;
        return resolved ? { id: `${index}`, name: `reference-${index}.png`, type: "image/png", dataUrl: resolved, storageKey: url.startsWith("image:") ? url : undefined } : null;
    });
    return references.every(Boolean) ? (references as ReferenceImage[]) : null;
}

// 二进制都在服务端，还原画布只是把 storageKey 拼成可直接引用的地址，不再需要异步取 Blob。
export function hydrateCanvasImages(nodes: CanvasNodeData[]) {
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

export function getGenerationCount(count: string) {
    return Math.max(1, Math.min(15, Math.floor(Math.abs(Number(count)) || 1)));
}

export function getInputSummary(inputs: NodeGenerationInput[]) {
    const resources = [...new Map(inputs.flatMap((input) => (input.type === "group" ? input.children : [input])).map((input) => [input.nodeId, input])).values()];
    return {
        textCount: resources.filter((input) => input.type === "text").length,
        imageCount: resources.filter((input) => input.type === "image").length,
        videoCount: resources.filter((input) => input.type === "video").length,
        audioCount: resources.filter((input) => input.type === "audio").length,
    };
}

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

export function isGenerationCanceled(error: unknown) {
    return error instanceof Error && (error.message === i18n.t("common.requestCanceled") || error.name === "AbortError");
}

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
