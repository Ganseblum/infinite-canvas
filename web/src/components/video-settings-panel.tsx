import { useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import i18n from "@/i18n";
import { ImageSettingsTheme } from "@/components/image-settings-panel";
import { useModelConstraints } from "@/hooks/use-model-catalog";
import { constraintOptions, constraintValues, pickConstraintValue } from "@/lib/model-constraints";
import { type CanvasTheme } from "@/lib/canvas-theme";
import { parseAspectRatio, parsePixelSize } from "@/lib/media-size";
import { type AiConfig } from "@/stores/use-config-store";

const videoModeOptions = [
    { value: "frames", labelKey: "frames" },
    { value: "reference", labelKey: "reference" },
];

type VideoSettingsPanelProps = {
    config: AiConfig;
    onConfigChange: (key: "vquality" | "size" | "videoSeconds" | "videoGenerateAudio" | "videoWatermark" | "videoMode", value: string) => void;
    theme: CanvasTheme;
    showTitle?: boolean;
    className?: string;
};

export function VideoSettingsPanel({ config, onConfigChange, theme, showTitle = true, className = "w-[320px] space-y-4 rounded-2xl px-1 py-0.5" }: VideoSettingsPanelProps) {
    const { t } = useTranslation();
    const constraints = useModelConstraints(config.model);
    const resolutionOptions = constraintOptions(constraints?.resolution);
    const ratioOptions = constraintOptions(constraints?.ratio);
    const durationOptions = constraintValues(constraints?.duration);
    const supportsReference = Boolean(constraints?.features?.includes("referenceImage"));
    const resolution = pickConstraintValue(constraints?.resolution, config.vquality || "");
    const ratio = pickConstraintValue(constraints?.ratio, config.size || "");
    const seconds = pickConstraintValue(constraints?.duration, config.videoSeconds || "");
    const videoMode = config.videoMode === "reference" ? "reference" : "frames";

    // 切换模型后把不在新约束里的旧取值落回第一个合法值。
    useEffect(() => {
        if (resolutionOptions.length && !constraintValues(constraints?.resolution).includes(config.vquality)) onConfigChange("vquality", resolutionOptions[0].value);
        if (ratioOptions.length && !constraintValues(constraints?.ratio).includes(config.size)) onConfigChange("size", ratioOptions[0].value);
        if (durationOptions.length && !durationOptions.includes(config.videoSeconds)) onConfigChange("videoSeconds", durationOptions[0]);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [config.model, config.vquality, config.size, config.videoSeconds, constraints]);

    return (
        <ImageSettingsTheme theme={theme}>
            <div className={className} style={{ color: theme.node.text }} onMouseDown={(event) => event.stopPropagation()}>
                {showTitle ? <div className="text-lg font-semibold">{t("settingsPanels.video.title")}</div> : null}
                {resolutionOptions.length ? (
                    <SettingGroup title={t("settingsPanels.video.quality")} color={theme.node.muted}>
                        <div className="grid grid-cols-4 gap-2.5">
                            {resolutionOptions.map((item) => (
                                <OptionPill key={item.value} selected={resolution === item.value} theme={theme} onClick={() => onConfigChange("vquality", item.value)}>
                                    {item.label}
                                </OptionPill>
                            ))}
                        </div>
                    </SettingGroup>
                ) : null}
                {ratioOptions.length ? (
                    <SettingGroup title={t("settingsPanels.video.ratio")} color={theme.node.muted}>
                        <div className="grid grid-cols-4 gap-2.5">
                            {ratioOptions.map((item) => (
                                <button
                                    key={item.value}
                                    type="button"
                                    className="flex h-[72px] cursor-pointer flex-col items-center justify-center gap-1.5 rounded-xl border bg-transparent text-sm transition hover:opacity-80"
                                    style={{ borderColor: ratio === item.value ? theme.node.text : theme.node.stroke, color: theme.node.text }}
                                    onMouseDown={(event) => event.stopPropagation()}
                                    onClick={() => onConfigChange("size", item.value)}
                                >
                                    <SizePreview ratio={item.value} color={theme.node.text} />
                                    <span>{item.label}</span>
                                </button>
                            ))}
                        </div>
                    </SettingGroup>
                ) : null}
                {durationOptions.length ? (
                    <SettingGroup title={t("settingsPanels.video.seconds")} color={theme.node.muted}>
                        <div className="grid grid-cols-4 gap-2.5">
                            {durationOptions.map((value) => (
                                <OptionPill key={value} selected={seconds === value} theme={theme} onClick={() => onConfigChange("videoSeconds", value)}>
                                    {value}s
                                </OptionPill>
                            ))}
                        </div>
                    </SettingGroup>
                ) : null}
                {supportsReference ? (
                    <SettingGroup title={t("settingsPanels.video.mode")} color={theme.node.muted}>
                        <div className="grid grid-cols-2 gap-2.5">
                            {videoModeOptions.map((item) => (
                                <OptionPill key={item.value} selected={videoMode === item.value} theme={theme} onClick={() => onConfigChange("videoMode", item.value)}>
                                    {t(`settingsPanels.video.modes.${item.labelKey}`)}
                                </OptionPill>
                            ))}
                        </div>
                    </SettingGroup>
                ) : null}
            </div>
        </ImageSettingsTheme>
    );
}

export function videoResolutionLabel(value: string) {
    const raw = String(value || "").trim();
    return raw.endsWith("p") ? raw : `${raw.replace(/p$/i, "") || "720"}p`;
}

export function videoSizeLabel(value: string) {
    return value || i18n.t("settingsPanels.video.adaptive");
}

export function videoSecondsLabel(value: string) {
    if (String(value).trim() === "-1") return i18n.t("settingsPanels.video.smart");
    return `${value || "6"}s`;
}

export function videoModeLabel(value: string) {
    return i18n.t(`settingsPanels.video.modes.${value === "reference" ? "reference" : "frames"}`);
}

function OptionPill({ selected, theme, onClick, children }: { selected: boolean; theme: CanvasTheme; onClick: () => void; children: ReactNode }) {
    return (
        <button
            type="button"
            className="h-9 cursor-pointer rounded-full border px-2 text-sm transition hover:opacity-80"
            style={{ background: "transparent", borderColor: selected ? theme.node.text : theme.node.stroke, color: theme.node.text }}
            onMouseDown={(event) => event.stopPropagation()}
            onClick={onClick}
        >
            {children}
        </button>
    );
}

function SettingGroup({ title, color, children }: { title: string; color: string; children: ReactNode }) {
    return (
        <div className="space-y-2.5">
            <div className="text-xs font-medium" style={{ color }}>
                {title}
            </div>
            {children}
        </div>
    );
}

function SizePreview({ ratio, color }: { ratio: string; color: string }) {
    const parsed = parsePixelSize(ratio) || parseAspectRatio(ratio);
    if (!parsed) return null;
    const longSide = Math.max(parsed.width, parsed.height);
    const previewWidth = Math.max(10, Math.round((parsed.width / longSide) * 26));
    const previewHeight = Math.max(10, Math.round((parsed.height / longSide) * 26));
    return <span className="rounded-[3px] border-2" style={{ width: previewWidth, height: previewHeight, borderColor: color }} />;
}
