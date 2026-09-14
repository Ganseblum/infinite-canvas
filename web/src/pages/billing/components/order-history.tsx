import { useState } from "react";
import { Alert, App, Button, Empty, Popconfirm, Segmented, Skeleton, Tag } from "antd";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import type { CreditPackage } from "@/services/api/credits";
import { cancelOrder, listOrders, type Order, type OrderStatus } from "@/services/api/orders";
import { useAuthStore } from "@/stores/use-auth-store";

const STATUS_COLORS: Record<string, string> = {
    pending: "processing",
    paid: "success",
    failed: "error",
    refunded: "default",
};

const STATUS_FILTERS: Array<OrderStatus | "all"> = ["all", "pending", "paid", "failed"];

function OrderRow({ order, packageName, onCancel, canceling }: { order: Order; packageName?: string; onCancel: (id: string) => void; canceling: boolean }) {
    const { t } = useTranslation();
    const label = t(`billing.orderStatus.${order.status}`);

    return (
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-3.5 text-sm">
            <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{packageName || order.packageId}</span>
                    <Tag color={STATUS_COLORS[order.status]} className="m-0">
                        {label}
                    </Tag>
                    {order.provider ? <span className="text-xs text-stone-400 dark:text-stone-500">{t(`billing.providers.${order.provider}`)}</span> : null}
                </div>
                <p className="mt-1 text-xs text-stone-400 dark:text-stone-500">
                    {dayjs(order.createdAt).format("YYYY-MM-DD HH:mm")} · {t("billing.points", { points: formatPoints(order.purchasedMicros + order.grantedMicros) })}
                </p>
            </div>
            <div className="flex items-center gap-3">
                <span className="font-medium">{formatMoney(order.priceMicros, order.currency)}</span>
                {order.status === "pending" ? (
                    <Popconfirm title={t("billing.cancelConfirm")} okText={t("billing.cancel")} cancelText={t("common.cancel")} onConfirm={() => onCancel(order.id)}>
                        <Button size="small" loading={canceling}>
                            {t("billing.cancel")}
                        </Button>
                    </Popconfirm>
                ) : null}
            </div>
        </div>
    );
}

export function OrderHistory({ packageMap }: { packageMap: Map<string, CreditPackage> }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const status = useAuthStore((state) => state.status);
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const [statusFilter, setStatusFilter] = useState<OrderStatus | "all">("all");

    const ordersQuery = useInfiniteQuery({
        queryKey: ["orders", userId, statusFilter],
        queryFn: ({ pageParam, signal }) => listOrders({ cursor: pageParam, size: 20, status: statusFilter === "all" ? undefined : statusFilter }, signal),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
        enabled: status === "authenticated",
        refetchOnWindowFocus: true,
    });

    const cancelMutation = useMutation({
        mutationFn: (id: string) => cancelOrder(id),
        onSuccess: async () => {
            message.success(t("billing.cancelSuccess"));
            await queryClient.invalidateQueries({ queryKey: ["orders", userId] });
        },
        onError: async (error) => {
            message.error(getApiErrorMessage(error));
            await queryClient.invalidateQueries({ queryKey: ["orders", userId] });
        },
    });

    const orders = ordersQuery.data?.pages.flatMap((page) => page.items) ?? [];

    if (status === "unauthenticated") return null;

    return (
        <section className="mt-10">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 className="text-lg font-semibold">{t("billing.ordersTitle")}</h2>
                <Segmented
                    value={statusFilter}
                    onChange={(value) => setStatusFilter(value as OrderStatus | "all")}
                    options={STATUS_FILTERS.map((value) => ({ label: t(`billing.orderStatus.${value}`), value }))}
                />
            </div>
            <div className="mt-4 overflow-hidden rounded-xl border border-stone-200 dark:border-stone-800">
                {ordersQuery.isError ? (
                    <div className="p-5">
                        <Alert
                            type="error"
                            showIcon
                            message={t("billing.ordersFailed")}
                            description={getApiErrorMessage(ordersQuery.error)}
                            action={
                                <Button size="small" onClick={() => void ordersQuery.refetch()}>
                                    {t("billing.retry")}
                                </Button>
                            }
                        />
                    </div>
                ) : ordersQuery.isPending ? (
                    <div className="p-5">
                        <Skeleton active paragraph={{ rows: 4 }} />
                    </div>
                ) : orders.length === 0 ? (
                    <Empty className="py-10" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("billing.noOrders")} />
                ) : (
                    <>
                        <div className="divide-y divide-stone-100 dark:divide-stone-800/60">
                            {orders.map((order) => (
                                <OrderRow
                                    key={order.id}
                                    order={order}
                                    packageName={packageMap.get(order.packageId)?.name}
                                    onCancel={(id) => cancelMutation.mutate(id)}
                                    canceling={cancelMutation.isPending && cancelMutation.variables === order.id}
                                />
                            ))}
                        </div>
                        {ordersQuery.hasNextPage ? (
                            <div className="border-t border-stone-200 px-5 py-3 text-center dark:border-stone-800">
                                <Button size="small" loading={ordersQuery.isFetchingNextPage} onClick={() => void ordersQuery.fetchNextPage()}>
                                    {t("billing.loadMore")}
                                </Button>
                            </div>
                        ) : null}
                    </>
                )}
            </div>
        </section>
    );
}
