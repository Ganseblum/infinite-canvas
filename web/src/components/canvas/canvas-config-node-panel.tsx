import type { CSSProperties } from "react";
import { Image as ImageIcon, LoaderCircle, MessageSquare, Music2, Play, Settings2, Square, Video } from "lucide-react";
import { Button, Segmented } from "antd";
import { useTranslation } from "react-i18next";

import { ModelPicker } from "@/components/model-picker";
import { defaultConfig, useEffectiveConfig, type AiConfig } from "@/stores/use-config-store";
import { resolveModelForCapability } from "@/stores/use-model-catalog-store";
import { canvasThemes } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import { CanvasImageSettingsPopover } from "./canvas-image-settings-popover";
import { CanvasAudioSettingsPopover, type CanvasAudioSettingKey } from "./canvas-audio-settings-popover";
import { CanvasVideoSettingsPopover } from "./canvas-video-settings-popover";
import { CanvasTextSettingsPopover } from "./canvas-text-settings-popover";
import type { CanvasGenerationMode, CanvasNodeData, CanvasNodeMetadata } from "@/types/canvas";

/** Config 节点下方面板的 props：配置变更以 metadata patch 形式回传父层落库。 */
type CanvasConfigNodePanelProps = {
    node: CanvasNodeData;
    isRunning: boolean; // 生成进行中：按钮切换为「停止」。
    inputSummary: { textCount: number; imageCount: number; videoCount: number; audioCount: number }; // 上游资源计数，展示为标签。
    onConfigChange: (nodeId: string, patch: Partial<CanvasNodeMetadata>) => void;
    onGenerate: (nodeId: string) => void;
    onStop: (nodeId: string) => void;
    onComposerToggle: () => void; // 打开 composer 引用编排弹窗。
};

/**
 * Config 节点配置面板：按生成模式（图/文/视频/音频）展示模式切换、上游输入统计、
 * 模型选择与对应设置弹层，底部为生成/停止按钮。
 * 配置项写入节点 metadata，缺省回落到全局配置。
 */
