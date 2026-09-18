import { useState } from "react";
import { Button, Input, Select, Space, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { QueryError } from "@admin/components/query-error";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { PERM } from "@admin/lib/admin-nav";
import { listAdminMembershipSubscriptions, type AdminMembershipSubscription } from "@admin/services/api/admin";
import { GrantModal } from "./components/grant-modal";
import { RevokeModal } from "./components/revoke-modal";

const PLAN_COLORS: Record<string, string> = { free: "default", paid: "gold", sunset: "orange" };
const STATUS_COLORS: Record<string, string> = { active: "success", ended: "default" };

export default function AdminMembershipPage() {
    const { t } = useTranslation();
    const access = useConsoleAccess();
    // 前端只隐藏入口：没有 membership.write 就不给发放/补偿/作废，服务端仍会独立校验。
    const canWrite = access.phase === "admin" && access.permissions.includes(PERM.membershipWrite);
    const [page, setPage] = useState(1);
    const [size, setSize] = useState(20);
    const [userId, setUserId] = useState("");
    const [planId, setPlanId] = useState<string | undefined>();
    const [status, setStatus] = useState<string | undefined>();
    const [grantMode, setGrantMode] = useState<"grant" | "compensate" | null>(null);
    const [revokeTarget, setRevokeTarget] = useState<AdminMembershipSubscription | null>(null);

    const subscriptionsQuery = useQuery({
        queryKey: ["admin", "membership", page, size, userId, planId, status],
        queryFn: ({ signal }) => listAdminMembershipSubscriptions({ page, size, userId: userId || undefined, planId, status }, signal),
        placeholderData: (previous) => previous,
    });

    const columns: ColumnsType<AdminMembershipSubscription> = [
        {
            title: t("admin.membership.columns.user"),
            dataIndex: "userEmail",
            render: (_, row) => (
                <div className="min-w-0">
                    <div className="truncate font-medium">{row.userUsername}</div>
                    <div className="truncate text-xs text-stone-500 dark:text-stone-400">{row.userEmail}</div>
                </div>
            ),
        },
        {
            title: t("admin.membership.columns.plan"),
            dataIndex: "planId",
            width: 140,
            render: (value: string, row) => (
                <Tag color={PLAN_COLORS[value]}>{row.planName || t(`admin.users.plans.${value}`, { defaultValue: value })}</Tag>
            ),
        },
        {
            title: t("admin.membership.columns.status"),
            dataIndex: "status",
            width: 110,
            render: (value: string) => <Tag color={STATUS_COLORS[value]}>{t(`admin.membership.statuses.${value}`, { defaultValue: value })}</Tag>,
        },
        {
            title: t("admin.membership.columns.startedAt"),
            dataIndex: "startedAt",
            width: 140,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD"),
        },
        {
            title: t("admin.membership.columns.periodEnd"),
            dataIndex: "periodEnd",
            width: 140,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD"),
        },
        {
            title: t("admin.membership.columns.sourceRef"),
            dataIndex: "sourceRef",
            width: 180,
            render: (value: string | null) => (value ? <span className="font-mono text-xs">{value}</span> : "—"),
        },
        {
            title: t("admin.membership.columns.createdAt"),
            dataIndex: "createdAt",
            width: 140,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD"),
        },
        {
            title: t("admin.membership.columns.actions"),
            key: "actions",
            width: 100,
            render: (_, row) =>
                canWrite && row.status === "active" ? (
                    <Button danger size="small" type="link" className="!px-1" onClick={() => setRevokeTarget(row)}>
                        {t("admin.membership.revoke")}
                    </Button>
                ) : null,
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-3">
                <Input.Search
                    allowClear
                    className="w-56"
                    placeholder={t("admin.membership.filterUserPlaceholder")}
                    onSearch={(value) => {
                        setUserId(value.trim());
                        setPage(1);
                    }}
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
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.membership.filterStatus")}
                    value={status}
                    onChange={(value) => {
                        setStatus(value);
                        setPage(1);
                    }}
                    options={(["active", "ended"] as const).map((value) => ({ value, label: t(`admin.membership.statuses.${value}`) }))}
                />
                {canWrite ? (
                    <Space className="ml-auto">
                        <Button onClick={() => setGrantMode("compensate")}>{t("admin.membership.compensate")}</Button>
                        <Button type="primary" onClick={() => setGrantMode("grant")}>
                            {t("admin.membership.grant")}
                        </Button>
                    </Space>
                ) : null}
            </div>

            {subscriptionsQuery.isError ? (
                <QueryError error={subscriptionsQuery.error} message={t("admin.membership.loadFailed")} onRetry={() => void subscriptionsQuery.refetch()} />
            ) : (
                <Table<AdminMembershipSubscription>
                    rowKey="id"
                    size="middle"
                    loading={subscriptionsQuery.isPending}
                    columns={columns}
                    dataSource={subscriptionsQuery.data?.items ?? []}
                    pagination={{
                        current: page,
                        pageSize: size,
                        total: subscriptionsQuery.data?.total ?? 0,
                        showSizeChanger: true,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setSize(nextSize);
                        },
                    }}
                />
            )}

            <GrantModal open={grantMode !== null} mode={grantMode ?? "grant"} onClose={() => setGrantMode(null)} />
            <RevokeModal subscription={revokeTarget} onClose={() => setRevokeTarget(null)} />
        </div>
    );
}
