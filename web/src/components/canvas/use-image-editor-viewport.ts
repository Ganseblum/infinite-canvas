import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from "react";

/** 媒体查看器的宽高描述。 */
type ImageSize = { width: number; height: number };

// 缩放边界与步进：1x ~ 4x，每次乘除 1.2；四周留 16px 呼吸边距。
const minZoom = 1;
const maxZoom = 4;
const zoomStep = 1.2;
const viewportPadding = 16;

/**
 * 图片编辑器（遮罩裁剪等弹窗）的视口 hook：管理适应窗口的基准尺寸、
 * 1x~4x 滚轮/按钮缩放（以鼠标为锚点）、空格+左键或中键拖拽平移。
 * 缩放锚点先记录，再在 useLayoutEffect 中于 DOM 更新后校正滚动位置，
 * 保证缩放前后指针指向的图像点保持不动。
 */
export function useImageEditorViewport(image: ImageSize | null, open: boolean) {
    const viewportNodeRef = useRef<HTMLDivElement>(null);
    const stageRef = useRef<HTMLDivElement>(null);
    const panRef = useRef<{ pointerId: number; x: number; y: number; scrollLeft: number; scrollTop: number } | null>(null);
    const zoomAnchorRef = useRef<{ zoom: number; ratioX: number; ratioY: number; viewportX: number; viewportY: number } | null>(null);
    const [viewportElement, setViewportElement] = useState<HTMLDivElement | null>(null);
    const [viewportSize, setViewportSize] = useState<ImageSize>({ width: 0, height: 0 });
    const [zoom, setZoom] = useState(minZoom);
    const [isPanning, setIsPanning] = useState(false);
    const [spacePressed, setSpacePressed] = useState(false);
    // keydown/keyup 之外（如失焦）也要能读到最新空格状态，用 ref 双写。
    const spacePressedRef = useRef(false);
    // callback ref：同时挂到 DOM ref 和 state 上，挂载后触发 ResizeObserver 等依赖 viewportElement 的 effect。
    const viewportRef = useCallback((node: HTMLDivElement | null) => {
        viewportNodeRef.current = node;
        setViewportElement(node);
    }, []);

    // 弹窗打开或图片尺寸变化时复位缩放与锚点。
    useEffect(() => {
        if (!open) return;
        zoomAnchorRef.current = null;
        setZoom(minZoom);
    }, [open, image?.width, image?.height]);

    // 捕获阶段拦截空格：进入平移模式，并移除焦点避免空格触发按钮点击；blur 时复位防卡键。
    useEffect(() => {
        if (!open) return;
        const releaseSpace = () => {
            spacePressedRef.current = false;
            setSpacePressed(false);
        };
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.code !== "Space" || event.repeat) return;
            const target = event.target instanceof Element ? event.target : null;
            if (target?.closest("input,textarea,[contenteditable='true']")) return;
            event.preventDefault();
            event.stopPropagation();
            event.stopImmediatePropagation();
            if (document.activeElement instanceof HTMLElement && document.activeElement.matches("button,a,[role='button']")) document.activeElement.blur();
            spacePressedRef.current = true;
            setSpacePressed(true);
        };
        const handleKeyUp = (event: KeyboardEvent) => {
            if (event.code !== "Space" || !spacePressedRef.current) return;
            event.preventDefault();
            event.stopPropagation();
            event.stopImmediatePropagation();
            releaseSpace();
        };
        window.addEventListener("keydown", handleKeyDown, true);
        window.addEventListener("keyup", handleKeyUp, true);
        window.addEventListener("blur", releaseSpace);
        return () => {
            window.removeEventListener("keydown", handleKeyDown, true);
            window.removeEventListener("keyup", handleKeyUp, true);
            window.removeEventListener("blur", releaseSpace);
            spacePressedRef.current = false;
        };
    }, [open]);

    // 视口容器尺寸监听（窗口缩放/侧栏变化时重算适应尺寸）。
    useEffect(() => {
        if (!open || !viewportElement) return;
        const updateSize = () => {
            const width = viewportElement.clientWidth;
            const height = viewportElement.clientHeight;
            setViewportSize((current) => (current.width === width && current.height === height ? current : { width, height }));
        };
        updateSize();
        const observer = new ResizeObserver(updateSize);
        observer.observe(viewportElement);
        return () => observer.disconnect();
    }, [open, viewportElement]);

    // 基准尺寸 = 图片按比例缩到视口内的显示尺寸（不超过 1x）。
    const baseSize = fitImage(image, viewportSize);
    const stageSize = { width: baseSize.width * zoom, height: baseSize.height * zoom };
    // 滚动内容取「视口」与「放大后的舞台」较大者，保证放大后可平移。
    const contentSize = {
        width: Math.max(viewportSize.width, stageSize.width),
        height: Math.max(viewportSize.height, stageSize.height),
    };
    // 视口大于舞台时把舞台居中。
    const stageOffset = {
        left: Math.max(0, Math.round((contentSize.width - stageSize.width) / 2)),
        top: Math.max(0, Math.round((contentSize.height - stageSize.height) / 2)),
    };

    // 缩放落地后按锚点校正 scroll：把「锚点指向的图像点」滚回指针所在视口位置。
    useLayoutEffect(() => {
        const viewport = viewportNodeRef.current;
        const anchor = zoomAnchorRef.current;
        // 锚点与当前缩放不一致说明已被后续操作覆盖，跳过本次校正。
        if (!viewport || !anchor || Math.abs(anchor.zoom - zoom) > 0.001) return;
        const nextWidth = baseSize.width * zoom;
        const nextHeight = baseSize.height * zoom;
        const nextLeft = Math.max(0, (Math.max(viewport.clientWidth, nextWidth) - nextWidth) / 2);
        const nextTop = Math.max(0, (Math.max(viewport.clientHeight, nextHeight) - nextHeight) / 2);
        viewport.scrollLeft = nextLeft + anchor.ratioX * nextWidth - anchor.viewportX;
        viewport.scrollTop = nextTop + anchor.ratioY * nextHeight - anchor.viewportY;
        zoomAnchorRef.current = null;
    }, [baseSize.height, baseSize.width, zoom]);

    /**
     * 设置缩放并记录锚点：ratio 是指针在舞台内的相对位置，viewportXY 是指针在视口内的位置。
     * 不传指针坐标时以视口中心为锚点（按钮缩放）。
     */
    const setZoomAround = useCallback(
        (nextZoom: number, clientX?: number, clientY?: number) => {
            const viewport = viewportNodeRef.current;
            const stage = stageRef.current;
            if (!viewport || !stage || !baseSize.width || !baseSize.height) return;

            const boundedZoom = clamp(nextZoom, minZoom, maxZoom);
            // 变化小于阈值视为同一次缩放，不重复触发。
            if (Math.abs(boundedZoom - zoom) < 0.001) return;

            const viewportRect = viewport.getBoundingClientRect();
            const stageRect = stage.getBoundingClientRect();
            const pointerX = clientX ?? viewportRect.left + viewportRect.width / 2;
            const pointerY = clientY ?? viewportRect.top + viewportRect.height / 2;
            // 相对位置夹在 0~1，防止指针在舞台外时锚点溢出。
            const ratioX = clamp((pointerX - stageRect.left) / Math.max(1, stageRect.width), 0, 1);
            const ratioY = clamp((pointerY - stageRect.top) / Math.max(1, stageRect.height), 0, 1);
            const viewportX = pointerX - viewportRect.left;
            const viewportY = pointerY - viewportRect.top;

            zoomAnchorRef.current = { zoom: boundedZoom, ratioX, ratioY, viewportX, viewportY };
            setZoom(boundedZoom);
        },
        [baseSize.height, baseSize.width, zoom],
    );

    // 滚轮缩放：只允许在弹窗打开时接管，向上一档放大、向下一档缩小。
    useEffect(() => {
        if (!open || !viewportElement) return;
        const handleWheel = (event: WheelEvent) => {
            event.preventDefault();
            event.stopPropagation();
            setZoomAround(event.deltaY < 0 ? zoom * zoomStep : zoom / zoomStep, event.clientX, event.clientY);
        };
        viewportElement.addEventListener("wheel", handleWheel, { passive: false });
        return () => viewportElement.removeEventListener("wheel", handleWheel);
    }, [open, setZoomAround, viewportElement, zoom]);

    // 平移：中键或空格+左键开始；直接改 scrollLeft/Top，不经过 React 状态。
    const startPan = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
        if (event.button !== 1 && !(event.button === 0 && spacePressedRef.current)) return;
        event.preventDefault();
        event.stopPropagation();
        const viewport = event.currentTarget;
        panRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, scrollLeft: viewport.scrollLeft, scrollTop: viewport.scrollTop };
        viewport.setPointerCapture(event.pointerId);
        setIsPanning(true);
    }, []);
    const movePan = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
        const pan = panRef.current;
        // 只响应发起平移的那根指针，避免多指触控互相干扰。
        if (!pan || event.pointerId !== pan.pointerId) return;
        event.preventDefault();
        event.stopPropagation();
        event.currentTarget.scrollLeft = pan.scrollLeft - (event.clientX - pan.x);
        event.currentTarget.scrollTop = pan.scrollTop - (event.clientY - pan.y);
    }, []);
    const stopPan = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
        const pan = panRef.current;
        if (!pan || event.pointerId !== pan.pointerId) return;
        event.preventDefault();
        event.stopPropagation();
        if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
        panRef.current = null;
        setIsPanning(false);
    }, []);
    // 中键松开时吞掉默认行为，避免触发自动滚动。
    const preventAuxClick = useCallback((event: ReactMouseEvent<HTMLDivElement>) => {
        if (event.button !== 1) return;
        event.preventDefault();
        event.stopPropagation();
    }, []);

    return {
        viewportRef,
        stageRef,
        zoom,
        isPanning,
        spacePressed,
        // 放大后才允许滚动（平移），1x 时锁定视口。
        scrollClassName: zoom > minZoom + 0.001 ? "overflow-scroll" : "overflow-hidden",
        panHandlers: {
            onPointerDownCapture: startPan,
            onPointerMoveCapture: movePan,
            onPointerUpCapture: stopPan,
            onPointerCancelCapture: stopPan,
            onAuxClick: preventAuxClick,
        },
        canZoomIn: zoom < maxZoom,
        canZoomOut: zoom > minZoom,
        imageScale: image ? stageSize.width / image.width : 0, // 媒体元素实际相对原始尺寸的缩放比例。
        zoomIn: () => setZoomAround(zoom * zoomStep),
        zoomOut: () => setZoomAround(zoom / zoomStep),
        resetZoom: () => setZoomAround(minZoom),
        contentStyle: { width: contentSize.width, height: contentSize.height } satisfies CSSProperties,
        stageStyle: {
            left: stageOffset.left,
            top: stageOffset.top,
            width: stageSize.width,
            height: stageSize.height,
        } satisfies CSSProperties,
        mediaStyle: {
            width: baseSize.width,
            height: baseSize.height,
            transform: `translateZ(0) scale(${zoom})`,
            transformOrigin: "top left",
        } satisfies CSSProperties,
    };
}

/** 计算图片「适应视口」的显示尺寸：按比例缩到 padding 内且不放大超过 1x。 */
function fitImage(image: ImageSize | null, viewport: ImageSize): ImageSize {
    if (!image || !viewport.width || !viewport.height) return { width: 0, height: 0 };
    const availableWidth = Math.max(1, viewport.width - viewportPadding * 2);
    const availableHeight = Math.max(1, viewport.height - viewportPadding * 2);
    const scale = Math.min(availableWidth / image.width, availableHeight / image.height, 1);
    return { width: Math.max(1, Math.floor(image.width * scale)), height: Math.max(1, Math.floor(image.height * scale)) };
}

/** 数值夹取到 [min, max]。 */
function clamp(value: number, min: number, max: number) {
    return Math.min(max, Math.max(min, value));
}
