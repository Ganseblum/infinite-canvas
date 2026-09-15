import { useEffect, useState } from "react";
import { Alert, App, Button, Card, Col, Descriptions, Form, Input, InputNumber, Modal, Popconfirm, Row, Select, Space, Switch, Table, Tabs, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import {
    addAdmin,
    getRevenueStats,
    getSettings,
    listAdminCommunityReports,
    listAdminCommunityWorks,
    listAdmins,
    listAuditLogs,
    patchAdminCommunityReport,
    patchAdminCommunityWork,
    removeAdmin,
    updateSettings,
    type AdminSummary,
    type AuditLog,
    type CommunityAdminReport,
    type CommunityAdminWork,
    type SiteSettings,
} from "@/services/api/admin";

export default function AdminSystemPage() {
    return (
        <Tabs
            items={[
                { key: "settings", label: <SettingsTabLabel />, children: <SettingsTab /> },
                { key: "admins", label: <AdminsTabLabel />, children: <AdminsTab /> },
                { key: "community", label: <CommunityTabLabel />, children: <CommunityTab /> },
                { key: "audit", label: <AuditTabLabel />, children: <AuditTab /> },
            ]}
        />
    );
}

function SettingsTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.settingsTab")}</>;
}
function AdminsTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.adminsTab")}</>;
}
function CommunityTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.communityTab")}</>;
}
function AuditTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.auditTab")}</>;
}

// ===== 站点设置 =====

function SettingsTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [form] = Form.useForm<SiteSettings>();

    const settingsQuery = useQuery({ queryKey: ["admin", "settings"], queryFn: ({ signal }) => getSettings(signal) });
    const revenueQuery = useQuery({ queryKey: ["admin", "revenue"], queryFn: ({ signal }) => getRevenueStats(signal) });

    useEffect(() => {
        if (settingsQuery.data) form.setFieldsValue(settingsQuery.data);
    }, [form, settingsQuery.data]);

    const saveMutation = useMutation({
        mutationFn: (values: Partial<SiteSettings>) => updateSettings(values),
        onSuccess: async () => {
            message.success(t("admin.system.saved"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "settings"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    if (settingsQuery.isError) {
        return (
            <Alert
                type="error"
                showIcon
                message={t("admin.system.loadFailed")}
                description={getApiErrorMessage(settingsQuery.error)}
                action={<Button size="small" onClick={() => void settingsQuery.refetch()}>{t("admin.retry")}</Button>}
            />
        );
    }

    const revenue = revenueQuery.data;

    return (
        <div className="flex flex-col gap-4">
            {revenue ? (
                <Row gutter={[16, 16]}>
                    {[
                        { key: "revenueToday", value: formatMoney(revenue.revenueMicrosToday), suffix: t("admin.system.ordersToday", { count: revenue.ordersToday }) },
                        { key: "revenueWeek", value: formatMoney(revenue.revenueMicrosWeek), suffix: t("admin.system.ordersWeek", { count: revenue.ordersWeek }) },
                        { key: "revenueTotal", value: formatMoney(revenue.revenueMicrosTotal), suffix: t("admin.system.ordersTotal", { count: revenue.ordersTotal }) },
                        { key: "paidUsers", value: formatPoints(revenue.paidUsers), suffix: t("admin.system.conversion", { percent: (revenue.conversionRate * 100).toFixed(1) }) },
                    ].map((item) => (
                        <Col key={item.key} xs={12} lg={6}>
                            <Card size="small">
                                <div className="text-xs text-stone-500 dark:text-stone-400">{t(`admin.system.${item.key}`)}</div>
                                <div className="mt-1 text-xl font-semibold">{item.value}</div>
                                <div className="mt-1 text-xs text-stone-500 dark:text-stone-400">{item.suffix}</div>
                            </Card>
                        </Col>
                    ))}
                </Row>
            ) : null}

            <Form form={form} layout="vertical" className="max-w-3xl" onFinish={(values) => saveMutation.mutate(values)}>
                <Form.Item name="announcement" label={t("admin.system.fields.announcement")} extra={t("admin.system.fields.announcementHint")}>
                    <Input.TextArea rows={2} maxLength={500} />
                </Form.Item>
                <div className="grid grid-cols-2 gap-x-4">
                    <Form.Item name="registrationEnabled" label={t("admin.system.fields.registrationEnabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                    <Form.Item name="maintenanceMode" label={t("admin.system.fields.maintenanceMode")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </div>
                <Form.Item name="maintenanceNotice" label={t("admin.system.fields.maintenanceNotice")} extra={t("admin.system.fields.maintenanceNoticeHint")}>
                    <Input maxLength={200} />
                </Form.Item>
                <div className="grid grid-cols-2 gap-x-4">
                    <Form.Item name="communityEnabled" label={t("admin.system.fields.communityEnabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                    <Form.Item name="checkinEnabled" label={t("admin.system.fields.checkinEnabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                    <Form.Item name="checkinRewardMicros" label={t("admin.system.fields.checkinRewardMicros")} extra={t("admin.system.fields.grantedHint")}>
                        <InputNumber className="w-full" min={0} step={10000} precision={0} />
                    </Form.Item>
                    <Form.Item name="inviteEnabled" label={t("admin.system.fields.inviteEnabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                    <Form.Item name="inviteRewardMicros" label={t("admin.system.fields.inviteRewardMicros")} extra={t("admin.system.fields.grantedHint")}>
                        <InputNumber className="w-full" min={0} step={10000} precision={0} />
                    </Form.Item>
                    <Form.Item name="inviteeRewardMicros" label={t("admin.system.fields.inviteeRewardMicros")} extra={t("admin.system.fields.grantedHint")}>
                        <InputNumber className="w-full" min={0} step={10000} precision={0} />
                    </Form.Item>
                    <Form.Item name="maxUploadBytes" label={t("admin.system.fields.maxUploadBytes")} extra={t("admin.system.fields.maxUploadBytesHint")}>
                        <InputNumber className="w-full" min={0} step={1024 * 1024} precision={0} />
                    </Form.Item>
                    <Form.Item name="generationConcurrency" label={t("admin.system.fields.generationConcurrency")} extra={t("admin.system.fields.generationConcurrencyHint")}>
                        <InputNumber className="w-full" min={0} max={50} precision={0} />
                    </Form.Item>
                </div>
                <Button type="primary" htmlType="submit" loading={saveMutation.isPending}>
                    {t("admin.save")}
                </Button>
            </Form>
        </div>
    );
}

// ===== 管理员管理 =====

function AdminsTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [open, setOpen] = useState(false);
    const [email, setEmail] = useState("");

    const adminsQuery = useQuery({ queryKey: ["admin", "admins"], queryFn: ({ signal }) => listAdmins(signal) });
    const addMutation = useMutation({
        mutationFn: (value: string) => addAdmin(value),
        onSuccess: async () => {
            message.success(t("admin.system.adminAdded"));
            setOpen(false);
            setEmail("");
            await queryClient.invalidateQueries({ queryKey: ["admin", "admins"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });
    const removeMutation = useMutation({
        mutationFn: (id: string) => removeAdmin(id),
        onSuccess: async () => {
            message.success(t("admin.system.adminRemoved"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "admins"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const columns: ColumnsType<AdminSummary> = [
        { title: t("admin.system.adminColumns.email"), dataIndex: "email" },
        { title: t("admin.system.adminColumns.username"), dataIndex: "username" },
        {
            title: t("admin.system.adminColumns.lastLogin"),
            dataIndex: "lastLoginAt",
            render: (value: string | null) => (value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—"),
        },
        {
            title: t("admin.system.adminColumns.createdAt"),
            dataIndex: "createdAt",
            render: (value: string) => dayjs(value).format("YYYY-MM-DD"),
        },
        {
            title: t("admin.system.adminColumns.actions"),
            key: "actions",
            width: 120,
            render: (_, row) => (
                <Popconfirm title={t("admin.system.removeAdminConfirm")} onConfirm={() => removeMutation.mutate(row.id)}>
                    <Button size="small" type="link" danger className="!px-1">
                        {t("admin.system.removeAdmin")}
                    </Button>
                </Popconfirm>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
                <p className="text-sm text-stone-500 dark:text-stone-400">{t("admin.system.adminsHint")}</p>
                <Button type="primary" onClick={() => setOpen(true)}>
                    {t("admin.system.addAdmin")}
                </Button>
            </div>
            {adminsQuery.isError ? (
                <Alert type="error" showIcon message={t("admin.system.loadFailed")} description={getApiErrorMessage(adminsQuery.error)} />
            ) : (
                <Table<AdminSummary> rowKey="id" size="middle" loading={adminsQuery.isPending} columns={columns} dataSource={adminsQuery.data?.items ?? []} pagination={false} />
            )}
            <Modal
                open={open}
                title={t("admin.system.addAdminTitle")}
                okText={t("admin.system.addAdmin")}
                cancelText={t("common.cancel")}
                confirmLoading={addMutation.isPending}
                onCancel={() => setOpen(false)}
                onOk={() => {
                    if (!email.trim()) {
                        message.warning(t("admin.system.adminEmailRequired"));
                        return;
                    }
                    addMutation.mutate(email.trim());
                }}
            >
                <Alert className="mb-4" type="info" showIcon message={t("admin.system.addAdminHint")} />
                <Input placeholder="admin@example.com" value={email} onChange={(event) => setEmail(event.target.value)} />
            </Modal>
        </div>
    );
}

// ===== 社区管理 =====

function CommunityTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [page, setPage] = useState(1);
    const [status, setStatus] = useState<string | undefined>("published");

    const reportsQuery = useQuery({
        queryKey: ["admin", "community", "reports"],
        queryFn: ({ signal }) => listAdminCommunityReports({ size: 20, status: "pending" }, signal),
    });
    const worksQuery = useQuery({
        queryKey: ["admin", "community", "works", page, status],
        queryFn: ({ signal }) => listAdminCommunityWorks({ page, size: 20, status }, signal),
        placeholderData: (previous) => previous,
    });

    const patchWorkMutation = useMutation({
        mutationFn: ({ id, next }: { id: string; next: "published" | "hidden" | "removed" }) => patchAdminCommunityWork(id, next),
        onSuccess: async () => {
            message.success(t("admin.system.workUpdated"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "community"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });
    const patchReportMutation = useMutation({
        mutationFn: ({ id, next, removeWork }: { id: string; next: "handled" | "dismissed"; removeWork?: boolean }) =>
            patchAdminCommunityReport(id, { status: next, removeWork }),
        onSuccess: async () => {
            message.success(t("admin.system.reportHandled"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "community"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const reportColumns: ColumnsType<CommunityAdminReport> = [
        { title: t("admin.system.reportColumns.work"), dataIndex: "workTitle" },
        { title: t("admin.system.reportColumns.reason"), dataIndex: "reason" },
        {
            title: t("admin.system.reportColumns.createdAt"),
            dataIndex: "createdAt",
            width: 160,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD HH:mm"),
        },
        {
            title: t("admin.system.reportColumns.actions"),
            key: "actions",
            width: 220,
            render: (_, row) => (
                <Space size={2}>
                    <Popconfirm title={t("admin.system.dismissConfirm")} onConfirm={() => patchReportMutation.mutate({ id: row.id, next: "dismissed" })}>
                        <Button size="small" type="link" className="!px-1">
                            {t("admin.system.dismiss")}
                        </Button>
                    </Popconfirm>
                    <Popconfirm title={t("admin.system.removeWorkConfirm")} onConfirm={() => patchReportMutation.mutate({ id: row.id, next: "handled", removeWork: true })}>
                        <Button size="small" type="link" danger className="!px-1">
                            {t("admin.system.handleAndRemove")}
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    const workColumns: ColumnsType<CommunityAdminWork> = [
        {
            title: t("admin.system.workColumns.work"),
            dataIndex: "title",
            render: (value: string, row) => (
                <div className="min-w-0">
                    <div className="truncate font-medium">{value}</div>
                    <Link to={`/community/users/${row.userId}`} className="truncate text-xs text-stone-500 hover:underline dark:text-stone-400">
                        {row.userId}
                    </Link>
                </div>
            ),
        },
        {
            title: t("admin.system.workColumns.status"),
            dataIndex: "status",
            width: 110,
            render: (value: string) => <Tag>{t(`admin.system.workStatuses.${value}`)}</Tag>,
        },
        { title: t("admin.system.workColumns.likes"), dataIndex: "likeCount", width: 90, align: "right" },
        { title: t("admin.system.workColumns.remix"), dataIndex: "remixCount", width: 90, align: "right" },
        {
            title: t("admin.system.workColumns.reports"),
            dataIndex: "reportCount",
            width: 90,
            align: "right",
            render: (value: number) => (value > 0 ? <Tag color="warning">{value}</Tag> : 0),
        },
        {
            title: t("admin.system.workColumns.actions"),
            key: "actions",
            width: 200,
            render: (_, row) => (
                <Space size={2}>
                    {row.status !== "removed" ? (
                        <Popconfirm title={t("admin.system.removeWorkConfirm")} onConfirm={() => patchWorkMutation.mutate({ id: row.id, next: "removed" })}>
                            <Button size="small" type="link" danger className="!px-1">
                                {t("admin.system.removeWork")}
                            </Button>
                        </Popconfirm>
                    ) : null}
                    {row.status === "removed" ? (
                        <Button size="small" type="link" className="!px-1" onClick={() => patchWorkMutation.mutate({ id: row.id, next: "published" })}>
                            {t("admin.system.restoreWork")}
                        </Button>
                    ) : null}
                </Space>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-6">
            <section>
                <h3 className="mb-3 text-sm font-medium">{t("admin.system.pendingReports")}</h3>
                {reportsQuery.isError ? (
                    <Alert type="error" showIcon message={t("admin.system.loadFailed")} description={getApiErrorMessage(reportsQuery.error)} />
                ) : (
                    <Table<CommunityAdminReport>
                        rowKey="id"
                        size="small"
                        loading={reportsQuery.isPending}
                        columns={reportColumns}
                        dataSource={reportsQuery.data?.items ?? []}
                        pagination={false}
                        locale={{ emptyText: t("admin.system.noReports") }}
                    />
                )}
            </section>
            <section>
                <div className="mb-3 flex items-center justify-between">
                    <h3 className="text-sm font-medium">{t("admin.system.worksTitle")}</h3>
                    <Select
                        allowClear
                        className="w-36"
                        placeholder={t("admin.system.workStatusFilter")}
                        value={status}
                        onChange={(value) => {
                            setStatus(value);
                            setPage(1);
                        }}
                        options={(["published", "hidden", "removed"] as const).map((value) => ({ value, label: t(`admin.system.workStatuses.${value}`) }))}
                    />
                </div>
                {worksQuery.isError ? (
                    <Alert type="error" showIcon message={t("admin.system.loadFailed")} description={getApiErrorMessage(worksQuery.error)} />
                ) : (
                    <Table<CommunityAdminWork>
                        rowKey="id"
                        size="small"
                        loading={worksQuery.isPending}
                        columns={workColumns}
                        dataSource={worksQuery.data?.items ?? []}
                        pagination={{
                            current: page,
                            pageSize: 20,
                            total: worksQuery.data?.total ?? 0,
                            onChange: (nextPage) => setPage(nextPage),
                        }}
                    />
                )}
            </section>
            <Descriptions size="small" column={1}>
                <Descriptions.Item label={t("admin.system.communityHintLabel")}>{t("admin.system.communityHint")}</Descriptions.Item>
            </Descriptions>
        </div>
    );
}

// ===== 审计日志 =====

function AuditTab() {
    const { t } = useTranslation();
    const [page, setPage] = useState(1);
    const [action, setAction] = useState<string | undefined>();

    const logsQuery = useQuery({
        queryKey: ["admin", "audit", page, action],
        queryFn: ({ signal }) => listAuditLogs({ page, size: 20, action }, signal),
        placeholderData: (previous) => previous,
    });

    const columns: ColumnsType<AuditLog> = [
        {
            title: t("admin.system.auditColumns.time"),
            dataIndex: "createdAt",
            width: 170,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD HH:mm:ss"),
        },
        { title: t("admin.system.auditColumns.action"), dataIndex: "action", width: 200, render: (value: string) => <code className="text-xs">{value}</code> },
        { title: t("admin.system.auditColumns.target"), dataIndex: "targetId", width: 260, render: (value: string, row) => <span className="font-mono text-xs">{row.targetType}·{value.slice(0, 8)}…</span> },
        { title: t("admin.system.auditColumns.actor"), dataIndex: "actorUserId", width: 260, render: (value: string) => <span className="font-mono text-xs">{value}</span> },
        { title: t("admin.system.auditColumns.reason"), dataIndex: "reason", render: (value?: string) => value || "—" },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center gap-3">
                <Select
                    allowClear
                    className="w-64"
                    placeholder={t("admin.system.auditActionFilter")}
                    value={action}
                    onChange={(value) => {
                        setAction(value);
                        setPage(1);
                    }}
                    options={[
                        "settings.update",
                        "admin.add",
                        "admin.remove",
                        "user.status",
                        "user.credits_adjust",
                        "user.password_reset",
                        "model.create",
                        "model.update",
                        "model.delete",
                        "promotion.create",
                        "promotion.update",
                        "package.create",
                        "package.update",
                        "channel.create",
                        "channel.update",
                        "channel.delete",
                        "moderation.review",
                        "moderation.compensate",
                        "community.work_status",
                        "community.report",
                    ].map((value) => ({ value, label: value }))}
                />
            </div>
            {logsQuery.isError ? (
                <Alert type="error" showIcon message={t("admin.system.loadFailed")} description={getApiErrorMessage(logsQuery.error)} />
            ) : (
                <Table<AuditLog>
                    rowKey="id"
                    size="small"
                    loading={logsQuery.isPending}
                    columns={columns}
                    dataSource={logsQuery.data?.items ?? []}
                    scroll={{ x: 1100 }}
                    pagination={{
                        current: page,
                        pageSize: 20,
                        total: logsQuery.data?.total ?? 0,
                        onChange: (nextPage) => setPage(nextPage),
                    }}
                />
            )}
        </div>
    );
}

// ===== 总览页复用的收入卡片 =====

export function RevenueCards() {
    const { t } = useTranslation();
    const revenueQuery = useQuery({ queryKey: ["admin", "revenue"], queryFn: ({ signal }) => getRevenueStats(signal), refetchOnWindowFocus: true });
    const revenue = revenueQuery.data;
    if (!revenue) return null;
    return (
        <Row gutter={[16, 16]}>
            {[
                { key: "revenueToday", value: formatMoney(revenue.revenueMicrosToday), suffix: t("admin.system.ordersToday", { count: revenue.ordersToday }) },
                { key: "revenueWeek", value: formatMoney(revenue.revenueMicrosWeek), suffix: t("admin.system.ordersWeek", { count: revenue.ordersWeek }) },
                { key: "revenueTotal", value: formatMoney(revenue.revenueMicrosTotal), suffix: t("admin.system.ordersTotal", { count: revenue.ordersTotal }) },
                { key: "paidUsers", value: formatPoints(revenue.paidUsers), suffix: t("admin.system.conversion", { percent: (revenue.conversionRate * 100).toFixed(1) }) },
            ].map((item) => (
                <Col key={item.key} xs={12} lg={6}>
                    <Card size="small">
                        <div className="text-xs text-stone-500 dark:text-stone-400">{t(`admin.system.${item.key}`)}</div>
                        <div className="mt-1 text-xl font-semibold">{item.value}</div>
                        <div className="mt-1 text-xs text-stone-500 dark:text-stone-400">{item.suffix}</div>
                    </Card>
                </Col>
            ))}
        </Row>
    );
}
