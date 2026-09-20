import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { Button, Card, Checkbox, Form, Modal, Space, Switch, Tag, Tooltip, Typography, theme as antdTheme } from "antd";
import { Ellipsis, Image as ImageIcon, Settings2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import type { ImageQuickToolId } from "./canvas-image-toolbar-tools";

/** 工具设置弹窗中展示的工具条目（由宿主从工具注册表解析后传入）。 */
export type ImageToolbarSettingsTool = {
    id: ImageQuickToolId;
    title: string;
    label: string;
    icon: ReactNode;
    active?: boolean;
    danger?: boolean;
};

/** 预览工具条条目：普通工具 + 末尾固定的「更多」占位。 */
type PreviewTool = ImageToolbarSettingsTool | {
    id: "more";
    title: string;
    label: string;
    icon: ReactNode;
    active?: boolean;
    danger?: boolean;
};

/** 预览工具条的横向滚动状态，用于驱动自定义滚动条。 */
type PreviewScroll = {
    left: number;
    max: number; // 最大可滚动距离。
    viewport: number;
    content: number;
};

/**
 * 图片快捷工具条自定义弹窗：上半部分实时预览工具条外观（含自定义横向滚动条），
 * 下半部分用复选框勾选要显示的工具。选中状态由父层持有，onToggle 逐项回传。
 */
export function ImageToolSettingsModal({
    open,
    tools,
    selectedIds,
    showLabels,
    onToggle,
    onShowLabelsChange,
    onCancel,
    onSave,
}: {
    open: boolean;
    tools: ImageToolbarSettingsTool[];
    selectedIds: ImageQuickToolId[];
    showLabels: boolean;
    onToggle: (id: ImageQuickToolId, visible: boolean) => void;
    onShowLabelsChange: (value: boolean) => void;
    onCancel: () => void;
    onSave: () => void;
}) {
    const { t } = useTranslation();
    const { token } = antdTheme.useToken();
    const previewToolbarRef = useRef<HTMLDivElement>(null);
    const scrollbarTrackRef = useRef<HTMLInputElement>(null);
    const [previewScroll, setPreviewScroll] = useState<PreviewScroll>({ left: 0, max: 0, viewport: 1, content: 1 });
    const selected = useMemo(() => new Set(selectedIds), [selectedIds]);
    const selectedTools = tools.filter((tool) => selected.has(tool.id));
    const previewTools: PreviewTool[] = [
        ...selectedTools,
        { id: "more", title: t("canvas.imageTools.configure"), label: t("canvas.imageTools.more"), icon: <Ellipsis className="size-4" />, active: true },
    ];

    // 从预览工具条 DOM 读取当前滚动位置与宽度，同步到自定义滚动条。
    const syncPreviewScroll = useCallback(() => {
        const toolbar = previewToolbarRef.current;
        if (!toolbar) return;
        setPreviewScroll({
            left: toolbar.scrollLeft,
            max: Math.max(0, toolbar.scrollWidth - toolbar.clientWidth),
            viewport: Math.max(1, toolbar.clientWidth),
            content: Math.max(1, toolbar.scrollWidth),
        });
    }, []);

    const setPreviewScrollLeft = useCallback(
        (left: number) => {
            const toolbar = previewToolbarRef.current;
            if (!toolbar) return;
            toolbar.scrollLeft = left;
            syncPreviewScroll();
        },
        [syncPreviewScroll],
    );

    // 勾选变化时逐项 diff，只对发生变化的工具回传 onToggle。
    const updateSelectedTools = (values: ImageQuickToolId[]) => {
        const next = new Set(values);
        tools.forEach((tool) => {
            const visible = next.has(tool.id);
            if (selected.has(tool.id) !== visible) onToggle(tool.id, visible);
        });
    };

    // 预览条在弹窗动画结束、图标字体/布局就绪后宽度才稳定，因此多重兜底：
    // 连续两帧 + 120ms 定时 + ResizeObserver（观察工具条与其子元素）+ 窗口 resize。
    useEffect(() => {
        if (!open) return;
        const toolbar = previewToolbarRef.current;
        const sync = () => syncPreviewScroll();
        const frames: number[] = [];
        const firstFrame = window.requestAnimationFrame(() => {
            sync();
            frames.push(window.requestAnimationFrame(sync));
        });
        frames.push(firstFrame);
        const timer = window.setTimeout(sync, 120);
        const resizeObserver = typeof ResizeObserver !== "undefined" && toolbar ? new ResizeObserver(sync) : null;
        if (resizeObserver && toolbar) {
            resizeObserver.observe(toolbar);
            toolbar.childNodes.forEach((child) => {
                if (child instanceof Element) resizeObserver.observe(child);
            });
        }
        sync();
        window.addEventListener("resize", syncPreviewScroll);
        return () => {
            frames.forEach((frame) => window.cancelAnimationFrame(frame));
            window.clearTimeout(timer);
            resizeObserver?.disconnect();
            window.removeEventListener("resize", syncPreviewScroll);
        };
    }, [open, selectedIds, showLabels, previewTools.length, syncPreviewScroll]);

    // 自定义滚动条宽度按「视口/内容」比例计算，最小 64px 保证可拖拽。
    const scrollbarWidth = scrollbarTrackRef.current?.clientWidth || previewScroll.viewport;
    const scrollbarThumbWidth = previewScroll.max > 0 ? Math.min(scrollbarWidth, Math.max(64, (previewScroll.viewport / previewScroll.content) * scrollbarWidth)) : scrollbarWidth;

    return (
        <Modal
            title={t("canvas.imageTools.customize")}
            open={open}
            centered
            width={760}
            onCancel={onCancel}
            destroyOnHidden
            footer={
                <div className="flex items-center justify-between gap-3">
                    <div className="flex items-center gap-2">
                        <span>{t("canvas.imageTools.showLabels")}</span>
                        <Switch checked={showLabels} onChange={onShowLabelsChange} />
                    </div>
                    <Space>
                        <Button onClick={onCancel}>{t("common.cancel")}</Button>
                        <Button type="primary" onClick={onSave}>
                            {t("common.save")}
                        </Button>
                    </Space>
                </div>
            }
        >
            <Typography.Paragraph type="secondary" className="!mb-4">
                {t("canvas.imageTools.description")}
            </Typography.Paragraph>

            <Card
                size="small"
                title={
                    <Space size={6}>
                        <Settings2 className="size-4" />
                        {t("canvas.imageTools.preview")}
                    </Space>
                }
                className="mb-4"
            >
                <div className="relative flex min-h-[300px] w-full justify-center pt-20 pb-9">
                    <div
                        ref={previewToolbarRef}
                        className="hide-scrollbar absolute left-2 right-2 top-3 z-10 flex h-12 items-center overflow-x-auto rounded-[18px] border px-1 text-[13px]"
                        style={{ background: token.colorBgElevated, borderColor: token.colorBorderSecondary, boxShadow: token.boxShadowSecondary, color: token.colorText }}
                        onScroll={syncPreviewScroll}
                    >
                        {previewTools.map((tool) => (
                            <PreviewToolbarItem key={tool.id} tool={tool} showLabels={showLabels} />
                        ))}
                    </div>
                    <div
                        className="flex h-48 w-full max-w-[360px] flex-col items-center justify-center rounded-xl border"
                        style={{ background: token.colorFillAlter, borderColor: token.colorBorderSecondary, color: token.colorTextSecondary }}
                    >
                        <ImageIcon className="mb-2 size-8" />
                        <Typography.Text type="secondary">{t("canvas.imageTools.imageNode")}</Typography.Text>
                    </div>
                    {/* 自定义横向滚动条：借用 range input 的拖拽语义驱动预览条 scrollLeft。 */}
                    <input
                        ref={scrollbarTrackRef}
                        type="range"
                        min={0}
                        max={Math.max(previewScroll.max, 1)}
                        value={Math.min(previewScroll.left, Math.max(previewScroll.max, 1))}
                        disabled={previewScroll.max <= 0}
                        className="absolute bottom-4 left-10 right-10 h-2.5 cursor-pointer appearance-none bg-transparent disabled:cursor-default [&::-moz-range-thumb]:h-2.5 [&::-moz-range-thumb]:w-[var(--preview-scrollbar-thumb-width)] [&::-moz-range-thumb]:rounded-full [&::-moz-range-thumb]:border-0 [&::-moz-range-thumb]:bg-[#8d9498] [&::-moz-range-track]:h-2.5 [&::-moz-range-track]:rounded-full [&::-moz-range-track]:bg-[#bdc4c8] [&::-webkit-slider-runnable-track]:h-2.5 [&::-webkit-slider-runnable-track]:rounded-full [&::-webkit-slider-runnable-track]:bg-[#bdc4c8] [&::-webkit-slider-thumb]:h-2.5 [&::-webkit-slider-thumb]:w-[var(--preview-scrollbar-thumb-width)] [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-[#8d9498]"
                        style={{ "--preview-scrollbar-thumb-width": `${scrollbarThumbWidth}px` } as CSSProperties}
                        onInput={(event) => setPreviewScrollLeft(Number(event.currentTarget.value))}
                        onChange={(event) => setPreviewScrollLeft(Number(event.target.value))}
                    />
                </div>
            </Card>

            <Form layout="vertical" className="!mb-0">
                <Form.Item
                    className="!mb-4"
                    label={
                        <Space size={8}>
                            <span>{t("canvas.imageTools.quickTools")}</span>
                            <Tag className="m-0">
                                {selectedTools.length}/{tools.length}
                            </Tag>
                        </Space>
                    }
                >
                    <Checkbox.Group value={selectedIds} className="grid w-full gap-3 md:grid-cols-3" onChange={(values) => updateSelectedTools(values as ImageQuickToolId[])}>
                        {tools.map((tool) => (
                            <Checkbox key={tool.id} value={tool.id} className="m-0">
                                <span className="inline-flex items-center gap-2">
                                    {tool.icon}
                                    {tool.label}
                                </span>
                            </Checkbox>
                        ))}
                    </Checkbox.Group>
                </Form.Item>
            </Form>
        </Modal>
    );
}

/** 预览工具条的单个条目：仅展示（Tooltip + 图标/文字），不可交互。 */
function PreviewToolbarItem({ tool, showLabels }: { tool: PreviewTool; showLabels: boolean }) {
    return (
        <Tooltip title={tool.title}>
            <span className="flex h-12 shrink-0 items-center px-1.5" style={{ color: tool.danger ? "#ef4444" : undefined }}>
                <span className={`flex h-9 items-center rounded-lg px-2 ${showLabels ? "gap-2" : "justify-center"}`}>
                    {tool.icon}
                    {showLabels ? <span className="whitespace-nowrap">{tool.label}</span> : null}
                </span>
            </span>
        </Tooltip>
    );
}