export function CanvasConfigNodePanel({ node, isRunning, inputSummary, onConfigChange, onGenerate, onStop, onComposerToggle }: CanvasConfigNodePanelProps) {
    const { t } = useTranslation();
    const globalConfig = useEffectiveConfig();
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const mode = node.metadata?.generationMode || "image";
    const config = buildNodeConfig(globalConfig, node, mode);
    const chipStyle = { background: theme.node.fill, borderColor: theme.node.stroke, color: theme.node.text };
    const hasAnyInput = Boolean(inputSummary.textCount || inputSummary.imageCount || inputSummary.videoCount || inputSummary.audioCount);
    const hasComposerContent = Boolean((node.metadata?.composerContent ?? node.metadata?.prompt ?? "").trim());
    // 可生成条件：有编排提示词，或有上游输入（音频只依赖文本，作为 TTS 语料）。
    const canGenerate = hasComposerContent || (mode === "audio" ? inputSummary.textCount > 0 : hasAnyInput);

    return (
        <div className="flex h-full w-full cursor-move flex-col px-3 pb-3 pt-7 text-sm" style={{ color: theme.node.text }} onWheel={(event) => event.stopPropagation()}>
            {/* 面板标题 + 生成模式切换（Segmented 的指针事件需阻断，避免拖动节点）。 */}
            <div className="mb-2 flex items-center justify-between gap-3">
                <div className="shrink-0 text-sm font-semibold">{t("canvas.configNode.title")}</div>
                <div className="cursor-default" onMouseDown={(event) => event.stopPropagation()}>
                    <Segmented
                        size="small"
                        className="canvas-config-mode !rounded-md !p-0.5"
                        value={mode}
                        onChange={(value) => onConfigChange(node.id, { generationMode: value as CanvasGenerationMode })}
                        options={[
                            {
                                value: "image",
                                label: (
                                    <span className="inline-flex items-center gap-1">
                                        <ImageIcon className="size-3.5" />
                                        {t("canvas.configNode.image")}
                                    </span>
                                ),
                            },
                            {
                                value: "text",
                                label: (
                                    <span className="inline-flex items-center gap-1">
                                        <MessageSquare className="size-3.5" />
                                        {t("canvas.configNode.text")}
                                    </span>
                                ),
                            },
                            {
                                value: "video",
                                label: (
                                    <span className="inline-flex items-center gap-1">
                                        <Video className="size-3.5" />
                                        {t("canvas.configNode.video")}
                                    </span>
                                ),
                            },
                            {
                                value: "audio",
                                label: (
                                    <span className="inline-flex items-center gap-1">
                                        <Music2 className="size-3.5" />
                                        {t("canvas.configNode.audio")}
                                    </span>
                                ),
                            },
                        ]}
                    />
                </div>
            </div>

            {/* 上游输入统计标签 + 编排入口。 */}
            <div className="mb-2 flex flex-wrap gap-1.5">
                <InputChip label={t("canvas.configNode.prompt")} value={t("canvas.configNode.items", { count: inputSummary.textCount })} style={chipStyle} />
                <InputChip label={t("canvas.configNode.references")} value={t("canvas.configNode.images", { count: inputSummary.imageCount })} style={chipStyle} />
                <InputChip label={t("canvas.configNode.videoReferences")} value={t("canvas.configNode.items", { count: inputSummary.videoCount })} style={chipStyle} />
                <InputChip label={t("canvas.configNode.audioReferences")} value={t("canvas.configNode.items", { count: inputSummary.audioCount })} style={chipStyle} />
                <button type="button" className="inline-flex h-7 cursor-pointer items-center gap-1 rounded-md border px-2 text-[11px]" style={chipStyle} onMouseDown={(event) => event.stopPropagation()} onClick={onComposerToggle}>
                    <Settings2 className="size-3.5" />
                    {t("canvas.configNode.compose")}
                </button>
            </div>

            {/* 模型选择 + 按模式渲染对应的设置弹层（配置变更转写成节点 metadata 字段）。 */}
            <div className="mb-2 grid min-w-0 cursor-default grid-cols-[minmax(0,1fr)_148px] items-center gap-2" onMouseDown={(event) => event.stopPropagation()}>
                <ModelPicker className="canvas-compact-control h-10" value={config.model} onChange={(model) => onConfigChange(node.id, { model })} capability={mode} fullWidth />
                {mode === "video" ? (
                    <CanvasVideoSettingsPopover
                        config={config}
                        placement="topRight"
                        buttonClassName="canvas-compact-control !h-10 !w-full !justify-start !rounded-lg !px-2"
                        onConfigChange={(key, value) => onConfigChange(node.id, videoConfigPatch(key, value))}
                    />
                ) : mode === "image" ? (
                    <CanvasImageSettingsPopover
                        config={config}
                        placement="topRight"
                        autoAdjustOverflow={false}
                        buttonClassName="canvas-compact-control !h-10 !w-full !justify-start !rounded-lg !px-2"
                        onConfigChange={(key, value) => onConfigChange(node.id, key === "count" ? { count: Number(value) || 1 } : { [key]: value })}
                    />
                ) : mode === "audio" ? (
                    <CanvasAudioSettingsPopover
                        config={config}
                        placement="topRight"
                        buttonClassName="canvas-compact-control !h-10 !w-full !justify-start !rounded-lg !px-2"
                        onConfigChange={(key, value) => onConfigChange(node.id, audioConfigPatch(key, value))}
                    />
                ) : (
                    <CanvasTextSettingsPopover
                        config={config}
                        count={node.metadata?.textCount || 1}
                        placement="topRight"
                        buttonClassName="canvas-compact-control !h-10 !w-full !justify-start !rounded-lg !px-2"
                        onConfigChange={(_, value) => onConfigChange(node.id, { reasoningEffort: value })}
                        onCountChange={(textCount) => onConfigChange(node.id, { textCount })}
                    />
                )}
            </div>

            <Button
                type="primary"
                className="mt-auto !h-9 !w-full !cursor-pointer !rounded-lg"
                danger={isRunning}
                disabled={!isRunning && !canGenerate}
                onMouseDown={(event) => event.stopPropagation()}
                onClick={() => (isRunning ? onStop(node.id) : onGenerate(node.id))}
            >
                <span className="inline-flex items-center gap-1.5">
                    {isRunning ? (
                        <>
                            <LoaderCircle className="size-4 animate-spin" />
                            <Square className="size-3.5 fill-current" />
                            <span>{t("canvas.configNode.stop")}</span>
                        </>
                    ) : (
                        <>
                            <Play className="size-4" />
                            <span>{t("canvas.configNode.generate")}</span>
                        </>
                    )}
                </span>
            </Button>
        </div>
    );
}

