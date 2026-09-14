import { Alert, Button, Empty, Skeleton } from "antd";
import { useQuery } from "@tanstack/react-query";
import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import { formatBytes } from "@/lib/image-utils";
import { getCreditPackages, getPlans } from "@/services/api/credits";
import { useAuthStore } from "@/stores/use-auth-store";

export default function PricingPage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const status = useAuthStore((state) => state.status);
    const authenticated = status === "authenticated";

    const plansQuery = useQuery({
        queryKey: ["plans"],
        queryFn: ({ signal }) => getPlans(signal),
        enabled: authenticated,
    });
    const packagesQuery = useQuery({
        queryKey: ["credit-packages"],
        queryFn: ({ signal }) => getCreditPackages(signal),
        enabled: authenticated,
    });

    const freePlan = plansQuery.data?.items.find((plan) => plan.id === "free");
    const paidPlan = plansQuery.data?.items.find((plan) => plan.id === "paid");
    const packages = packagesQuery.data?.items ?? [];

    const comparisonRows =
        freePlan && paidPlan
            ? [
                  { key: "storage", free: formatBytes(freePlan.storageBytes), paid: formatBytes(paidPlan.storageBytes) },
                  { key: "maxFile", free: formatBytes(freePlan.maxFileBytes), paid: formatBytes(paidPlan.maxFileBytes) },
                  { key: "retention", free: t("pricing.retentionValue", { count: freePlan.retentionDays }), paid: t("pricing.retentionValue", { count: paidPlan.retentionDays }) },
                  {
                      key: "imageTrials",
                      free: freePlan.freeImageTrials ? t("pricing.trialsValue", { count: freePlan.freeImageTrials }) : t("pricing.trialsNone"),
                      paid: paidPlan.freeImageTrials ? t("pricing.trialsValue", { count: paidPlan.freeImageTrials }) : t("pricing.trialsNone"),
                  },
                  {
                      key: "videoTrials",
                      free: freePlan.freeVideoTrials ? t("pricing.trialsValue", { count: freePlan.freeVideoTrials }) : t("pricing.trialsNone"),
                      paid: paidPlan.freeVideoTrials ? t("pricing.trialsValue", { count: paidPlan.freeVideoTrials }) : t("pricing.trialsNone"),
                  },
              ]
            : [];

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="bg-[radial-gradient(#e5e7eb_1px,transparent_1px)] [background-size:16px_16px] dark:bg-[radial-gradient(rgba(245,245,244,.14)_1px,transparent_1px)]">
                <section className="mx-auto max-w-6xl px-4 pb-4 pt-14 text-center sm:px-6">
                    <h1 className="text-4xl font-semibold sm:text-5xl">{t("pricing.title")}</h1>
                    <p className="mx-auto mt-4 max-w-2xl text-base leading-7 text-stone-500 dark:text-stone-400">{t("pricing.description")}</p>
                    {status === "unauthenticated" ? (
                        <Alert
                            className="mx-auto mt-6 max-w-xl text-left"
                            type="warning"
                            showIcon
                            message={t("pricing.signInRequired")}
                            action={
                                <Button size="small" type="primary" onClick={() => navigate("/login")}>
                                    {t("pricing.goLogin")}
                                </Button>
                            }
                        />
                    ) : null}
                </section>

                <section className="mx-auto max-w-6xl px-4 pb-16 pt-10 sm:px-6">
                    <h2 className="mb-5 text-center text-lg font-semibold">{t("pricing.packagesTitle")}</h2>
                    {status === "unauthenticated" ? null : packagesQuery.isError ? (
                        <Alert
                            type="error"
                            showIcon
                            message={t("pricing.packagesFailed")}
                            description={getApiErrorMessage(packagesQuery.error)}
                            action={
                                <Button size="small" onClick={() => void packagesQuery.refetch()}>
                                    {t("pricing.retry")}
                                </Button>
                            }
                        />
                    ) : packagesQuery.isPending ? (
                        <div className="grid gap-4 md:grid-cols-3">
                            <Skeleton active paragraph={{ rows: 4 }} />
                            <Skeleton active paragraph={{ rows: 4 }} />
                            <Skeleton active paragraph={{ rows: 4 }} />
                        </div>
                    ) : packages.length === 0 ? (
                        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("pricing.packagesEmpty")} />
                    ) : (
                        <div className="grid gap-4 md:grid-cols-3">
                            {packages.map((item) => (
                                <div key={item.id} className="flex flex-col rounded-xl border border-stone-200 p-7 dark:border-stone-800">
                                    <h3 className="font-medium">{item.name}</h3>
                                    <p className="mt-3 text-4xl font-semibold">{formatMoney(item.priceMicros, item.currency)}</p>
                                    <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("pricing.points", { points: formatPoints(item.purchasedMicros) })}</p>
                                    {item.bonusMicros > 0 ? <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("pricing.bonus", { points: formatPoints(item.bonusMicros) })}</p> : null}
                                    <ul className="mt-6 space-y-3 text-sm">
                                        {item.bonusMicros > 0 ? (
                                            <li className="flex gap-2.5">
                                                <Check className="mt-0.5 size-4 shrink-0" />
                                                {t("pricing.bonus", { points: formatPoints(item.bonusMicros) })}
                                            </li>
                                        ) : null}
                                        <li className="flex gap-2.5">
                                            <Check className="mt-0.5 size-4 shrink-0" />
                                            {t("pricing.entitlement", { days: item.entitlementDays })}
                                        </li>
                                    </ul>
                                    <Button className="mt-6" type="primary" onClick={() => navigate("/billing")}>
                                        {t("pricing.buy")}
                                    </Button>
                                </div>
                            ))}
                        </div>
                    )}
                </section>

                <section className="mx-auto max-w-4xl px-4 pb-16 sm:px-6">
                    <div className="mb-6 text-center">
                        <h2 className="text-2xl font-semibold">{t("pricing.plansTitle")}</h2>
                        <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("pricing.plansDescription")}</p>
                    </div>
                    {status === "unauthenticated" ? null : plansQuery.isError ? (
                        <Alert
                            type="error"
                            showIcon
                            message={t("pricing.plansFailed")}
                            description={getApiErrorMessage(plansQuery.error)}
                            action={
                                <Button size="small" onClick={() => void plansQuery.refetch()}>
                                    {t("pricing.retry")}
                                </Button>
                            }
                        />
                    ) : plansQuery.isPending ? (
                        <Skeleton active paragraph={{ rows: 6 }} />
                    ) : (
                        <div className="overflow-x-auto rounded-xl border border-stone-200 dark:border-stone-800">
                            <table className="w-full min-w-[520px]">
                                <thead className="border-b border-stone-200 dark:border-stone-800">
                                    <tr>
                                        <th className="px-5 py-3 text-left text-xs font-medium uppercase tracking-wide text-stone-400 dark:text-stone-500">{t("pricing.columns.benefit")}</th>
                                        <th className="px-5 py-3 text-left text-xs font-medium uppercase tracking-wide text-stone-400 dark:text-stone-500">{t("pricing.columns.free")}</th>
                                        <th className="px-5 py-3 text-left text-xs font-medium uppercase tracking-wide text-stone-400 dark:text-stone-500">{t("pricing.columns.paid")}</th>
                                    </tr>
                                </thead>
                                <tbody className="divide-y divide-stone-100 text-sm dark:divide-stone-800/60">
                                    {comparisonRows.map((row) => (
                                        <tr key={row.key}>
                                            <td className="px-5 py-3.5">{t(`pricing.rows.${row.key}`)}</td>
                                            <td className="px-5 py-3.5 text-stone-500 dark:text-stone-400">{row.free}</td>
                                            <td className="px-5 py-3.5 font-medium">{row.paid}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </section>

                <section className="mx-auto max-w-4xl px-4 pb-24 sm:px-6">
                    <div className="flex flex-col items-center justify-between gap-4 rounded-xl border border-stone-200 p-6 text-center sm:flex-row sm:text-left dark:border-stone-800">
                        <div>
                            <h2 className="font-medium">{t("pricing.ctaTitle")}</h2>
                            <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("pricing.ctaDescription")}</p>
                        </div>
                        <Button type="primary" onClick={() => navigate("/billing")}>
                            {t("pricing.cta")}
                        </Button>
                    </div>
                </section>
            </div>
        </main>
    );
}
