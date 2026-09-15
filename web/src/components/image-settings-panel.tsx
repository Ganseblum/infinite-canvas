import { useEffect, type ReactNode } from "react";
import { ConfigProvider, Switch } from "antd";
import { useTranslation } from "react-i18next";

import i18n from "@/i18n";
import { useModelConstraints } from "@/hooks/use-model-catalog";
import { constraintOptions, constraintValues, pickConstraintValue } from "@/lib/model-constraints";
import { type CanvasTheme } from "@/lib/canvas-theme";
import { parseAspectRatio, parsePixelSize } from "@/lib/media-size";
import type { AiConfig } from "@/stores/use-config-store";

type ImageSettingsPanelProps = {
    config: AiConfig;
    onConfigChange: (key: "quality" | "size" | "count" | "background", value: string) => void;
    theme: CanvasTheme;
    showTitle?: boolean;
    className?: string;
    maxCount?: number;
    quickCount?: number;
};

export function ImageSettingsPanel({ config, onConfigChange, theme, showTitle = true, className = "w-[320px] space-y-4 rounded-2xl px-1 py-0.5", maxCount = 15, quickCount = 10 }: ImageSettingsPanelProps) {
    const { t } = useTranslation();
    const constraints = useModelConstraints(config.model);
    const qualityOptions = constraintOptions(constraints?.quality);
    const sizeOptions = constraintOptions(constraints?.size);
    const ratioOptions = constraintOptions(constraints?.ratio);
    const countMax = Math.min(maxCount, constraints?.n?.max && constraints.n.max > 0 ? constraints.n.max : 1);
    const supportsTransparent = Boolean(constraints?.features?.includes("transparentBackground"));
    const count = Math.max(1, Math.min(countMax, Math.floor(Math.abs(Number(config.count)) || 1)));
    const quality = pickConstraintValue(constraints?.quality, config.quality || "");
    const activeSize = config.size || "";
    const showCount = Boolean(constraints?.n?.max && constraints.n.max > 0);

    // 切换模型后把不在新约束里的旧取值落回第一个合法值，避免用户点了生成才被拒绝。
    useEffect(() => {
        if (qualityOptions.length && !constraintValues(constraints?.quality).includes(config.quality)) onConfigChange("quality", qualityOptions[0].value);
        const legalSizes = [...constraintValues(constraints?.size), ...constraintValues(constraints?.ratio)];
        if (legalSizes.length && !legalSizes.includes(config.size)) onConfigChange("size", legalSizes[0]);
        if (showCount && Number(config.count) > countMax) onConfigChange("count", String(countMax));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [config.model, config.quality, config.size, config.count, constraints]);

    return (
        <ImageSettingsTheme theme={theme}>
            <div
                className={className}
                style={{ color: theme.node.text }}
                onMouseDown={(event) => {
                    event.stopPropagation();
                    if (event.target instanceof HTMLInputElement) return;
                    if (document.activeElement instanceof HTMLInputElement && event.currentTarget.contains(document.activeElement)) document.activeElement.blur();
                }}
            >
                {showTitle ? <div className="text-lg font-semibold">{t("settingsPanels.image.title")}</div> : null}
                {qualityOptions.length ? (
                    <div className="space-y-2.5">
                        <SettingTitle color={theme.node.muted}>{t("settingsPanels.image.quality")}</SettingTitle>
                        <div className="grid grid-cols-4 gap-2.5">
                            {qualityOptions.map((item) => (
                                <OptionPill key={item.value} selected={quality === item.value} theme={theme} onClick={() => onConfigChange("quality", item.value)}>
                                    {qualityLabel(item.value)}
                                </OptionPill>
                            ))}
                        </div>
                    </div>
                ) : null}
                {sizeOptions.length ? (
                    <div className="space-y-2.5">
                        <SettingTitle color={theme.node.muted}>{t("settingsPanels.image.size")}</SettingTitle>
                        <div className="grid grid-cols-3 gap-2.5">
                            {sizeOptions.map((item) => (
                                <OptionPill key={item.value} selected={activeSize === item.value} theme={theme} onClick={() => onConfigChange("size", item.value)}>
                                    {item.value}
                                </OptionPill>
                            ))}
                        </div>
                    </div>
                ) : null}
                {ratioOptions.length ? (
                    <div className="space-y-2.5">
                        <SettingTitle color={theme.node.muted}>{t("settingsPanels.image.aspectRatio")}</SettingTitle>
                        <div className="grid grid-cols-4 gap-2.5">
                            {ratioOptions.map((item) => (
                                <button
                                    key={item.value}
                                    type="button"
                                    className="flex h-[72px] cursor-pointer flex-col items-center justify-center gap-1.5 rounded-xl border bg-transparent text-sm transition hover:opacity-80"
                                    style={{ borderColor: activeSize === item.value ? theme.node.text : theme.node.stroke, background: "transparent", color: theme.node.text }}
                                    onMouseDown={(event) => event.stopPropagation()}
                                    onClick={() => onConfigChange("size", item.value)}
                                >
                                    <AspectIcon ratio={item.value} color={theme.node.text} />
                                    <span>{item.value}</span>
                                </button>
                            ))}
                        </div>
                    </div>
                ) : null}
                {supportsTransparent ? (
                    <div className="flex items-center justify-between gap-3">
                        <div className="space-y-0.5">
                            <SettingTitle color={theme.node.muted}>{t("settingsPanels.image.transparent")}</SettingTitle>
                            <div className="text-xs" style={{ color: theme.node.muted, opacity: 0.75 }}>
                                {t("settingsPanels.image.transparentHint")}
                            </div>
                        </div>
                        <span onMouseDown={(event) => event.stopPropagation()}>
                            <Switch size="small" checked={config.background === "transparent"} onChange={(checked) => onConfigChange("background", checked ? "transparent" : "")} />
                        </span>
                    </div>
                ) : null}
                {showCount ? (
                    <div className="space-y-2.5">
                        <SettingTitle color={theme.node.muted}>{t("settingsPanels.image.count")}</SettingTitle>
                        <div className="grid grid-cols-4 gap-2.5">
                            {Array.from({ length: Math.min(quickCount, countMax) }, (_, index) => index + 1).map((value) => (
                                <OptionPill key={value} selected={count === value} theme={theme} onClick={() => onConfigChange("count", String(value))}>
                                    {t("settingsPanels.image.images", { count: value })}
                                </OptionPill>
                            ))}
                            <CountInput value={count} max={countMax} theme={theme} onChange={(value) => onConfigChange("count", String(value || 1))} />
                        </div>
                    </div>
                ) : null}
            </div>
        </ImageSettingsTheme>
    );
}

export function ImageSettingsTheme({ theme, children }: { theme: CanvasTheme; children: ReactNode }) {
    return (
        <ConfigProvider
            theme={{
                token: { colorBgContainer: theme.toolbar.panel, colorBgElevated: theme.toolbar.panel, colorBorder: theme.node.stroke, colorPrimary: theme.node.activeStroke, colorText: theme.node.text, colorTextLightSolid: theme.node.panel },
                components: {
                    Button: { defaultBg: theme.toolbar.panel, defaultBorderColor: theme.node.stroke, defaultColor: theme.node.text },
                    Slider: { railBg: theme.node.stroke, railHoverBg: theme.node.stroke, trackBg: theme.node.activeStroke, handleColor: theme.node.text, handleActiveColor: theme.node.text },
                },
            }}
        >
            {children}
        </ConfigProvider>
    );
}

export function imageQualityLabel(value: string) {
    return qualityLabel(value);
}

function qualityLabel(value: string) {
    return ["auto", "high", "medium", "low"].includes(value) ? i18n.t(`settingsPanels.common.${value}`) : value;
}

export function imageSizeLabel(size: string) {
    if (!size || size === "auto") return i18n.t("settingsPanels.common.auto");
    return size;
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

function CountInput({ value, max, theme, onChange }: { value: number; max: number; theme: CanvasTheme; onChange: (value: number | null) => void }) {
    return (
        <label className="col-span-2 flex h-9 overflow-hidden rounded-full border text-sm" style={{ borderColor: theme.node.stroke, color: theme.node.text }}>
            <input
                type="number"
                min={1}
                max={max}
                className="min-w-0 flex-1 bg-transparent px-3 text-center outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                style={{ color: theme.node.text, WebkitTextFillColor: theme.node.text }}
                value={value || ""}
                onChange={(event) => onChange(Math.max(1, Math.min(max, Number(event.target.value) || 1)))}
                onMouseDown={(event) => event.stopPropagation()}
            />
        </label>
    );
}

function AspectIcon({ ratio, color }: { ratio: string; color: string }) {
    const parsed = parsePixelSize(ratio) || parseAspectRatio(ratio);
    if (!parsed) return null;
    const value = parsed.width / parsed.height;
    const boxWidth = value >= 1 ? 24 : Math.max(10, 24 * value);
    const boxHeight = value >= 1 ? Math.max(10, 24 / value) : 24;
    return (
        <span className="grid h-7 w-9 place-items-center">
            <span className="border-2" style={{ width: boxWidth, height: boxHeight, borderColor: color }} />
        </span>
    );
}

function SettingTitle({ children, color }: { children: string; color: string }) {
    return (
        <div className="text-xs font-medium" style={{ color }}>
            {children}
        </div>
    );
}