/** 输入统计小标签：label + 数值。 */
function InputChip({ label, value, style }: { label: string; value: string; style: CSSProperties }) {
    return (
        <div className="inline-flex h-7 items-center gap-1 rounded-md border px-2 text-[11px]" style={style}>
            <span>{label}</span>
            <span className="font-medium">{value}</span>
        </div>
    );
}

/** 合成节点生效配置：节点 metadata 各字段优先，逐项回落全局配置，最后回落内置默认值。 */
function buildNodeConfig(globalConfig: AiConfig, node: CanvasNodeData, mode: CanvasGenerationMode): AiConfig {
    const defaultModel = mode === "image" ? globalConfig.imageModel : mode === "video" ? globalConfig.videoModel : mode === "audio" ? globalConfig.audioModel : globalConfig.textModel;
    return {
        ...globalConfig,
        model: resolveModelForCapability(node.metadata?.model, mode, defaultModel),
        reasoningEffort: node.metadata?.reasoningEffort || globalConfig.reasoningEffort || defaultConfig.reasoningEffort,
        quality: node.metadata?.quality || globalConfig.quality || defaultConfig.quality,
        size: node.metadata?.size || globalConfig.size || defaultConfig.size,
        background: node.metadata?.background ?? globalConfig.background ?? defaultConfig.background,
        videoSeconds: node.metadata?.seconds || globalConfig.videoSeconds || defaultConfig.videoSeconds,
        vquality: node.metadata?.vquality || globalConfig.vquality || defaultConfig.vquality,
        videoGenerateAudio: node.metadata?.generateAudio || globalConfig.videoGenerateAudio || defaultConfig.videoGenerateAudio,
        videoWatermark: node.metadata?.watermark || globalConfig.videoWatermark || defaultConfig.videoWatermark,
        videoMode: node.metadata?.videoMode || globalConfig.videoMode || defaultConfig.videoMode,
        audioVoice: node.metadata?.audioVoice || globalConfig.audioVoice || defaultConfig.audioVoice,
        audioFormat: node.metadata?.audioFormat || globalConfig.audioFormat || defaultConfig.audioFormat,
        audioSpeed: node.metadata?.audioSpeed || globalConfig.audioSpeed || defaultConfig.audioSpeed,
        audioInstructions: node.metadata?.audioInstructions || globalConfig.audioInstructions || defaultConfig.audioInstructions,
        count: String(node.metadata?.count || (mode === "image" ? globalConfig.canvasImageCount || globalConfig.count : globalConfig.count) || defaultConfig.count),
    };
}

// 视频设置弹层的配置键 → 节点 metadata 字段映射（两套命名不同，需转写后再写回）。
function videoConfigPatch(key: keyof AiConfig, value: string) {
    if (key === "videoSeconds") return { seconds: value };
    if (key === "videoGenerateAudio") return { generateAudio: value };
    if (key === "videoWatermark") return { watermark: value };
    if (key === "videoMode") return { videoMode: value };
    return { [key]: value };
}

// 音频设置弹层的配置键 → 节点 metadata 字段映射；其余键一律落到 audioInstructions。
function audioConfigPatch(key: CanvasAudioSettingKey, value: string) {
    if (key === "audioVoice") return { audioVoice: value };
    if (key === "audioFormat") return { audioFormat: value };
    if (key === "audioSpeed") return { audioSpeed: value };
    return { audioInstructions: value };
}
