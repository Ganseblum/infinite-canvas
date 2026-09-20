import React, { useEffect, useRef, useState } from "react";

import { canvasThemes, type CanvasBackgroundMode } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import type { ViewportTransform } from "@/types/canvas";

/**
 * InfiniteCanvas 的 props。
 * 本组件只负责视口容器：平移/缩放/背景网格，节点与连线由 children 在世界坐标系内渲染。
 */
type InfiniteCanvasProps = {
    containerRef: React.RefObject<HTMLDivElement | null>; // 画布容器 ref，滚轮/指针事件基于它的 rect 换算坐标。
    viewport: ViewportTransform; // 当前视口（位移 x/y + 缩放 k），由父层持有。
    tool: "select" | "pan"; // 当前激活工具；按住 Ctrl/空格可临时反向切换。
    backgroundMode?: CanvasBackgroundMode; // 背景网格样式：点阵/线条/空白。
    onViewportChange: (viewport: ViewportTransform) => void;
    onCanvasMouseDown?: (event: React.PointerEvent<HTMLDivElement>) => void; // 点在空白背景（非节点/连线）时触发，用于框选等。
    onCanvasDeselect?: () => void; // 背景按下且未发生拖拽（点击空白）时触发，用于取消选中。
    onCanvasDoubleClick?: (event: React.MouseEvent<HTMLDivElement>) => void; // 双击空白背景时触发。
    onContextMenu?: (event: React.MouseEvent) => void;
    onDrop?: (event: React.DragEvent<HTMLDivElement>) => void; // 拖入文件/资源时触发。
    children: React.ReactNode;
};

/**
 * 无限画布视口容器：负责背景网格、滚轮以鼠标为锚点缩放、拖拽平移与临时工具切换。
 * 平移状态放 ref 中避免高频 setState；视口更新经 requestAnimationFrame 合帧，
 * 空格键临时平移、Ctrl 键临时选择（与当前工具互为反转）。
 */
