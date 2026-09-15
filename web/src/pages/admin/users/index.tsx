import { useState } from "react";
import { Alert, App, Button, Descriptions, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Table, Tag, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatPoints } from "@/lib/credits-format";
import { formatBytes } from "@/lib/image-utils";
import {
    adjustAdminUserCredits,
    getAdminUser,
    listAdminUsers,
    patchAdminUser,
    recalculateAdminUserUsage,
    reclaimAdminUserMedia,
    resetAdminUserPassword,
    type AdminCleanupReport,
    type AdminUser,
} from "@/services/api/admin";

const PLAN_COLORS: Record<string, string> = { free: "default", paid: "gold", sunset: "orange" };
const STATUS_COLORS: Record<string, string> = { active: "success", disabled: "error", pending_deletion: "warning" };

type CreditFormValues = { bucket: "purchased" | "granted"; amountMicros: number; note: string };
type PasswordFormValues = { password: string };

export default function AdminUsersPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [page, setPage] = useState(1);
    const [size, setSize] = useState(20);
    const [q, setQ] = useState("");
    const [status, setStatus] = useState<string | undefined>();
    const [planId, setPlanId] = useState<string | undefined>();
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const [creditTarget, setCreditTarget] = useState<AdminUser | null>(null);
    const [passwordTarget, setPasswordTarget] = useState<AdminUser | null>(null);
    const [cleanupReport, setCleanupReport] = useState<AdminCleanupReport | null>(null);
    const [creditForm] = Form.useForm<CreditFormValues>();
    const [passwordForm] = Form.useForm<PasswordFormValues>();

    const usersQuery = useQuery({
        queryKey: ["admin", "users", page, size, q, status, planId],
        queryFn: ({ signal }) => listAdminUsers({ page, size, q: q || undefined, status, planId, sort: "-createdAt" }, signal),
        placeholderData: (previous) => previous,
    });
    const detailQuery = useQuery({
        queryKey: ["admin", "user", selectedId],
        queryFn: ({ signal }) => getAdminUser(selectedId as string, signal),
        enabled: !!selectedId,
    });

    const invalidateUsers = async () => {
        await queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
        if (selectedId) await queryClient.invalidateQueries({ queryKey: ["admin", "user", selectedId] });
    };

    const statusMutation = useMutation({
        mutationFn: ({ id, nextStatus }: { id: string; nextStatus: "active" | "disabled" }) => patchAdminUser(id, nextStatus),
        onSuccess: async () => {
            message.success(t("admin.users.statusUpdated"));
            await invalidateUsers();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const creditMutation = useMutation({
        mutationFn: (values: CreditFormValues) => adjustAdminUserCredits(creditTarget!.id, values),
        onSuccess: async () => {
            message.success(t("admin.users.creditsUpdated"));
            setCreditTarget(null);
            creditForm.resetFields();
            await invalidateUsers();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const passwordMutation = useMutation({
        mutationFn: (values: PasswordFormValues) => resetAdminUserPassword(passwordTarget!.id, values.password),
        onSuccess: async () => {
            message.success(t("admin.users.passwordReset"));
            setPasswordTarget(null);
            passwordForm.resetFields();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const recalcMutation = useMutation({
        mutationFn: (id: string) => recalculateAdminUserUsage(id),
        onSuccess: async (result) => {
            message.success(t("admin.users.usageRecalculated", { size: formatBytes(result.storageBytes) }));
            await invalidateUsers();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const reclaimMutation = useMutation({
        mutationFn: ({ id, dryRun }: { id: string; dryRun: boolean }) => reclaimAdminUserMedia(id, dryRun),
        onSuccess: async (report) => {
            setCleanupReport(report);
            await invalidateUsers();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const columns: ColumnsType<AdminUser> = [
        {
            title: t("admin.users.columns.user"),
            dataIndex: "email",
            render: (_, row) => (
                <div className="min-w-0">
                    <div className="truncate font-medium">{row.username}</div>
                    <div className="truncate text-xs text-stone-500 dark:text-stone-400">{row.email}</div>
                </div>
            ),
        },
        {
            title: t("admin.users.columns.plan"),
            dataIndex: "planId",
            width: 110,
            render: (value: string) => <Tag color={PLAN_COLORS[value]}>{t(`admin.users.plans.${value}`)}</Tag>,
        },
        {
            title: t("admin.users.columns.status"),
            dataIndex: "status",
            width: 110,
            render: (value: string) => <Tag color={STATUS_COLORS[value]}>{t(`admin.users.statuses.${value}`)}</Tag>,
        },
        {
            title: t("admin.users.columns.purchased"),
            dataIndex: "purchasedMicros",
            align: "right",
            width: 130,
            render: (value: number) => formatPoints(value),
        },
        {
            title: t("admin.users.columns.granted"),
            dataIndex: "grantedMicros",
            align: "right",
            width: 130,
            render: (value: number) => formatPoints(value),
        },
        {
            title: t("admin.users.columns.storage"),
            dataIndex: "storageBytes",
            align: "right",
            width: 110,
            render: (value: number) => formatBytes(value),
        },
        {
            title: t("admin.users.columns.paidUntil"),
            dataIndex: "paidUntil",
            width: 140,
            render: (value: string | null) => (value ? dayjs(value).format("YYYY-MM-DD") : "—"),
        },
        {
            title: t("admin.users.columns.createdAt"),
            dataIndex: "createdAt",
            width: 140,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD"),
        },
        {
            title: t("admin.users.columns.actions"),
            key: "actions",
            width: 250,
            render: (_, row) => (
                <Space size={4} wrap>
                    <Button size="small" type="link" className="!px-1" onClick={() => setSelectedId(row.id)}>
                        {t("admin.users.detailAction")}
                    </Button>
                    <Button
                        size="small"
                        type="link"
                        className="!px-1"
                        onClick={() => {
                            setCreditTarget(row);
                            creditForm.setFieldsValue({ bucket: "granted", amountMicros: 100000, note: "" });
                        }}
                    >
                        {t("admin.users.adjustCredits")}
                    </Button>
                    <Button
                        size="small"
                        type="link"
                        className="!px-1"
                        onClick={() => {
                            setPasswordTarget(row);
                            passwordForm.resetFields();
                        }}
                    >
                        {t("admin.users.resetPassword")}
                    </Button>
                    {row.status === "disabled" ? (
                        <Popconfirm title={t("admin.users.unbanConfirm")} onConfirm={() => statusMutation.mutate({ id: row.id, nextStatus: "active" })}>
                            <Button size="small" type="link" className="!px-1">
                                {t("admin.users.unban")}
                            </Button>
                        </Popconfirm>
                    ) : (
                        <Popconfirm title={t("admin.users.banConfirm")} description={t("admin.users.banHint")} onConfirm={() => statusMutation.mutate({ id: row.id, nextStatus: "disabled" })}>
                            <Button size="small" type="link" danger className="!px-1">
                                {t("admin.users.ban")}
                            </Button>
                        </Popconfirm>
                    )}
                </Space>
            ),
        },
    ];

    const detail = detailQuery.data?.user;

    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-3">
                <Input.Search
                    allowClear
                    className="w-64"
                    placeholder={t("admin.users.searchPlaceholder")}
                    onSearch={(value) => {
                        setQ(value.trim());
                        setPage(1);
                    }}
                />
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.users.filterStatus")}
                    value={status}
                    onChange={(value) => {
                        setStatus(value);
                        setPage(1);
                    }}
                    options={(["active", "disabled", "pending_deletion"] as const).map((value) => ({ value, label: t(`admin.users.statuses.${value}`) }))}
                />
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.users.filterPlan")}
                    value={planId}
                    onChange={(value) => {
                        setPlanId(value);
                        setPage(1);
                    }}
                    options={(["free", "paid", "sunset"] as const).map((value) => ({ value, label: t(`admin.users.plans.${value}`) }))}
                />
            </div>

            {usersQuery.isError ? (
                <Alert
                    type="error"
                    showIcon
                    message={t("admin.users.loadFailed")}
                    description={getApiErrorMessage(usersQuery.error)}
                    action={<Button size="small" onClick={() => void usersQuery.refetch()}>{t("admin.retry")}</Button>}
                />
            ) : (
                <Table<AdminUser>
                    rowKey="id"
                    size="middle"
                    loading={usersQuery.isPending}
                    columns={columns}
                    dataSource={usersQuery.data?.items ?? []}
                    pagination={{
                        current: page,
                        pageSize: size,
                        total: usersQuery.data?.total ?? 0,
                        showSizeChanger: true,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setSize(nextSize);
                        },
                    }}
                />
            )}

            <Drawer
                open={!!selectedId}
                width={520}
                title={t("admin.users.detailTitle")}
                onClose={() => setSelectedId(null)}
                extra={
                    <Button loading={recalcMutation.isPending} onClick={() => selectedId && recalcMutation.mutate(selectedId)}>
                        {t("admin.users.recalculateUsage")}
                    </Button>
                }
            >
                {detailQuery.isError ? (
                    <Alert type="error" showIcon message={t("admin.users.detailFailed")} description={getApiErrorMessage(detailQuery.error)} />
                ) : detailQuery.isPending ? (
                    <div className="py-6 text-center text-sm text-stone-500 dark:text-stone-400">{t("admin.loading")}</div>
                ) : detail ? (
                    <div className="flex flex-col gap-5">
                        <Descriptions size="small" column={1} bordered>
                            <Descriptions.Item label={t("admin.users.detail.email")}>{detail.email}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.username")}>{detail.username}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.plan")}>{detail.planName}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.emailVerified")}>
                                {detail.emailVerified ? t("admin.users.detail.yes") : t("admin.users.detail.no")}
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.purchased")}>{formatPoints(detail.purchasedMicros)}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.granted")}>{formatPoints(detail.grantedMicros)}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.paidUntil")}>
                                {detail.paidUntil ? dayjs(detail.paidUntil).format("YYYY-MM-DD HH:mm") : "—"}
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.storage")}>
                                {formatBytes(detail.storageBytes)} / {formatBytes(detail.storageLimit)} · {t("admin.users.detail.mediaCount", { count: detail.mediaCount })}
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.readOnly")}>
                                {detail.readOnly ? t("admin.users.detail.yes") : t("admin.users.detail.no")}
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.users.detail.lastLogin")}>
                                {detail.lastLoginAt ? dayjs(detail.lastLoginAt).format("YYYY-MM-DD HH:mm") : "—"}
                            </Descriptions.Item>
                        </Descriptions>
                        <div className="flex flex-wrap gap-2">
                            <Button loading={reclaimMutation.isPending} onClick={() => reclaimMutation.mutate({ id: detail.id, dryRun: true })}>
                                {t("admin.users.cleanupDryRun")}
                            </Button>
                            <Popconfirm title={t("admin.users.cleanupConfirm")} description={t("admin.users.cleanupHint")} onConfirm={() => reclaimMutation.mutate({ id: detail.id, dryRun: false })}>
                                <Button danger loading={reclaimMutation.isPending}>
                                    {t("admin.users.cleanupRun")}
                                </Button>
                            </Popconfirm>
                        </div>
                    </div>
                ) : null}
            </Drawer>

            <Modal
                open={!!creditTarget}
                title={t("admin.users.adjustTitle", { name: creditTarget?.username ?? "" })}
                okText={t("admin.users.adjustSubmit")}
                cancelText={t("common.cancel")}
                confirmLoading={creditMutation.isPending}
                onCancel={() => setCreditTarget(null)}
                onOk={async () => {
                    const values = await creditForm.validateFields();
                    await creditMutation.mutateAsync(values);
                }}
            >
                <Form form={creditForm} layout="vertical">
                    <Form.Item name="bucket" label={t("admin.users.bucket")} rules={[{ required: true }]}>
                        <Segmented
                            options={[
                                { value: "granted", label: t("admin.users.buckets.granted") },
                                { value: "purchased", label: t("admin.users.buckets.purchased") },
                            ]}
                        />
                    </Form.Item>
                    <Form.Item
                        name="amountMicros"
                        label={t("admin.users.amount")}
                        extra={t("admin.users.amountHint")}
                        rules={[{ required: true, message: t("admin.users.amountRequired") }]}
                    >
                        <InputNumber className="w-full" step={100000} precision={0} />
                    </Form.Item>
                    <Form.Item name="note" label={t("admin.users.note")} rules={[{ required: true, message: t("admin.users.noteRequired") }]}>
                        <Input maxLength={200} placeholder={t("admin.users.notePlaceholder")} />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={!!passwordTarget}
                title={t("admin.users.passwordTitle", { name: passwordTarget?.username ?? "" })}
                okText={t("admin.users.passwordSubmit")}
                cancelText={t("common.cancel")}
                confirmLoading={passwordMutation.isPending}
                onCancel={() => setPasswordTarget(null)}
                onOk={async () => {
                    const values = await passwordForm.validateFields();
                    await passwordMutation.mutateAsync(values);
                }}
            >
                <Alert className="mb-4" type="warning" showIcon message={t("admin.users.passwordHint")} />
                <Form form={passwordForm} layout="vertical">
                    <Form.Item
                        name="password"
                        label={t("admin.users.newPassword")}
                        rules={[
                            { required: true, message: t("admin.users.newPasswordRequired") },
                            { min: 8, max: 72, message: t("admin.users.newPasswordLength") },
                        ]}
                    >
                        <Input.Password autoComplete="new-password" />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={!!cleanupReport}
                title={cleanupReport?.dryRun ? t("admin.users.cleanupDryTitle") : t("admin.users.cleanupDoneTitle")}
                footer={<Button onClick={() => setCleanupReport(null)}>{t("admin.close")}</Button>}
                onCancel={() => setCleanupReport(null)}
                width={520}
            >
                {cleanupReport ? (
                    <div className="text-sm">
                        <p>
                            {t("admin.users.cleanupSummary", {
                                scanned: cleanupReport.scanned,
                                reclaimed: cleanupReport.reclaimed,
                                size: formatBytes(cleanupReport.freedBytes),
                            })}
                        </p>
                        <p className="mt-1 text-stone-500 dark:text-stone-400">
                            {t("admin.users.cleanupStorage", { used: formatBytes(cleanupReport.storageUsed), limit: formatBytes(cleanupReport.storageLimit) })}
                        </p>
                        {cleanupReport.items.length > 0 ? (
                            <ul className="mt-3 max-h-52 list-disc space-y-1 overflow-y-auto pl-5 text-xs">
                                {cleanupReport.items.map((item) => (
                                    <li key={item.storageKey}>
                                        {item.storageKey} · {formatBytes(item.bytes)} · {t(`admin.users.reasons.${item.reason}`)}
                                    </li>
                                ))}
                            </ul>
                        ) : null}
                    </div>
                ) : null}
            </Modal>
        </div>
    );
}
