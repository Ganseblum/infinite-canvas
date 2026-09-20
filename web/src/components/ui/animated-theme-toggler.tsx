import { useCallback, useEffect, useRef, useState } from "react";
import { Moon, Sun } from "lucide-react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

/** 切换动画的几何形状变体，决定 View Transition 用的 clip-path 轮廓。 */
export type TransitionVariant = "circle" | "square" | "triangle" | "diamond" | "hexagon" | "rectangle" | "star";

/**
 * 主题切换按钮的属性：受控/非受控两种用法，
 * 不传 theme/targetTheme 时由组件自行读写 documentElement 的 dark class。
 */
interface AnimatedThemeTogglerProps extends React.ComponentPropsWithoutRef<"button"> {
    /** 一次切换动画的时长（毫秒）。 */
    duration?: number;
    variant?: TransitionVariant;
    /** When true, the transition expands from the viewport center instead of the button center. */
    fromCenter?: boolean;
    /** 受控模式下的当前主题；不传则监听 html 的 dark class 变化自行维护。 */
    theme?: "light" | "dark";
    /** 点击后的目标主题；不传则在明暗之间取反。 */
    targetTheme?: "light" | "dark";
    /** 主题实际切换完成后的回调（View Transition 失败直接生效时也会触发）。 */
    onThemeChange?: (theme: "light" | "dark") => void;
}

/** 生成「所有顶点都收拢在同一点」的 polygon：即动画起点时形状完全不可见。 */
function polygonCollapsed(cx: number, cy: number, vertexCount: number): string {
    const pairs = Array.from({ length: vertexCount }, () => `${cx}px ${cy}px`).join(", ");
    return `polygon(${pairs})`;
}

/**
 * 按形状变体计算 View Transition 的 [起始, 结束] clip-path：
 * 起始都是收拢在展开中心 (cx, cy) 的不可见形状，结束是能盖住整个视口的外扩形状。
 */
function getThemeTransitionClipPaths(variant: TransitionVariant, cx: number, cy: number, maxRadius: number, viewportWidth: number, viewportHeight: number): [string, string] {
    switch (variant) {
        case "circle":
            return [`circle(0px at ${cx}px ${cy}px)`, `circle(${maxRadius}px at ${cx}px ${cy}px)`];
        case "square": {
            const halfW = Math.max(cx, viewportWidth - cx);
            const halfH = Math.max(cy, viewportHeight - cy);
            const halfSide = Math.max(halfW, halfH) * 1.05;
            const end = [`${cx - halfSide}px ${cy - halfSide}px`, `${cx + halfSide}px ${cy - halfSide}px`, `${cx + halfSide}px ${cy + halfSide}px`, `${cx - halfSide}px ${cy + halfSide}px`].join(", ");
            return [polygonCollapsed(cx, cy, 4), `polygon(${end})`];
        }
        case "triangle": {
            const scale = maxRadius * 2.2;
            const dx = (Math.sqrt(3) / 2) * scale;
            const verts = [`${cx}px ${cy - scale}px`, `${cx + dx}px ${cy + 0.5 * scale}px`, `${cx - dx}px ${cy + 0.5 * scale}px`].join(", ");
            return [polygonCollapsed(cx, cy, 3), `polygon(${verts})`];
        }
        case "diamond": {
            // Slightly larger than the view-transition circle radius so axis-aligned coverage matches the circle reveal.
            const R = maxRadius * Math.SQRT2;
            const end = [`${cx}px ${cy - R}px`, `${cx + R}px ${cy}px`, `${cx}px ${cy + R}px`, `${cx - R}px ${cy}px`].join(", ");
            return [polygonCollapsed(cx, cy, 4), `polygon(${end})`];
        }
        case "hexagon": {
            const R = maxRadius * Math.SQRT2;
            const verts: string[] = [];
            for (let i = 0; i < 6; i++) {
                const a = -Math.PI / 2 + (i * Math.PI) / 3;
                verts.push(`${cx + R * Math.cos(a)}px ${cy + R * Math.sin(a)}px`);
            }
            return [polygonCollapsed(cx, cy, 6), `polygon(${verts.join(", ")})`];
        }
        case "rectangle": {
            const halfW = Math.max(cx, viewportWidth - cx);
            const halfH = Math.max(cy, viewportHeight - cy);
            const end = [`${cx - halfW}px ${cy - halfH}px`, `${cx + halfW}px ${cy - halfH}px`, `${cx + halfW}px ${cy + halfH}px`, `${cx - halfW}px ${cy + halfH}px`].join(", ");
            return [polygonCollapsed(cx, cy, 4), `polygon(${end})`];
        }
        case "star": {
            // Small overscan so the last frames never leave a 1px seam before the transition group ends.
            const R = maxRadius * Math.SQRT2 * 1.03;
            const innerRatio = 0.42;
            const starPolygon = (radius: number) => {
                const verts: string[] = [];
                for (let i = 0; i < 5; i++) {
                    const outerA = -Math.PI / 2 + (i * 2 * Math.PI) / 5;
                    verts.push(`${cx + radius * Math.cos(outerA)}px ${cy + radius * Math.sin(outerA)}px`);
                    const innerA = outerA + Math.PI / 5;
                    verts.push(`${cx + radius * innerRatio * Math.cos(innerA)}px ${cy + radius * innerRatio * Math.sin(innerA)}px`);
                }
                return `polygon(${verts.join(", ")})`;
            };
            const startR = Math.max(2, R * 0.025);
            return [starPolygon(startR), starPolygon(R)];
        }
        default:
            return [`circle(0px at ${cx}px ${cy}px)`, `circle(${maxRadius}px at ${cx}px ${cy}px)`];
    }
}

