import { useMemo, useState } from "react";
import { Card, Col, Row, Segmented, Skeleton, Statistic, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { formatPoints } from "@/lib/credits-format";
import { QueryError } from "@admin/components/query-error";
import { getAdminUsageAnalytics, type AdminUsageTopUser } from "@admin/services/api/admin";
import { EChart, type AdminEChartOption } from "./components/echart";

const RANGES = [7, 30, 90] as const;

export default function AdminAnalyticsPage() {
    const { t } = useTranslation();
    const [days, setDays] = useState<(typeof RANGES)[number]>(7);

    const usageQuery = useQuery({
        queryKey: ["admin", "analytics", days],
        queryFn: ({ signal }) => getAdminUsageAnalytics(days, signal),
        refetchOnWindowFocus: true,
        // 切时间范围时保留上一份数据，图表不闪空。
        placeholderData: (previous) => previous,
    });

    const data = usageQuery.data;

    const charts = useMemo(() => {
        if (!data) return null;

        const daily = data.daily;
        const trendOption: AdminEChartOption = {
            backgroundColor: "transparent",
            tooltip: { trigger: "axis" },
            legend: { top: 0 },
            grid: { left: 8, right: 8, top: 40, bottom: 8, containLabel: true },
            dataZoom: [{ type: "inside" }],
            xAxis: { type: "category", boundaryGap: false, data: daily.map((point) => point.date) },
            yAxis: [{ type: "value" }, { type: "value", splitLine: { show: false } }],
            series: [
                {
                    name: t("admin.analytics.trend.requests"),
                    type: "line",
                    smooth: true,
                    showSymbol: daily.length <= 31,
                    emphasis: { focus: "series" },
                    data: daily.map((point) => point.requests),
                },
                {
                    name: t("admin.analytics.trend.cost"),
                    type: "line",
                    smooth: true,
                    yAxisIndex: 1,
                    showSymbol: daily.length <= 31,
                    emphasis: { focus: "series" },
                    tooltip: { valueFormatter: (value) => formatPoints(Number(value)) },
                    data: daily.map((point) => point.costMicros),
                },
            ],
        };

        // 横向条形图倒序排列让最大值贴顶；键名过长时截断，完整名看 tooltip。
        const topByModel = [...data.byModel].sort((a, b) => b.requests - a.requests).slice(0, 10);
        const byModelOption: AdminEChartOption = {
            backgroundColor: "transparent",
            tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
            grid: { left: 8, right: 48, top: 8, bottom: 8, containLabel: true },
            xAxis: { type: "value" },
            yAxis: { type: "category", inverse: true, axisLabel: { width: 104, overflow: "truncate" }, data: topByModel.map((item) => item.key) },
            series: [
                {
                    name: t("admin.analytics.byModel.requests"),
                    type: "bar",
                    barMaxWidth: 16,
                    label: { show: true, position: "right" },
                    data: topByModel.map((item) => item.requests),
                },
            ],
        };

        const byCapabilityOption: AdminEChartOption = {
            backgroundColor: "transparent",
            tooltip: { trigger: "item" },
            legend: { bottom: 0 },
            series: [
                {
                    type: "pie",
                    radius: ["42%", "68%"],
                    center: ["50%", "44%"],
                    itemStyle: { borderRadius: 4, borderWidth: 1 },
                    label: { formatter: "{d}%" },
                    data: data.byCapability.map((item) => ({ name: item.key, value: item.requests })),
                },
            ],
        };

        const topBySpec = [...data.bySpec].sort((a, b) => b.requests - a.requests).slice(0, 10);
        const bySpecOption: AdminEChartOption = {
            backgroundColor: "transparent",
            tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
            grid: { left: 8, right: 48, top: 8, bottom: 8, containLabel: true },
            xAxis: { type: "value" },
            yAxis: {
                type: "category",
                inverse: true,
                axisLabel: { width: 136, overflow: "truncate" },
                data: topBySpec.map((item) => `${item.capability} / ${item.key}`),
            },
            series: [
                {
                    name: t("admin.analytics.bySpec.requests"),
                    type: "bar",
                    barMaxWidth: 16,
                    label: { show: true, position: "right" },
                    data: topBySpec.map((item) => item.requests),
                },
            ],
        };

        return { trendOption, byModelOption, byCapabilityOption, bySpecOption };
    }, [data, t]);

    if (usageQuery.isError) {
        return <QueryError error={usageQuery.error} message={t("admin.analytics.loadFailed")} onRetry={() => void usageQuery.refetch()} />;
    }

    if (usageQuery.isPending || !data || !charts) {
        return <Skeleton active paragraph={{ rows: 8 }} />;
    }

    const { summary } = data;
    // 后端 successRate 是 0-1 比例（4 位小数），乘 100 变百分数；先放大再取整消浮点尾差。
    const successPercent = Math.round(summary.successRate * 10000) / 100;
    const kpiCards: Array<{ key: string; value: string | number; suffix?: string }> = [
        { key: "requests", value: summary.requests },
        { key: "successRate", value: successPercent, suffix: "%" },
        { key: "cost", value: formatPoints(summary.costMicros) },
        { key: "activeUsers", value: summary.activeUsers },
        { key: "activeSessions", value: summary.activeSessions },
    ];

    const columns: ColumnsType<AdminUsageTopUser> = [
        {
            title: t("admin.analytics.topUsers.user"),
            dataIndex: "email",
            render: (_, row) => (
                <div className="flex flex-col">
                    <span>{row.displayName || row.email}</span>
                    <span className="text-xs text-stone-500 dark:text-stone-400">{row.email}</span>
                </div>
            ),
        },
        { title: t("admin.analytics.topUsers.requests"), dataIndex: "requests", align: "right", width: 120 },
        {
            title: t("admin.analytics.topUsers.cost"),
            dataIndex: "costMicros",
            align: "right",
            width: 160,
            render: (value: number) => formatPoints(value),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div>
                <Segmented
                    value={days}
                    onChange={(value) => setDays(value as (typeof RANGES)[number])}
                    options={RANGES.map((value) => ({ value, label: t(`admin.analytics.range.d${value}`) }))}
                />
            </div>
            <Row gutter={[16, 16]}>
                {kpiCards.map((card) => (
                    <Col key={card.key} xs={24} sm={12} lg={8}>
                        <Card size="small">
                            <Statistic title={t(`admin.analytics.kpi.${card.key}`)} value={card.value} suffix={card.suffix} />
                        </Card>
                    </Col>
                ))}
            </Row>
            <Card size="small" title={t("admin.analytics.trend.title")}>
                <EChart option={charts.trendOption} className="h-80" ariaLabel={t("admin.analytics.trend.title")} />
            </Card>
            <Row gutter={[16, 16]}>
                <Col xs={24} xl={8}>
                    <Card size="small" title={t("admin.analytics.byModel.title")}>
                        <EChart option={charts.byModelOption} className="h-64" ariaLabel={t("admin.analytics.byModel.title")} />
                    </Card>
                </Col>
                <Col xs={24} xl={8}>
                    <Card size="small" title={t("admin.analytics.byCapability.title")}>
                        <EChart option={charts.byCapabilityOption} className="h-64" ariaLabel={t("admin.analytics.byCapability.title")} />
                    </Card>
                </Col>
                <Col xs={24} xl={8}>
                    <Card size="small" title={t("admin.analytics.bySpec.title")}>
                        <EChart option={charts.bySpecOption} className="h-64" ariaLabel={t("admin.analytics.bySpec.title")} />
                    </Card>
                </Col>
            </Row>
            <Card size="small" title={t("admin.analytics.topUsers.title")}>
                <Table<AdminUsageTopUser>
                    rowKey="userId"
                    size="middle"
                    loading={usageQuery.isFetching}
                    columns={columns}
                    dataSource={data.topUsers}
                    pagination={false}
                    scroll={{ x: 720 }}
                />
            </Card>
        </div>
    );
}
