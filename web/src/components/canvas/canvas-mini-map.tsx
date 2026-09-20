import { useCallback, useMemo, useRef, useState } from "react";

import { canvasThemes } from "@/lib/canvas-theme";
import { getNodeDefinition } from "@/lib/canvas/node-registry";
import { useThemeStore } from "@/stores/use-theme-store";
import { type CanvasNodeData, type ViewportTransform } from "@/types/canvas";

/**
 * 画布小地图：把所有节点缩略渲染到左下角 240x160 的面板上，
 * 高亮当前视口矩形；点击/拖动可把视口中心跳到对应世界坐标。
 * 世界 → 小地图的映射由 worldBounds + scale + offset 决定，节点变化时自动适配。
 */
export function Minimap({ nodes, viewport, viewportSize, onViewportChange }: { nodes: CanvasNodeData[]; viewport: ViewportTransform; viewportSize: { width: number; height: number }; onViewportChange: (viewport: ViewportTransform) => void }) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const containerRef = useRef<HTMLDivElement>(null);
    const [isDragging, setIsDragging] = useState(false);
    const width = 240;
    const height = 160;

    // 计算世界包围盒（四周留 500px 余量）、适配缩放与居中偏移；无节点时给默认视野。
    const { worldBounds, scale, offset } = useMemo(() => {
        if (!nodes.length) {
            return { worldBounds: { x: -500, y: -500, w: 1000, h: 1000 }, scale: 0.16, offset: { x: 40, y: 0 } };
        }

        let minX = Infinity;
        let minY = Infinity;
        let maxX = -Infinity;
        let maxY = -Infinity;

        nodes.forEach((node) => {
            minX = Math.min(minX, node.position.x);
            minY = Math.min(minY, node.position.y);
            maxX = Math.max(maxX, node.position.x + node.width);
            maxY = Math.max(maxY, node.position.y + node.height);
        });

        minX -= 500;
        minY -= 500;
        maxX += 500;
        maxY += 500;

        // 取宽高比例中较小者，保证包围盒完整落入小地图。
        const boundsWidth = maxX - minX;
        const boundsHeight = maxY - minY;
        const nextScale = Math.min(width / boundsWidth, height / boundsHeight);
        const mapContentW = boundsWidth * nextScale;
        const mapContentH = boundsHeight * nextScale;

        return {
            worldBounds: { x: minX, y: minY, w: boundsWidth, h: boundsHeight },
            scale: nextScale,
            offset: { x: (width - mapContentW) / 2, y: (height - mapContentH) / 2 },
        };
    }, [nodes]);

    // 世界坐标 → 小地图像素坐标。
    const toMinimap = useCallback(
        (worldX: number, worldY: number) => {
            return {
                x: (worldX - worldBounds.x) * scale + offset.x,
                y: (worldY - worldBounds.y) * scale + offset.y,
            };
        },
        [offset.x, offset.y, scale, worldBounds.x, worldBounds.y],
    );

    // 小地图像素坐标 → 世界坐标。
    const toWorld = useCallback(
        (minimapX: number, minimapY: number) => {
            return {
                x: (minimapX - offset.x) / scale + worldBounds.x,
                y: (minimapY - offset.y) / scale + worldBounds.y,
            };
        },
        [offset.x, offset.y, scale, worldBounds.x, worldBounds.y],
    );

    // 当前视口在世界坐标系中的矩形（由视口位移/缩放反推），再映射到小地图。
    const viewportRect = useMemo(() => {
        const vx = -viewport.x / viewport.k;
        const vy = -viewport.y / viewport.k;
        const vw = viewportSize.width / viewport.k;
        const vh = viewportSize.height / viewport.k;
        const p1 = toMinimap(vx, vy);
        const p2 = toMinimap(vx + vw, vy + vh);

        return {
            x: p1.x,
            y: p1.y,
            // 最小 4px：缩得很远时矩形不至于看不见。
            w: Math.max(p2.x - p1.x, 4),
            h: Math.max(p2.y - p1.y, 4),
        };
    }, [toMinimap, viewport.k, viewport.x, viewport.y, viewportSize.height, viewportSize.width]);

    // 把点击/拖动的位置设为视口中心（保持当前缩放 k 不变）。
    const updateViewportFromEvent = (event: React.PointerEvent) => {
        const rect = containerRef.current?.getBoundingClientRect();
        if (!rect) return;

        const world = toWorld(event.clientX - rect.left, event.clientY - rect.top);
        onViewportChange({
            x: viewportSize.width / 2 - world.x * viewport.k,
            y: viewportSize.height / 2 - world.y * viewport.k,
            k: viewport.k,
        });
    };

    return (
        <div className="absolute bottom-24 left-6 z-50 overflow-hidden rounded-lg border shadow-2xl backdrop-blur-sm" style={{ width, height, background: theme.toolbar.panel, borderColor: theme.toolbar.border }}>
            <div
                ref={containerRef}
                className="relative h-full w-full cursor-crosshair"
                onPointerDown={(event) => {
                    event.preventDefault();
                    // 捕获指针，按下即可连续拖动视口。
                    event.currentTarget.setPointerCapture(event.pointerId);
                    setIsDragging(true);
                    updateViewportFromEvent(event);
                }}
                onPointerMove={(event) => {
                    if (isDragging) updateViewportFromEvent(event);
                }}
                onPointerUp={() => setIsDragging(false)}
                onPointerLeave={() => setIsDragging(false)}
            >
                {nodes.map((node) => {
                    const pos = toMinimap(node.position.x, node.position.y);
                    // 颜色优先用节点定义声明的 minimapColor（插件节点可自定义）。
                    const color = getNodeDefinition(node.type)?.minimapColor || theme.node.muted;
                    return (
                        <div
                            key={node.id}
                            className="absolute rounded-[1px]"
                            style={{
                                left: pos.x,
                                top: pos.y,
                                // 最小 2px：极小节点在小地图上仍可见。
                                width: Math.max(node.width * scale, 2),
                                height: Math.max(node.height * scale, 2),
                                backgroundColor: color,
                                opacity: 0.8,
                            }}
                        />
                    );
                })}
                {/* 当前视口高亮矩形（不可点击，穿透事件）。 */}
                <div className="pointer-events-none absolute border" style={{ left: viewportRect.x, top: viewportRect.y, width: viewportRect.w, height: viewportRect.h, borderColor: theme.node.activeStroke, background: `${theme.node.activeStroke}18` }} />
            </div>
        </div>
    );
}
