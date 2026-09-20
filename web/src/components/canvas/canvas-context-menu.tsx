import { useEffect } from "react";
import type { ReactNode } from "react";
import { BetweenHorizontalStart, GalleryHorizontalEnd, GalleryHorizontal, Group, Plus, Trash2, Ungroup } from "lucide-react";
import { useTranslation } from "react-i18next";

import { canvasThemes } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import type { ContextMenuState } from "@/types/canvas";
import type { VideoFramePosition } from "@/lib/canvas/canvas-video-frame";

/**
 * 画布节点右键菜单：视频节点追加「取帧」组（首帧/末帧/当前帧），
 * 节点菜单提供分组/取消分组/复制/删除。fixed 定位在右键坐标处。
 */
export function CanvasNodeContextMenu({ menu, canCaptureVideoFrame, canGroup, canUngroup, onClose, onCaptureVideoFrame, onDuplicate, onGroup, onUngroup, onDelete }: { menu: ContextMenuState; canCaptureVideoFrame: boolean; canGroup?: boolean; canUngroup?: boolean; onClose: () => void; onCaptureVideoFrame: (position: VideoFramePosition) => void; onDuplicate: () => void; onGroup?: () => void; onUngroup?: () => void; onDelete: () => void }) {
    const { t } = useTranslation();
    const theme = canvasThemes[useThemeStore((state) => state.theme)];

    // 点击菜单外即关闭；antd 弹层（如 Tooltip）内的点击不关闭，避免交互冲突。
    useEffect(() => {
        const close = (event: PointerEvent) => {
            const target = event.target;
            if (target instanceof Element && target.closest(".ant-popover")) return;
            onClose();
        };
        window.addEventListener("pointerdown", close);
        return () => window.removeEventListener("pointerdown", close);
    }, [onClose]);

    return (
        <div
            className="fixed z-[80] min-w-44 overflow-hidden rounded-xl border py-1 shadow-2xl"
            style={{ left: menu.x, top: menu.y, background: theme.toolbar.panel, borderColor: theme.toolbar.border, color: theme.node.text }}
            onPointerDown={(event) => event.stopPropagation()}
        >
            {canCaptureVideoFrame ? (
                <>
                    <MenuButton icon={<BetweenHorizontalStart className="size-4" />} label={t("canvas.videoFrames.first")} onClick={() => onCaptureVideoFrame("first")} />
                    <MenuButton icon={<GalleryHorizontalEnd className="size-4" />} label={t("canvas.videoFrames.last")} onClick={() => onCaptureVideoFrame("last")} />
                    <MenuButton icon={<GalleryHorizontal className="size-4" />} label={t("canvas.videoFrames.current")} onClick={() => onCaptureVideoFrame("current")} />
                    <div className="my-1 border-t" style={{ borderColor: theme.toolbar.border }} />
                </>
            ) : null}
            {menu.type === "node" && canGroup ? <MenuButton icon={<Group className="size-4" />} label={t("canvas.nodeToolbar.group")} onClick={onGroup} /> : null}
            {menu.type === "node" && canUngroup ? <MenuButton icon={<Ungroup className="size-4" />} label={t("canvas.nodeToolbar.ungroup")} onClick={onUngroup} /> : null}
            {menu.type === "node" ? <MenuButton icon={<Plus className="size-4" />} label={t("canvas.controls.duplicate")} onClick={onDuplicate} /> : null}
            <MenuButton icon={<Trash2 className="size-4" />} label={t("canvas.controls.delete")} onClick={onDelete} danger />
        </div>
    );
}

/** 菜单项按钮：整行宽、图标 + 文字，danger 时红字。 */
function MenuButton({ icon, label, onClick, danger = false }: { icon: ReactNode; label: string; onClick?: () => void; danger?: boolean }) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];

    return (
        <button type="button" className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors hover:opacity-80" style={{ color: danger ? "#f87171" : theme.node.text }} onClick={onClick}>
            {icon}
            <span>{label}</span>
        </button>
    );
}
