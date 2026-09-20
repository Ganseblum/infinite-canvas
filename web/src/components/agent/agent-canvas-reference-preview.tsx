import { FileText, Image as ImageIcon, Music2, Video } from "lucide-react";
import { useTranslation } from "react-i18next";

import i18n from "@/i18n";
import { canvasThemes } from "@/lib/canvas-theme";
import type { AgentCanvasReference } from "@/stores/use-agent-store";

/**
 * 画布素材引用的悬停预览卡片：按素材类型展示图片/视频/音频播放器或文本内容，
 * 预览数据取不到时显示「暂无法预览」占位。
 */
export function AgentCanvasReferencePreview({ reference, previewUrl, previewText, theme }: { reference: AgentCanvasReference; previewUrl?: string; previewText?: string; theme: (typeof canvasThemes)[keyof typeof canvasThemes] }) {
    const { t } = useTranslation();
    const Icon = canvasReferenceIcon(reference.kind);
    return (
        <div className="w-64" style={{ color: theme.node.text }}>
            <div className="mb-2 flex min-w-0 items-center gap-2">
                <Icon className="size-4 shrink-0" style={{ color: theme.node.muted }} />
                <span className="truncate text-sm font-medium">{reference.title}</span>
            </div>
            {reference.kind === "image" && previewUrl ? <img src={previewUrl} alt={reference.title} className="max-h-64 w-full rounded-md object-contain" /> : null}
            {reference.kind === "video" && previewUrl ? <video src={previewUrl} controls preload="metadata" className="max-h-64 w-full rounded-md" /> : null}
            {reference.kind === "audio" && previewUrl ? <audio src={previewUrl} controls preload="metadata" className="w-full" /> : null}
            {reference.kind === "text" && previewText ? (
                <div className="thin-scrollbar max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-md border px-3 py-2 text-xs leading-5" style={{ borderColor: theme.node.stroke }}>
                    {previewText}
                </div>
            ) : null}
            {!previewUrl && !(reference.kind === "text" && previewText) ? <div className="py-3 text-center text-xs" style={{ color: theme.node.muted }}>{t("agent.composer.mentions.previewUnavailable")}</div> : null}
        </div>
    );
}

/** 素材类型 → lucide 图标；文本/未知类型一律回退到文件图标。 */
export function canvasReferenceIcon(kind: AgentCanvasReference["kind"]) {
    return kind === "audio" ? Music2 : kind === "video" ? Video : kind === "image" ? ImageIcon : FileText;
}

/** 素材类型的本地化名称，用于 aria 标签与候选列表的副标题。 */
export function canvasReferenceKindLabel(kind: AgentCanvasReference["kind"]) {
    return i18n.t(`agent.composer.mentions.kind.${kind}`);
}
