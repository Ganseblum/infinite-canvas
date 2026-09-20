import { useMemo, useState } from "react";
import { Button, Card, Col, Drawer, Input, Row, Segmented, Select, Skeleton, Statistic, Table, Tabs, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { QueryError } from "@admin/components/query-error";
import { EChart, type AdminEChartOption } from "@admin/pages/analytics/components/echart";
import {
    getAdminOfficeSession,
    getAdminOfficeStats,
    listAdminOfficeSessions,
    type AdminOfficeMessage,
    type AdminOfficeSession,
    type AdminOfficeStats,
} from "@admin/services/api/office-admin";

const PAGE_SIZE = 20;

// 时间与 web 后台各页同口径：RFC3339 取到分钟。
const timeText = (value: string | null | undefined) => (value || "").slice(0, 16).replace("T", " ");
// 消息 content 是存储 JSON（{schemaVersion:1,text}），渲染只取 text；缺字段时显示占位。
const messageText = (message: AdminOfficeMessage) => (message.content && typeof message.content.text === "string" ? message.content.text : "");

export default function AdminOfficePage() {
    const { t } = useTranslation();
    return (
        <main className="min-h-full bg-background">
            <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6">
                <h1 className="text-2xl font-semibold">{t("admin.office.title")}</h1>
                <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("admin.office.description")}</p>
                <Tabs
                    className="mt-4"
                    items={[
                        { key: "sessions", label: t("admin.office.sessionsTab"), children: <SessionsTab /> },
                        { key: "stats", label: t("admin.office.statsTab"), children: <StatsTab /> },
                    ]}
                />
            </div>
        </main>
    );
}

// ===== 会话页 =====

function SessionsTab() {
    const { t } = useTranslation();
    const [user, setUser] = useState("");
    const [status, setStatus] = useState<string | undefined>();
    const [page, setPage] = useState(1);
    // 游标分页没有 total：记下每页的起始游标（第 1 页无游标），用「total = 已知页数 × 每页条数」
    // 的常规技巧让 antd 分页器能往后翻；nextCursor 为空即末页，往前翻回放已存游标。
    const [cursors, setCursors] = useState<Record<number, string | undefined>>({ 1: undefined });
    const [detailId, setDetailId] = useState<string | null>(null);

    const cursor = cursors[page];
    const listQuery = useQuery({
        queryKey: ["admin", "office", "sessions", page, user, status, cursor],
        queryFn: ({ signal }) => listAdminOfficeSessions({ userId: user || undefined, status, cursor, limit: PAGE_SIZE }, signal),
    });

    const detailQuery = useQuery({
        queryKey: ["admin", "office", "session", detailId],
        queryFn: ({ signal }) => getAdminOfficeSession(detailId!, signal),
        enabled: !!detailId,
    });

    const resetPage = () => {
        setPage(1);
        setCursors({ 1: undefined });
    };

    const columns: ColumnsType<AdminOfficeSession> = [
        { title: t("admin.office.columns.updatedAt"), dataIndex: "updatedAt", width: 110, render: (value: string) => timeText(value) },
        { title: t("admin.office.columns.user"), dataIndex: "userEmail", ellipsis: true, render: (value: string, row) => value || row.userId || "—" },
        { title: t("admin.office.columns.title"), dataIndex: "title", ellipsis: true, render: (value: string) => value || "—" },
        { title: t("admin.office.columns.messages"), dataIndex: "messagesCount", width: 80, align: "right" },
        { title: t("admin.office.columns.status"), dataIndex: "status", width: 90, render: (value: string) => <SessionStatusTag status={value} /> },
        { title: t("admin.office.columns.lastRunStatus"), dataIndex: "lastRunStatus", width: 100, render: (value: string | null | undefined) => <RunStatusTag status={value} /> },
        {
            title: t("admin.membership.columns.actions"),
            key: "actions",
            width: 80,
            render: (_: unknown, row: AdminOfficeSession) => (
                <Button size="small" type="link" onClick={() => setDetailId(row.id)}>
                    {t("common.view")}
                </Button>
            ),
        },
    ];

    const items = listQuery.data?.items ?? [];
    const nextCursor = listQuery.data?.nextCursor;

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3">
                <Input.Search
                    className="w-64"
                    allowClear
                    placeholder={t("admin.office.filter.user")}
                    onSearch={(value) => {
                        setUser(value.trim());
                        resetPage();
                    }}
                />
                <Select
                    className="w-40"
                    allowClear
                    placeholder={t("admin.office.filter.status")}
                    value={status}
                    onChange={(value) => {
                        setStatus(value || undefined);
                        resetPage();
                    }}
                    options={["active"].map((key) => ({ value: key, label: t(`admin.office.statuses.${key}`) }))}
                />
            </div>
            {listQuery.isError ? (
                <QueryError error={listQuery.error} message={t("admin.office.loadFailed")} onRetry={() => void listQuery.refetch()} />
            ) : listQuery.isPending ? (
                <Skeleton active paragraph={{ rows: 6 }} />
            ) : (
                <Table
                    rowKey="id"
                    size="small"
                    columns={columns}
                    dataSource={items}
                    loading={listQuery.isFetching}
                    rowClassName="cursor-pointer"
                    onRow={(row) => ({ onClick: () => setDetailId(row.id) })}
                    pagination={{
                        current: page,
                        pageSize: PAGE_SIZE,
                        total: nextCursor ? (page + 1) * PAGE_SIZE : page * PAGE_SIZE,
                        onChange: (next) => {
                            if (next > page) {
                                if (!nextCursor) return;
                                setCursors((prev) => ({ ...prev, [next]: nextCursor }));
                            }
                            setPage(next);
                        },
                        showSizeChanger: false,
                    }}
                />
            )}

            <Drawer title={t("admin.office.detailTitle")} open={!!detailId} size="large" onClose={() => setDetailId(null)}>
                {detailQuery.isError ? (
                    <QueryError error={detailQuery.error} message={t("admin.office.detailLoadFailed")} onRetry={() => void detailQuery.refetch()} />
                ) : detailQuery.isPending || !detailQuery.data ? (
                    <Skeleton active paragraph={{ rows: 8 }} />
                ) : (
                    <SessionDetail session={detailQuery.data.session} messages={detailQuery.data.messages} />
                )}
            </Drawer>
        </div>
    );
}

