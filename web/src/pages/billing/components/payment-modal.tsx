import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Modal, QRCode, Spin, Typography } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { useCopyText } from "@/hooks/use-copy-text";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import { getOrder, type CreateOrderResponse } from "@/services/api/orders";
import { useAuthStore } from "@/stores/use-auth-store";

const POLL_INTERVAL_MS = 3000;
const POLL_TIMEOUT_MS = 10 * 60 * 1000;

export function PaymentModal({ open, data, onClose }: { open: boolean; data: CreateOrderResponse | null; onClose: () => void }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const copyText = useCopyText();
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const [pollingExpired, setPollingExpired] = useState(false);
    const paidHandledRef = useRef<string | null>(null);

    const orderId = data?.order.id ?? "";
    const paymentType = data?.payment.type;
    const isQrcode = paymentType === "qrcode";

    useEffect(() => {
        setPollingExpired(false);
        paidHandledRef.current = null;
    }, [orderId, open]);

    useEffect(() => {
        if (!open || !isQrcode) return;
        const timer = window.setTimeout(() => setPollingExpired(true), POLL_TIMEOUT_MS);
        return () => window.clearTimeout(timer);
    }, [open, isQrcode, orderId]);

    const orderQuery = useQuery({
        queryKey: ["order", userId, orderId],
        queryFn: ({ signal }) => getOrder(orderId, signal),
        enabled: open && isQrcode && !!orderId,
        refetchInterval: (query) => {
            if (pollingExpired) return false;
            const current = query.state.data;
            return current && current.status !== "pending" ? false : POLL_INTERVAL_MS;
        },
    });

    const orderPaid = data?.order.status === "paid" || orderQuery.data?.status === "paid";

    useEffect(() => {
        if (!open || !orderPaid || !orderId || paidHandledRef.current === orderId) return;
        paidHandledRef.current = orderId;
        message.success(t("billing.payment.paid"));
        void queryClient.invalidateQueries({ queryKey: ["me", userId] });
        void queryClient.invalidateQueries({ queryKey: ["credits", userId] });
        void queryClient.invalidateQueries({ queryKey: ["orders", userId] });
    }, [open, orderPaid, orderId, message, queryClient, t, userId]);

    const refreshStatus = async () => {
        await Promise.all([
            queryClient.invalidateQueries({ queryKey: ["orders", userId] }),
            queryClient.invalidateQueries({ queryKey: ["me", userId] }),
            queryClient.invalidateQueries({ queryKey: ["credits", userId] }),
        ]);
        message.success(t("billing.payment.refreshed"));
    };

    const openRedirect = () => {
        if (data?.payment.payload) window.open(data.payment.payload, "_blank", "noopener,noreferrer");
    };

    const title = isQrcode ? t("billing.payment.title") : paymentType === "redirect" ? t("billing.payment.redirectTitle") : t("billing.payment.clientSecretTitle");
    const providerLabel = data?.order.provider ? t(`billing.providers.${data.order.provider}`) : "";

    return (
        <Modal
            open={open}
            title={title}
            footer={null}
            width={420}
            onCancel={onClose}
        >
            {data ? (
                <div className="flex flex-col items-center gap-5">
                    {isQrcode ? (
                        <div className="flex flex-col items-center gap-3">
                            {orderPaid ? (
                                <Alert type="success" showIcon message={t("billing.payment.paid")} />
                            ) : pollingExpired ? (
                                <Alert type="info" showIcon message={t("billing.payment.timeoutTitle")} description={t("billing.payment.timeoutHint")} />
                            ) : (
                                <>
                                    <QRCode value={data.payment.payload} size={200} />
                                    <p className="text-sm text-stone-500 dark:text-stone-400">{t("billing.payment.scanHint", { provider: providerLabel })}</p>
                                    <span className="flex items-center gap-2 text-xs text-stone-500 dark:text-stone-400">
                                        <Spin size="small" />
                                        {t("billing.payment.waiting")}
                                    </span>
                                </>
                            )}
                        </div>
                    ) : paymentType === "redirect" ? (
                        <div className="w-full">
                            <Alert type="info" showIcon message={t("billing.payment.redirectHint")} />
                        </div>
                    ) : (
                        <div className="w-full">
                            <Alert type="info" showIcon message={t("billing.payment.clientSecretHint")} />
                            <div className="mt-3 flex items-start gap-2 rounded-lg bg-black/[0.03] p-3 dark:bg-white/[0.06]">
                                <Typography.Text className="min-w-0 flex-1 break-all text-xs">{data.payment.payload}</Typography.Text>
                                <Button size="small" onClick={() => copyText(data.payment.payload)}>
                                    {t("billing.payment.copyPayload")}
                                </Button>
                            </div>
                        </div>
                    )}
                    <div className="w-full rounded-lg bg-black/[0.03] p-4 text-sm dark:bg-white/[0.06]">
                        <div className="flex items-center justify-between gap-3">
                            <span className="text-stone-500 dark:text-stone-400">{t("billing.payment.amount")}</span>
                            <span className="font-medium">{formatMoney(data.order.priceMicros, data.order.currency)}</span>
                        </div>
                        <div className="mt-2 flex items-center justify-between gap-3">
                            <span className="text-stone-500 dark:text-stone-400">{t("billing.payment.points")}</span>
                            <span className="font-medium">{formatPoints(data.order.purchasedMicros)}</span>
                        </div>
                        {data.order.grantedMicros > 0 ? (
                            <div className="mt-2 flex items-center justify-between gap-3">
                                <span className="text-stone-500 dark:text-stone-400">{t("billing.payment.bonus")}</span>
                                <span className="font-medium">{formatPoints(data.order.grantedMicros)}</span>
                            </div>
                        ) : null}
                        <div className="mt-2 flex items-center justify-between gap-3">
                            <span className="text-stone-500 dark:text-stone-400">{t("billing.payment.entitlement")}</span>
                            <span className="font-medium">{t("billing.entitlement", { days: data.order.entitlementDays })}</span>
                        </div>
                    </div>
                    <div className="flex w-full justify-end gap-3">
                        <Button onClick={onClose}>{orderPaid ? t("billing.payment.done") : t("billing.payment.close")}</Button>
                        {!orderPaid && paymentType === "redirect" ? (
                            <>
                                <Button onClick={() => void refreshStatus()}>{t("billing.payment.refresh")}</Button>
                                <Button type="primary" onClick={openRedirect}>
                                    {t("billing.payment.openRedirect")}
                                </Button>
                            </>
                        ) : null}
                        {!orderPaid && !isQrcode && paymentType !== "redirect" ? <Button onClick={() => void refreshStatus()}>{t("billing.payment.refresh")}</Button> : null}
                    </div>
                </div>
            ) : null}
        </Modal>
    );
}
