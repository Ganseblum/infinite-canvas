import type { ReactNode } from "react";
import { Group, Ungroup } from "lucide-react";
import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";

import { canvasThemes } from "@/lib/canvas-theme";
import { nodeBounds } from "@/lib/canvas/canvas-node-geometry";
import { useThemeStore } from "@/stores/use-theme-store";
import type { CanvasNodeData, ViewportTransform } from "@/types/canvas";

// 多选框在节点包围盒外扩的像素距离，避免贴着节点边缘。
const SELECTION_PAD = 14;

/**
 * 多选高亮与批量操作工具条：按选中节点包围盒画虚线圆角框，
 * 框上方悬浮分组/取消分组按钮（仅具备对应能力时显示）。
 * 框与工具条都挂在屏幕坐标上（世界坐标 × 视口缩放换算）。
 */
export function CanvasSelectionToolbar({
    nodes,
    viewport,
    showToolbar,
    canGroup,
    canUngroup,
    onGroup,
    onUngroup,
}: {
    nodes: CanvasNodeData[];
    viewport: ViewportTransform;
    showToolbar: boolean;
    canGroup: boolean;
    canUngroup: boolean;
    onGroup: () => void;
    onUngroup: () => void;
}) {
    const { t } = useTranslation();
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    if (nodes.length < 2) return null;

    const bounds = nodeBounds(nodes);
    const left = viewport.x + bounds.left * viewport.k - SELECTION_PAD;
    const top = viewport.y + bounds.top * viewport.k - SELECTION_PAD;
    const width = (bounds.right - bounds.left) * viewport.k + SELECTION_PAD * 2;
    const height = (bounds.bottom - bounds.top) * viewport.k + SELECTION_PAD * 2;
    const showActions = showToolbar && (canGroup || canUngroup);

    return (
        <>
            {/* 多选包围框：SVG 虚线矩形，不响应指针。 */}
            <svg className="pointer-events-none absolute z-[65] overflow-visible" style={{ left, top, width, height }}>
                <rect
                    x={1}
                    y={1}
                    width={Math.max(width - 2, 0)}
                    height={Math.max(height - 2, 0)}
                    rx={16}
                    ry={16}
                    fill={theme.canvas.selectionFill}
                    stroke={theme.canvas.selectionStroke}
                    strokeOpacity={0.55}
                    strokeWidth={1.5}
                    strokeDasharray="7 5"
                    strokeLinecap="round"
                />
            </svg>
            {/* 悬浮操作条：固定白色浅色风格（跨主题一致），阻断指针冒泡防止拖动选区。 */}
            {showActions ? (
                <div
                    className="absolute z-[70] flex h-12 -translate-x-1/2 -translate-y-full items-center overflow-visible rounded-[18px] border border-black/10 bg-white text-[15px] text-[#242529] shadow-[0_8px_28px_rgba(15,23,42,.12)]"
                    style={{ left: left + width / 2, top: top - 8 }}
                    onMouseDown={(event) => event.stopPropagation()}
                    onPointerDown={(event) => event.stopPropagation()}
                >
                    {canGroup ? <SelectionAction title={t("canvas.nodeToolbar.groupTitle")} label={t("canvas.nodeToolbar.group")} icon={<Group className="size-4" />} onClick={onGroup} /> : null}
                    {canUngroup ? <SelectionAction title={t("canvas.nodeToolbar.ungroupTitle")} label={t("canvas.nodeToolbar.ungroup")} icon={<Ungroup className="size-4" />} onClick={onUngroup} /> : null}
                </div>
            ) : null}
        </>
    );
}

/** 工具条按钮：图标 + 文字，悬停浅色反馈，Tooltip 固定白底。 */
function SelectionAction({ title, label, icon, onClick }: { title: string; label: string; icon: ReactNode; onClick: () => void }) {
    return (
        <Tooltip title={title} placement="top" mouseEnterDelay={0.2} color="#ffffff" styles={{ root: { color: "#242529", boxShadow: "0 8px 24px rgba(15,23,42,.16)", fontSize: 13, fontWeight: 500 } }}>
            <button type="button" className="group relative flex h-12 items-center whitespace-nowrap px-1.5" onClick={onClick} aria-label={title}>
                <span className="flex h-9 items-center gap-2 rounded-lg px-2.5 transition group-hover:bg-[#f0f0f1]">
                    {icon}
                    <span>{label}</span>
                </span>
            </button>
        </Tooltip>
    );
}