// 会话状态：M1 恒为 active（server/internal/office/model.go），未知值按默认样式兜底。
function SessionStatusTag({ status }: { status: string }) {
    const { t } = useTranslation();
    return <Tag color={status === "active" ? "green" : undefined}>{t(`admin.office.statuses.${status}`)}</Tag>;
}

// run 状态：cancelled 用默认灰；空值（该会话还没跑过 run）显示占位。
function RunStatusTag({ status }: { status: string | null | undefined }) {
    const { t } = useTranslation();
    if (!status) return <span className="text-stone-400 dark:text-stone-500">—</span>;
    const colors: Record<string, string> = { queued: "gold", running: "blue", succeeded: "green", failed: "red" };
    return <Tag color={colors[status]}>{t(`admin.office.runStatuses.${status}`)}</Tag>;
}

// 会话详情：只读。元信息一块，下面是消息时间线（用户左、助理右，与反馈工单详情同一视觉语言）。
function SessionDetail({ session, messages }: { session: AdminOfficeSession; messages: AdminOfficeMessage[] }) {
    const { t } = useTranslation();
    return (
        <div className="space-y-4">
            <div className="rounded-xl border border-stone-200 p-4 dark:border-stone-800">
                <div className="flex flex-wrap items-center gap-2">
                    <SessionStatusTag status={session.status} />
                    {session.lastRunStatus ? <RunStatusTag status={session.lastRunStatus} /> : null}
                    <span className="text-xs text-stone-400">{session.userEmail || session.userId}</span>
                    <span className="ml-auto text-xs text-stone-400">{timeText(session.updatedAt)}</span>
                </div>
                <p className="mt-3 text-sm font-medium">{session.title || "—"}</p>
                <p className="mt-1 text-xs text-stone-400">
                    {t("admin.office.detail.createdAt")} {timeText(session.createdAt)} · {t("admin.office.columns.messages")} {session.messagesCount}
                </p>
            </div>
            <div className="space-y-3">
                {messages.length === 0 ? (
                    <p className="text-sm text-stone-400">{t("admin.office.messagesEmpty")}</p>
                ) : (
                    messages.map((message) => {
                        const isAssistant = message.role === "assistant";
                        return (
                            <div key={message.id} className={isAssistant ? "flex justify-end" : ""}>
                                <div className={`max-w-[85%] rounded-xl border p-3 text-sm ${isAssistant ? "border-blue-200 bg-blue-50 dark:border-blue-900 dark:bg-blue-950" : "border-stone-200 dark:border-stone-800"}`}>
                                    <p className="text-xs text-stone-400">
                                        {t(`admin.office.roleLabels.${message.role}`)} · {timeText(message.createdAt)}
                                    </p>
                                    <p className="mt-1 whitespace-pre-wrap break-words">{messageText(message) || "—"}</p>
                                </div>
                            </div>
                        );
                    })
                )}
            </div>
        </div>
    );
}

// ===== 运营统计页 =====