export function InfiniteCanvas({ containerRef, viewport, tool, backgroundMode = "lines", onViewportChange, onCanvasMouseDown, onCanvasDeselect, onCanvasDoubleClick, onContextMenu, onDrop, children }: InfiniteCanvasProps) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    // 平移拖拽的起点数据放 ref，mousemove 高频更新不触发重渲染。
    const panState = useRef({
        isPanning: false,
        startX: 0,
        startY: 0,
        initialX: 0,
        initialY: 0,
        hasMoved: false,
        startedOnBackground: false,
    });
    const scaleRef = useRef(viewport.k);
    // 平移视口更新合帧：move 事件只写 nextViewportRef，由 rAF 回调统一提交。
    const frameRef = useRef<number | null>(null);
    const nextViewportRef = useRef<ViewportTransform | null>(null);
    const [isSpacePressed, setIsSpacePressed] = useState(false);
    const [isControlPressed, setIsControlPressed] = useState(false);
    const [isPanning, setIsPanning] = useState(false);

    useEffect(() => {
        scaleRef.current = viewport.k;
    }, [viewport.k]);

    // 卸载时取消未落地的 rAF，避免对已卸载组件回调 onViewportChange。
    useEffect(
        () => () => {
            if (frameRef.current) cancelAnimationFrame(frameRef.current);
        },
        [],
    );

    // 空格 = 临时平移、Ctrl = 临时选择；焦点在输入框/可编辑元素内时不拦截空格默认行为。
    useEffect(() => {
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key === "Control") setIsControlPressed(true);
            if (event.code !== "Space") return;
            const target = event.target instanceof Element ? event.target : null;
            if (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || event.target instanceof HTMLSelectElement || target?.closest("[contenteditable='true']")) return;
            event.preventDefault();
            setIsSpacePressed(true);
        };

        const handleKeyUp = (event: KeyboardEvent) => {
            if (event.code === "Space") {
                const target = event.target instanceof Element ? event.target : null;
                if (!(event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || event.target instanceof HTMLSelectElement || target?.closest("[contenteditable='true']"))) event.preventDefault();
                setIsSpacePressed(false);
            }
            if (event.key === "Control") setIsControlPressed(false);
        };

        // 窗口失焦时按键状态可能收不到 keyup，直接复位，避免工具卡在临时态/拖拽态。
        const handleBlur = () => {
            setIsSpacePressed(false);
            setIsControlPressed(false);
            panState.current.isPanning = false;
            setIsPanning(false);
            document.body.style.cursor = "";
        };

        window.addEventListener("keydown", handleKeyDown);
        window.addEventListener("keyup", handleKeyUp);
        window.addEventListener("blur", handleBlur);
        return () => {
            window.removeEventListener("keydown", handleKeyDown);
            window.removeEventListener("keyup", handleKeyUp);
            window.removeEventListener("blur", handleBlur);
        };
    }, []);

    const handleWheel = (event: React.WheelEvent<HTMLDivElement>) => {
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest("[data-canvas-no-zoom],.ant-modal,.ant-popover,.ant-dropdown,.ant-select-dropdown,.ant-picker-dropdown")) return;

        // Firefox 等浏览器的滚轮事件按行（deltaMode=1）或页（deltaMode=2）上报，先归一化成像素再算缩放系数。
        const deltaUnit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? 100 : 1;
        const delta = -event.deltaY * deltaUnit;
        const factor = Math.pow(1.1, delta / 100);
        // 缩放范围限制在 5% ~ 500%，防止缩到不可见或过大。
        const newScale = Math.min(Math.max(viewport.k * factor, 0.05), 5);
        const rect = containerRef.current?.getBoundingClientRect();
        if (!rect) return;

        // 以鼠标位置为锚点缩放：先算鼠标下的世界坐标，再反推新视口位移，保证缩放前后该点仍在鼠标下。
        const mouseX = event.clientX - rect.left;
        const mouseY = event.clientY - rect.top;
        const worldX = (mouseX - viewport.x) / viewport.k;
        const worldY = (mouseY - viewport.y) / viewport.k;

        onViewportChange({
            x: mouseX - worldX * newScale,
            y: mouseY - worldY * newScale,
            k: newScale,
        });
    };

    const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest("[data-canvas-no-zoom]")) return;
        if (target?.closest("[data-connection-create-menu]")) return;
        const isBackgroundClick = !target?.closest("[data-node-id],[data-connection-id]");
        // Ctrl/空格按住时临时反转当前工具：选择工具下变平移，平移工具下变选择。
        const temporaryTool = event.ctrlKey || isSpacePressed;
        const activeTool = temporaryTool ? (tool === "select" ? "pan" : "select") : tool;
        // 中键任意位置可平移；左键仅在背景处且激活平移工具时平移。
        const shouldPan = event.button === 1 || (event.button === 0 && activeTool === "pan" && isBackgroundClick);

        if (shouldPan) {
            event.preventDefault();
            event.currentTarget.setPointerCapture(event.pointerId);
            panState.current = {
                isPanning: true,
                startX: event.clientX,
                startY: event.clientY,
                initialX: viewport.x,
                initialY: viewport.y,
                hasMoved: false,
                startedOnBackground: isBackgroundClick,
            };
            setIsPanning(true);
            document.body.style.cursor = "grabbing";
            return;
        }

        if (event.button === 0 && isBackgroundClick) {
            event.preventDefault();
            // 捕获指针，保证按住拖出容器后仍能收到 move/up 事件完成框选。
            event.currentTarget.setPointerCapture(event.pointerId);
            onCanvasMouseDown?.(event);
        }
    };

    const handleDoubleClick = (event: React.MouseEvent<HTMLDivElement>) => {
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest("[data-canvas-no-zoom],[data-node-id],[data-connection-id]")) return;
        onCanvasDoubleClick?.(event);
    };

    useEffect(() => {
        const handlePointerMove = (event: PointerEvent) => {
            if (!panState.current.isPanning) return;

            const dx = event.clientX - panState.current.startX;
            const dy = event.clientY - panState.current.startY;
            // 位移超过 3px 才算真正拖动，避免误把点击当拖拽而跳过取消选中。
            if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
                panState.current.hasMoved = true;
            }

            nextViewportRef.current = {
                x: panState.current.initialX + dx,
                y: panState.current.initialY + dy,
                k: scaleRef.current,
            };
            if (frameRef.current) return;
            frameRef.current = requestAnimationFrame(() => {
                frameRef.current = null;
                if (nextViewportRef.current) onViewportChange(nextViewportRef.current);
            });
        };

        const handlePointerUp = () => {
            if (!panState.current.isPanning) return;

            // 背景上按下且没拖动 = 纯点击空白，触发取消选中。
            if (!panState.current.hasMoved && panState.current.startedOnBackground) {
                onCanvasDeselect?.();
            }
            panState.current.isPanning = false;
            setIsPanning(false);
            document.body.style.cursor = "";
        };

        window.addEventListener("pointermove", handlePointerMove);
        window.addEventListener("pointerup", handlePointerUp);
        window.addEventListener("pointercancel", handlePointerUp);
        return () => {
            window.removeEventListener("pointermove", handlePointerMove);
            window.removeEventListener("pointerup", handlePointerUp);
            window.removeEventListener("pointercancel", handlePointerUp);
            document.body.style.cursor = "";
        };
    }, [onCanvasDeselect, onViewportChange]);

    useEffect(() => {
        const container = containerRef.current;
        if (!container) return;

        // Prevent canvas scrolling from moving the page while preserving native scrolling inside overlays and dialogs.
        // 阻止画布区域滚轮滚动页面，弹层（antd Modal/Popover 等）内的原生滚动不受影响。
        const preventWheelScroll = (event: WheelEvent) => {
            const target = event.target instanceof Element ? event.target : null;
            if (target?.closest("[data-canvas-no-zoom],.ant-modal,.ant-popover,.ant-dropdown,.ant-select-dropdown,.ant-picker-dropdown")) return;
            event.preventDefault();
        };
        container.addEventListener("wheel", preventWheelScroll, { passive: false });
        return () => container.removeEventListener("wheel", preventWheelScroll);
    }, [containerRef]);

    const temporaryTool = isControlPressed || isSpacePressed;
    const activeTool = temporaryTool ? (tool === "select" ? "pan" : "select") : tool;
    const cursor = isPanning ? "grabbing" : activeTool === "pan" ? "grab" : undefined;

    return (
        <div
            ref={containerRef}
            className="relative h-full w-full select-none overflow-hidden"
            style={{ background: theme.canvas.background, cursor }}
            onPointerDown={handlePointerDown}
            onDoubleClick={handleDoubleClick}
            onWheel={handleWheel}
            onContextMenu={onContextMenu}
            onDragOver={(event) => event.preventDefault()}
            onDrop={onDrop}
        >
            <CanvasGrid viewport={viewport} mode={backgroundMode} />
            {/* 世界坐标系容器：children（节点/连线）统一随视口平移缩放。 */}
            <div
                className="absolute origin-top-left"
                style={{
                    transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.k})`,
                }}
            >
                {children}
            </div>
        </div>
    );
}

/**
 * 画布背景网格：点阵或线条，随视口平移缩放。
 * 网格尺寸按视口缩放换算，仅调整 backgroundPosition/Size 实现无限平铺。
 */
function CanvasGrid({ viewport, mode }: { viewport: ViewportTransform; mode: CanvasBackgroundMode }) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    if (mode === "blank") return null;

    // 网格间距固定 48px 世界单位，乘以缩放得屏幕像素；取模让网格随视口平移滚动。
    const gridSize = 48 * viewport.k;
    const x = viewport.x % gridSize;
    const y = viewport.y % gridSize;
    // 缩得很小时缩小点径，避免点阵糊成一片。
    const dotSize = viewport.k < 0.12 ? 0.8 : 1.15;
    const backgroundImage =
        mode === "dots" ? `radial-gradient(circle, ${theme.canvas.dot} ${dotSize}px, transparent ${dotSize + 0.2}px)` : `linear-gradient(${theme.canvas.line} 1px, transparent 1px), linear-gradient(90deg, ${theme.canvas.line} 1px, transparent 1px)`;

    return (
        <div
            className="pointer-events-none absolute inset-0 opacity-40"
            style={{
                backgroundImage,
                backgroundSize: `${gridSize}px ${gridSize}px`,
                backgroundPosition: `${x}px ${y}px`,
            }}
        />
    );
}
