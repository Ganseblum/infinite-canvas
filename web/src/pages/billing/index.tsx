import { useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Skeleton, Tag } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import { formatPoints } from "@/lib/credits-format";
import { getCreditPackages, getCredits } from "@/services/api/credits";
import { createOrder, type CreateOrderResponse, type PaymentProvider } from "@/services/api/orders";
import { useAuthStore } from "@/stores/use-auth-store";

import { OrderHistory } from "./components/order-history";
import { PackageGrid } from "./components/package-grid";
import { PaymentModal } from "./components/payment-modal";

/**
 * 从限流错误里提取 Retry-After 秒数（向上取整）。
 * @returns 可用于倒计时的秒数；非限流错误或取值非法时返回 null
 */
function retryAfterSeconds(error: unknown): number | null {
    if (!(error instanceof ApiError)) return null;
    const seconds = error.retryAfter;
    return typeof seconds === "number" && Number.isFinite(seconds) && seconds > 0 ? Math.ceil(seconds) : null;
}

/** 充值页入口：点数余额概览 + 套餐下单 + 订单历史。
 * 下单成功打开支付弹窗；被限流时进入秒级倒计时，期间禁用全部购买按钮。
 */
export default function BillingPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const status = useAuthStore((state) => state.status);
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const authenticated = status === "authenticated";
    const [provider, setProvider] = useState<PaymentProvider>("alipay");
    const [payment, setPayment] = useState<CreateOrderResponse | null>(null);
    const [rateLimitSeconds, setRateLimitSeconds] = useState(0);

    const packagesQuery = useQuery({
        queryKey: ["credit-packages"],
        queryFn: ({ signal }) => getCreditPackages(signal),
        enabled: authenticated,
    });
    const creditsQuery = useQuery({
        queryKey: ["credits", userId],
        queryFn: ({ signal }) => getCredits(signal),
        enabled: authenticated,
        // 支付完成切回本页时自动刷新余额，避免看到下单前的旧值。
        refetchOnWindowFocus: true,
    });

    // 限流倒计时：每秒递减到 0，期间购买按钮保持禁用。
    useEffect(() => {
        if (rateLimitSeconds <= 0) return;
        const timer = window.setTimeout(() => setRateLimitSeconds((value) => Math.max(0, value - 1)), 1000);
        return () => window.clearTimeout(timer);
    }, [rateLimitSeconds]);

    const createMutation = useMutation({
        mutationFn: (packageId: string) => createOrder({ packageId, provider }),
        onSuccess: async (result) => {
            setPayment(result);
            await queryClient.invalidateQueries({ queryKey: ["orders", userId] });
        },
        onError: (error) => {
            // 命中下单限流时不弹错误，改为页面顶部倒计时提示。
            const seconds = retryAfterSeconds(error);
            if (seconds) {
                setRateLimitSeconds(seconds);
                return;
            }
            message.error(getApiErrorMessage(error));
        },
    });

    const packageItems = packagesQuery.data?.items;
    const availableProviders = useMemo(() => (packagesQuery.data?.providers ?? []) as PaymentProvider[], [packagesQuery.data?.providers]);
    const packageMap = useMemo(() => new Map((packageItems ?? []).map((item) => [item.id, item])), [packageItems]);

    // 渠道未配置时自动选中第一个可用渠道，只配一个渠道时不展示切换。
    useEffect(() => {
        if (availableProviders.length > 0 && !availableProviders.includes(provider)) {
            setProvider(availableProviders[0]);
        }
    }, [availableProviders, provider]);

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6">
                <div className="flex flex-wrap items-start justify-between gap-4">
                    <div>
                        <h1 className="text-2xl font-semibold">{t("billing.title")}</h1>
                        <p className="mt-2 max-w-2xl text-sm text-stone-500 dark:text-stone-400">{t("billing.description")}</p>
                    </div>
                </div>

                {status === "unauthenticated" ? (
                    <Alert
                        className="mt-6"
                        type="warning"
                        showIcon
                        message={t("billing.signInRequired")}
                        action={
                            <Button size="small" type="primary" onClick={() => navigate("/login")}>
                                {t("billing.goLogin")}
                            </Button>
                        }
                    />
                ) : null}

                <div className="mt-6 flex flex-wrap items-center justify-between gap-4 rounded-xl border border-stone-200 p-5 dark:border-stone-800">
                    <div>
                        <p className="text-sm text-stone-500 dark:text-stone-400">{t("billing.balance")}</p>
                        <p className="mt-1 text-2xl font-semibold">{creditsQuery.data ? formatPoints(creditsQuery.data.totalMicros) : "—"}</p>
                    </div>
                    <div className="flex flex-wrap items-center gap-4 text-sm">
                        <span className="text-stone-500 dark:text-stone-400">
                            {t("billing.purchased")}
                            <span className="ml-1.5 font-medium text-stone-950 dark:text-stone-100">{creditsQuery.data ? formatPoints(creditsQuery.data.purchasedMicros) : "—"}</span>
                        </span>
                        <span className="text-stone-500 dark:text-stone-400">
                            {t("billing.granted")}
                            <span className="ml-1.5 font-medium text-stone-950 dark:text-stone-100">{creditsQuery.data ? formatPoints(creditsQuery.data.grantedMicros) : "—"}</span>
                        </span>
                        {creditsQuery.data?.paidUntil ? <Tag className="m-0">{t("billing.paidUntil", { date: dayjs(creditsQuery.data.paidUntil).format("YYYY-MM-DD") })}</Tag> : null}
                    </div>
                </div>

                {rateLimitSeconds > 0 ? <Alert className="mt-4" type="info" showIcon message={t("billing.rateLimited", { seconds: rateLimitSeconds })} /> : null}

                {status === "unauthenticated" ? null : packagesQuery.isError ? (
                    <Alert
                        className="mt-6"
                        type="error"
                        showIcon
                        message={t("billing.packagesFailed")}
                        description={getApiErrorMessage(packagesQuery.error)}
                        action={
                            <Button size="small" onClick={() => void packagesQuery.refetch()}>
                                {t("billing.retry")}
                            </Button>
                        }
                    />
                ) : packagesQuery.isPending ? (
                    <div className="mt-6 grid gap-4 md:grid-cols-3">
                        <Skeleton active paragraph={{ rows: 4 }} />
                        <Skeleton active paragraph={{ rows: 4 }} />
                        <Skeleton active paragraph={{ rows: 4 }} />
                    </div>
                ) : (
                    <PackageGrid
                        packages={packageItems ?? []}
                        provider={provider}
                        providers={availableProviders}
                        onProviderChange={setProvider}
                        onBuy={(packageId) => createMutation.mutate(packageId)}
                        buyingPackageId={createMutation.isPending ? createMutation.variables : undefined}
                        disabled={rateLimitSeconds > 0 || availableProviders.length === 0}
                    />
                )}

                <OrderHistory packageMap={packageMap} />
            </div>
            <PaymentModal open={!!payment} data={payment} onClose={() => setPayment(null)} />
        </main>
    );
}
