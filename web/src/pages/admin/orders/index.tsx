import { useState } from "react";
import { Alert, Button, Input, Select, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import { listAdminOrders, type AdminOrder } from "@/services/api/admin";

const STATUS_COLORS: Record<string, string> = { pending: "processing", paid: "success", failed: "error", refunded: "default" };

export default function AdminOrdersPage() {
    const { t } = useTranslation();
    const [page, setPage] = useState(1);
    const [size, setSize] = useState(20);
    const [status, setStatus] = useState<string | undefined>();
    const [provider, setProvider] = useState<string | undefined>();
    const [userId, setUserId] = useState("");

    const ordersQuery = useQuery({
        queryKey: ["admin", "orders", page, size, status, provider, userId],
        queryFn: ({ signal }) => listAdminOrders({ page, size, status, provider, userId: userId || undefined, sort: "-createdAt" }, signal),
        placeholderData: (previous) => previous,
    });

    const columns: ColumnsType<AdminOrder> = [
        {
            title: t("admin.orders.columns.order"),
            dataIndex: "id",
            width: 280,
            render: (value: string) => <span className="font-mono text-xs">{value}</span>,
        },
        { title: t("admin.orders.columns.package"), dataIndex: "packageId", width: 120 },
        {
            title: t("admin.orders.columns.provider"),
            dataIndex: "provider",
            width: 100,
            render: (value: string) => t(`billing.providers.${value}`, { defaultValue: value }),
        },
        {
            title: t("admin.orders.columns.price"),
            dataIndex: "priceMicros",
            align: "right",
            width: 110,
            render: (value: number, row) => formatMoney(value, row.currency),
        },
        {
            title: t("admin.orders.columns.points"),
            key: "points",
            align: "right",
            width: 160,
            render: (_, row) => `${formatPoints(row.purchasedMicros)} + ${formatPoints(row.grantedMicros)}`,
        },
        {
            title: t("admin.orders.columns.status"),
            dataIndex: "status",
            width: 100,
            render: (value: string) => <Tag color={STATUS_COLORS[value]}>{t(`billing.orderStatus.${value}`, { defaultValue: value })}</Tag>,
        },
        {
            title: t("admin.orders.columns.paidAt"),
            dataIndex: "paidAt",
            width: 160,
            render: (value: string | null) => (value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—"),
        },
        {
            title: t("admin.orders.columns.createdAt"),
            dataIndex: "createdAt",
            width: 160,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD HH:mm"),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-3">
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.orders.filterStatus")}
                    value={status}
                    onChange={(value) => {
                        setStatus(value);
                        setPage(1);
                    }}
                    options={(["pending", "paid", "failed", "refunded"] as const).map((value) => ({ value, label: t(`billing.orderStatus.${value}`) }))}
                />
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.orders.filterProvider")}
                    value={provider}
                    onChange={(value) => {
                        setProvider(value);
                        setPage(1);
                    }}
                    options={[
                        { value: "alipay", label: t("billing.providers.alipay") },
                        { value: "wechat", label: t("billing.providers.wechat") },
                    ]}
                />
                <Input.Search
                    allowClear
                    className="w-80"
                    placeholder={t("admin.orders.filterUser")}
                    onSearch={(value) => {
                        setUserId(value.trim());
                        setPage(1);
                    }}
                />
            </div>

            {ordersQuery.isError ? (
                <Alert
                    type="error"
                    showIcon
                    message={t("admin.orders.loadFailed")}
                    description={getApiErrorMessage(ordersQuery.error)}
                    action={<Button size="small" onClick={() => void ordersQuery.refetch()}>{t("admin.retry")}</Button>}
                />
            ) : (
                <Table<AdminOrder>
                    rowKey="id"
                    size="middle"
                    loading={ordersQuery.isPending}
                    columns={columns}
                    dataSource={ordersQuery.data?.items ?? []}
                    scroll={{ x: 1100 }}
                    pagination={{
                        current: page,
                        pageSize: size,
                        total: ordersQuery.data?.total ?? 0,
                        showSizeChanger: true,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setSize(nextSize);
                        },
                    }}
                />
            )}
        </div>
    );
}
