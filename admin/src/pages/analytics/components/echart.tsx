import { useEffect, useRef } from "react";
import * as echarts from "echarts/core";
import { BarChart, LineChart, PieChart } from "echarts/charts";
import { DataZoomComponent, GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { BarSeriesOption, LineSeriesOption, PieSeriesOption } from "echarts/charts";
import type { DataZoomComponentOption, GridComponentOption, LegendComponentOption, TooltipComponentOption } from "echarts/components";
import type { ComposeOption, EChartsType } from "echarts/core";

import { useThemeStore } from "@/stores/use-theme-store";

// 按需注册：只挂本页用到的图表/组件与 Canvas 渲染器，控制 echarts 的打包体积。
echarts.use([LineChart, BarChart, PieChart, GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, CanvasRenderer]);

export type AdminEChartOption = ComposeOption<
    BarSeriesOption | LineSeriesOption | PieSeriesOption | GridComponentOption | TooltipComponentOption | LegendComponentOption | DataZoomComponentOption
>;

type EChartProps = {
    option: AdminEChartOption;
    className?: string;
    // 图表内容对读屏不可达，用 role="img" + aria-label 提供等价说明。
    ariaLabel?: string;
};

// echarts 的轻量封装：业务侧只负责拼 option。实例与 ResizeObserver 都随组件生命周期走；
// 深浅色跟随全局主题（app-providers 用同一 store 挂 .dark class），颜色交给 echarts 内置
// dark 主题处理，这里与调用方都不写硬编码配色，图表底色由调用方在 option 里设为透明。
export function EChart({ option, className, ariaLabel }: EChartProps) {
    const containerRef = useRef<HTMLDivElement>(null);
    const chartRef = useRef<EChartsType | null>(null);
    const isDark = useThemeStore((state) => state.theme === "dark");

    useEffect(() => {
        const container = containerRef.current;
        if (!container) return;
        const chart = echarts.init(container, isDark ? "dark" : undefined);
        chartRef.current = chart;
        const observer = new ResizeObserver(() => chart.resize());
        observer.observe(container);
        return () => {
            observer.disconnect();
            chart.dispose();
            chartRef.current = null;
        };
    }, [isDark]);

    useEffect(() => {
        // notMerge：切换时间范围后系列数量可能变化，残留旧系列会画出脏数据。
        chartRef.current?.setOption(option, { notMerge: true });
    }, [option, isDark]);

    return <div ref={containerRef} className={className} role="img" aria-label={ariaLabel} />;
}
