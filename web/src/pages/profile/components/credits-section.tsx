import { useState } from "react";
import type { ComponentType } from "react";
import { Alert, Button, Empty, Skeleton, Typography } from "antd";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { Clock, Gift, PlusCircle, RotateCcw, Sparkles } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatPoints, formatSignedPoints } from "@/lib/credits-format";
import { getCredits, listCreditTransactions, type CreditTransaction, type CreditTransactionType } from "@/services/api/credits";
import { useAuthStore } from "@/stores/use-auth-store";

// 点数流水的类型到图标组件的映射，覆盖服务端全部交易类型。
const TYPE_ICONS: Record<CreditTransactionType, ComponentType<{ className?: string }>> = {
    purchase: PlusCircle,
    consume: Sparkles,
    refund: RotateCcw,
    grant: Gift,
    expire: Clock,
};

/** 点数流水的单行展示：图标 + 摘要 + 时间/来源桶 + 带符号金额。
 * 金额以 micros（百万分之一点）存储，正数为收入、负数为支出。
 * @param transaction 单条点数流水记录
 */
function TransactionRow({ transaction }: { transaction: CreditTransaction }) {
    const { t } = useTranslation();
    const Icon = TYPE_ICONS[transaction.type] ?? Sparkles;

    return (
        <div className="flex items-center justify-between gap-4 px-5 py-3.5 text-sm">
            <div className="flex min-w-0 items-center gap-3">
                <Icon className="size-4 shrink-0 text-stone-400 dark:text-stone-500" />
                <div className="min-w-0">
                    <p className="truncate">{transaction.note || t(`profile.credits.types.${transaction.type}`)}</p>
                    <p className="mt-0.5 text-xs text-stone-400 dark:text-stone-500">
                        {dayjs(transaction.createdAt).format("YYYY-MM-DD HH:mm")} · {t(`profile.credits.buckets.${transaction.bucket}`)}
                    </p>
                </div>
            </div>
            <Typography.Text type={transaction.amountMicros >= 0 ? "success" : "danger"} className="shrink-0 font-medium">
                {formatSignedPoints(transaction.amountMicros)}
            </Typography.Text>
        </div>
    );
}

/** 个人中心点数区块：余额三类构成卡片 + 流水列表；折叠态显示余额接口自带的最近几条，
 * 展开后切换为分页流水的无限加载视图。
 */
export function CreditsSection() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const status = useAuthStore((state) => state.status);
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const authenticated = status === "authenticated";
    const [showAll, setShowAll] = useState(false);

    const creditsQuery = useQuery({
        queryKey: ["credits", userId],
        queryFn: ({ signal }) => getCredits(signal),
        enabled: authenticated,
    });
    const transactionsQuery = useInfiniteQuery({
        queryKey: ["credits", userId, "transactions"],
        queryFn: ({ pageParam, signal }) => listCreditTransactions({ cursor: pageParam, size: 20 }, signal),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
        enabled: authenticated,
    });

    const balance = creditsQuery.data;
    // 余额接口自带最近流水，折叠态直接取前 5 条，无需额外请求。
    const recent = balance?.recent?.slice(0, 5) ?? [];
    const transactions = transactionsQuery.data?.pages.flatMap((page) => page.items) ?? [];

    return (
        <section id="profile-credits" className="scroll-mt-4 rounded-xl border border-stone-200 dark:border-stone-800">
            <div className="px-6 pt-6">
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <h2 className="text-lg font-semibold">{t("profile.credits.title")}</h2>
                    <Button size="small" type="primary" onClick={() => navigate("/billing")}>
                        {t("profile.credits.topUp")}
                    </Button>
                </div>
                {creditsQuery.isError ? (
                    <Alert
                        className="mt-4"
                        type="error"
                        showIcon
                        message={t("profile.credits.loadFailed")}
                        description={getApiErrorMessage(creditsQuery.error)}
                        action={<Button size="small" onClick={() => void creditsQuery.refetch()}>{t("profile.retry")}</Button>}
                    />
                ) : (
                    <div className="mt-4 grid gap-4 sm:grid-cols-3">
                        {[
                            { key: "purchased", value: balance?.purchasedMicros },
                            { key: "granted", value: balance?.grantedMicros },
                            { key: "total", value: balance?.totalMicros },
                        ].map((item) => (
                            <div key={item.key} className="rounded-lg bg-black/[0.03] px-4 py-3 dark:bg-white/[0.06]">
                                <p className="text-xs text-stone-500 dark:text-stone-400">{t(`profile.credits.${item.key}`)}</p>
                                <p className="mt-1 text-lg font-semibold">{balance ? formatPoints(item.value ?? 0) : "—"}</p>
                            </div>
                        ))}
                    </div>
                )}
                <p className="mt-3 text-xs text-stone-500 dark:text-stone-400">{t("profile.credits.spendHint")}</p>
            </div>

            <div className="mt-5 border-t border-stone-200 dark:border-stone-800">
                <div className="flex items-center justify-between px-6 py-3.5">
                    <h3 className="text-sm font-medium">{showAll ? t("profile.credits.allTitle") : t("profile.credits.recentTitle")}</h3>
                    <Button size="small" type="link" className="!px-0" onClick={() => setShowAll((value) => !value)}>
                        {showAll ? t("profile.credits.collapse") : t("profile.credits.viewAll")}
                    </Button>
                </div>
                {showAll ? (
                    <>
                        {transactionsQuery.isError ? (
                            <div className="px-6 pb-5">
                                <Alert
                                    type="error"
                                    showIcon
                                    message={t("profile.credits.transactionsFailed")}
                                    description={getApiErrorMessage(transactionsQuery.error)}
                                    action={<Button size="small" onClick={() => void transactionsQuery.refetch()}>{t("profile.retry")}</Button>}
                                />
                            </div>
                        ) : transactionsQuery.isPending ? (
                            <div className="px-6 pb-5">
                                <Skeleton active paragraph={{ rows: 4 }} />
                            </div>
                        ) : transactions.length === 0 ? (
                            <Empty className="py-8" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("profile.credits.empty")} />
                        ) : (
                            <>
                                <div className="divide-y divide-stone-100 dark:divide-stone-800/60">
                                    {transactions.map((transaction) => (
                                        <TransactionRow key={transaction.id} transaction={transaction} />
                                    ))}
                                </div>
                                {transactionsQuery.hasNextPage ? (
                                    <div className="border-t border-stone-200 px-6 py-3 text-center dark:border-stone-800">
                                        <Button size="small" loading={transactionsQuery.isFetchingNextPage} onClick={() => void transactionsQuery.fetchNextPage()}>
                                            {transactionsQuery.isFetchingNextPage ? t("profile.credits.loadingMore") : t("profile.credits.loadMore")}
                                        </Button>
                                    </div>
                                ) : null}
                            </>
                        )}
                    </>
                ) : creditsQuery.isPending ? (
                    <div className="px-6 pb-5">
                        <Skeleton active paragraph={{ rows: 3 }} />
                    </div>
                ) : recent.length === 0 ? (
                    <Empty className="py-8" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("profile.credits.empty")} />
                ) : (
                    <div className="divide-y divide-stone-100 dark:divide-stone-800/60">
                        {recent.map((transaction) => (
                            <TransactionRow key={transaction.id} transaction={transaction} />
                        ))}
                    </div>
                )}
            </div>
        </section>
    );
}