/**
 * 带形状揭示动画的主题切换按钮：点击后通过 View Transitions API 以 clip-path
 * 从按钮（或视口中心）展开新主题截图；浏览器不支持时静默降级为直接切换。
 */
export const AnimatedThemeToggler = ({ children, className, duration = 400, variant, fromCenter = false, theme, targetTheme, onThemeChange, ...props }: AnimatedThemeTogglerProps) => {
    const { t } = useTranslation();
    const shape = variant ?? "circle";
    const [isDark, setIsDark] = useState(false);
    const buttonRef = useRef<HTMLButtonElement>(null);

    useEffect(() => {
        // 传入受控 theme 时以它为准，不再监听 DOM；否则跟随其它入口（如系统/画布主题）对 dark class 的修改。
        if (theme) {
            setIsDark(theme === "dark");
            return;
        }

        const updateTheme = () => {
            setIsDark(document.documentElement.classList.contains("dark"));
        };

        updateTheme();

        const observer = new MutationObserver(updateTheme);
        observer.observe(document.documentElement, {
            attributes: true,
            attributeFilter: ["class"],
        });

        return () => observer.disconnect();
    }, [theme]);

    const toggleTheme = useCallback(() => {
        const button = buttonRef.current;
        if (!button) return;

        const viewportWidth = window.visualViewport?.width ?? window.innerWidth;
        const viewportHeight = window.visualViewport?.height ?? window.innerHeight;

        let x: number;
        let y: number;
        if (fromCenter) {
            x = viewportWidth / 2;
            y = viewportHeight / 2;
        } else {
            const { top, left, width, height } = button.getBoundingClientRect();
            x = left + width / 2;
            y = top + height / 2;
        }

        // 展开中心到视口最远角的距离，保证动画形状最终能覆盖全屏。
        const maxRadius = Math.hypot(Math.max(x, viewportWidth - x), Math.max(y, viewportHeight - y));

        const applyTheme = () => {
            const nextTheme = targetTheme ?? (isDark ? "light" : "dark");
            // 目标主题与当前一致时跳过，避免无意义的 DOM 变更触发 View Transition。
            if (nextTheme === (isDark ? "dark" : "light")) return;
            setIsDark(nextTheme === "dark");
            document.documentElement.classList.toggle("dark", nextTheme === "dark");
            document.documentElement.style.colorScheme = nextTheme;
            onThemeChange?.(nextTheme);
        };

        // 不支持 View Transitions（如旧版 Firefox）时退化为直接切换，不播动画。
        if (typeof document.startViewTransition !== "function") {
            applyTheme();
            return;
        }

        const clipPath = getThemeTransitionClipPaths(shape, x, y, maxRadius, viewportWidth, viewportHeight);

        const root = document.documentElement;
        root.dataset.magicuiThemeVt = "active";
        root.style.setProperty("--magicui-theme-toggle-vt-duration", `${duration}ms`);
        // Pin the collapsed clip-path via CSS so Firefox does not paint the new
        // theme unclipped between snapshot and the ready.then() JS animation.
        root.style.setProperty("--magicui-theme-vt-clip-from", clipPath[0]);
        const cleanup = () => {
            delete root.dataset.magicuiThemeVt;
            root.style.removeProperty("--magicui-theme-toggle-vt-duration");
            root.style.removeProperty("--magicui-theme-vt-clip-from");
        };

        const transition = document.startViewTransition(() => {
            flushSync(applyTheme);
        });
        if (typeof transition?.finished?.finally === "function") {
            transition.finished.finally(cleanup);
        } else {
            cleanup();
        }

        const ready = transition?.ready;
        if (ready && typeof ready.then === "function") {
            // 必须等 ready（旧快照已截图）才能开始动画，否则动画会被并行的截图打断。
            ready.then(() => {
                document.documentElement.animate(
                    {
                        clipPath,
                    },
                    {
                        duration,
                        // Star: linear avoids easing overshoot that fights polygon interpolation at t→1; VT group duration is synced above.
                        easing: shape === "star" ? "linear" : "ease-in-out",
                        fill: "forwards",
                        pseudoElement: "::view-transition-new(root)",
                    },
                );
            });
        }
    }, [shape, fromCenter, duration, isDark, targetTheme, onThemeChange]);

    return (
        <button type="button" ref={buttonRef} onClick={toggleTheme} className={cn(className)} {...props}>
            {children ?? (isDark ? <Sun /> : <Moon />)}
            <span className="sr-only">{props["aria-label"] || t("theme.toggle")}</span>
        </button>
    );
};