function StatsTab() {
    const { t } = useTranslation();
    const [days, setDays] = useState<7 | 30>(7);

    const statsQuery = useQuery({
        queryKey: ["admin", "office", "stats", days],
        queryFn: ({ signal }) => getAdminOfficeStats(days, signal),
        refetchOnWindowFocus: true,
        // 切时间范围时保留上一份数据，图表不闪空。
        placeholderData: (previous) => previous,
    });

    const data = statsQuery.data;

    const charts = useMemo(() => {
        if (!data) return null;

        const byStatusOption: AdminEChartOption = {
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
                    data: Object.entries(data.runsByStatus).map(([key, count]) => ({ name: t(`admin.office.runStatuses.${key}`), value: count })),
                },
            ],
        };

        // 横向条形图倒序排列让最大值贴顶（与用量分析页同一形态）。
        const barOption = (names: string[], values: number[], seriesName: string, labelWidth: number): AdminEChartOption => ({
            backgroundColor: "transparent",
            tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
            grid: { left: 8, right: 48, top: 8, bottom: 8, containLabel: true },
            xAxis: { type: "value" },
            yAxis: { type: "category", inverse: true, axisLabel: { width: labelWidth, overflow: "truncate" }, data: names },
            series: [{ name: seriesName, type: "bar", barMaxWidth: 16, label: { show: true, position: "right" }, data: values }],
        });

        const topByModel = [...data.runsByModel].sort((a, b) => b.count - a.count).slice(0, 10);
        const topToolCalls = [...data.toolCalls].sort((a, b) => b.count - a.count).slice(0, 10);

        return {
            byStatusOption,
            byModelOption: barOption(topByModel.map((item) => item.model), topByModel.map((item) => item.count), t("admin.office.stats.byModel.runs"), 104),
            toolCallsOption: barOption(topToolCalls.map((item) => item.name), topToolCalls.map((item) => item.count), t("admin.office.stats.toolCalls.calls"), 136),
        };
    }, [data, t]);

    if (statsQuery.isError) {
        return <QueryError error={statsQuery.error} message={t("admin.office.stats.loadFailed")} onRetry={() => void statsQuery.refetch()} />;
    }

    if (statsQuery.isPending || !data || !charts) {
        return <Skeleton active paragraph={{ rows: 8 }} />;
    }

    // 成功率只统计到达终态的 run（排队/运行中不计入分母）；一个终态都没有时显示占位。
    const { runsByStatus } = data;
    const terminalTotal = runsByStatus.succeeded + runsByStatus.failed + runsByStatus.cancelled;
    const successPercent = terminalTotal > 0 ? Math.round((runsByStatus.succeeded / terminalTotal) * 10000) / 100 : null;
    const kpiCards: Array<{ key: string; value: string | number; suffix?: string }> = [
        { key: "runs", value: data.runsTotal },
        { key: "messages", value: data.messagesTotal },
        { key: "sessions", value: data.sessionsTotal },
        { key: "activeUsers", value: data.activeUsers },
        { key: "successRate", value: successPercent ?? "—", suffix: successPercent === null ? undefined : "%" },
    ];

    const columns: ColumnsType<AdminOfficeStats["topUsers"][number]> = [
        {
            title: t("admin.office.stats.topUsers.user"),
            dataIndex: "email",
            render: (_, row) => (
                <div className="flex flex-col">
                    <span>{row.email || row.userId}</span>
                    <span className="text-xs text-stone-500 dark:text-stone-400">{row.userId}</span>
                </div>
            ),
        },
        { title: t("admin.office.stats.topUsers.runs"), dataIndex: "runs", align: "right", width: 120 },
        { title: t("admin.office.stats.topUsers.messages"), dataIndex: "messages", align: "right", width: 120 },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div>
                <Segmented
                    value={days}
                    onChange={(value) => setDays(value as 7 | 30)}
                    options={[7, 30].map((value) => ({ value, label: t(`admin.office.stats.range.d${value}`) }))}
                />
            </div>
            <Row gutter={[16, 16]}>
                {kpiCards.map((card) => (
                    <Col key={card.key} xs={24} sm={12} lg={8}>
                        <Card size="small">
                            <Statistic title={t(`admin.office.stats.kpi.${card.key}`)} value={card.value} suffix={card.suffix} />
                        </Card>
                    </Col>
                ))}
            </Row>
            <Row gutter={[16, 16]}>
                <Col xs={24} xl={8}>
                    <Card size="small" title={t("admin.office.stats.byStatus.title")}>
                        <EChart option={charts.byStatusOption} className="h-64" ariaLabel={t("admin.office.stats.byStatus.title")} />
                    </Card>
                </Col>
                <Col xs={24} xl={8}>
                    <Card size="small" title={t("admin.office.stats.byModel.title")}>
                        <EChart option={charts.byModelOption} className="h-64" ariaLabel={t("admin.office.stats.byModel.title")} />
                    </Card>
                </Col>
                <Col xs={24} xl={8}>
                    <Card size="small" title={t("admin.office.stats.toolCalls.title")}>
                        <EChart option={charts.toolCallsOption} className="h-64" ariaLabel={t("admin.office.stats.toolCalls.title")} />
                    </Card>
                </Col>
            </Row>
            <Card size="small" title={t("admin.office.stats.topUsers.title")}>
                <Table rowKey="userId" size="middle" loading={statsQuery.isFetching} columns={columns} dataSource={data.topUsers} pagination={false} scroll={{ x: 480 }} />
            </Card>
        </div>
    );
}
