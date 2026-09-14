import { Progress, Typography } from "antd";
import { useTranslation } from "react-i18next";

import { formatBytes } from "@/lib/image-utils";
import type { PlanDetail, UsageSummary } from "@/services/api/account";

export function UsageSection({ plan, usage }: { plan: PlanDetail; usage?: UsageSummary }) {
    const { t } = useTranslation();
    const limit = plan.storageBytes;
    const used = usage?.storageBytes ?? 0;
    const ratio = usage && limit > 0 ? used / limit : 0;
    const percent = usage ? Math.min(100, Math.round(ratio * 1000) / 10) : 0;
    const nearLimit = ratio >= 0.9;

    return (
        <section id="profile-usage" className="scroll-mt-4 rounded-xl border border-stone-200 p-6 dark:border-stone-800">
            <h2 className="text-lg font-semibold">{t("profile.usage.title")}</h2>
            {usage ? (
                <>
                    <div className="mt-4 flex items-end justify-between gap-3">
                        <div>
                            <p className="text-sm text-stone-500 dark:text-stone-400">{t("profile.usage.storage")}</p>
                            <p className="mt-1 text-lg font-semibold">{t("profile.usage.storageValue", { used: formatBytes(used), limit: formatBytes(limit) })}</p>
                        </div>
                        <span className="text-sm text-stone-500 dark:text-stone-400">{percent}%</span>
                    </div>
                    <Progress className="mt-2" percent={percent} status={nearLimit ? "exception" : "normal"} showInfo={false} />
                    {nearLimit ? (
                        <Typography.Text type="danger" className="mt-2 block text-xs">
                            {t("profile.usage.nearLimit")}
                        </Typography.Text>
                    ) : null}
                </>
            ) : (
                <p className="mt-4 text-sm text-stone-500 dark:text-stone-400">{t("profile.usage.noUsage")}</p>
            )}
            <div className="mt-4 grid gap-3 border-t border-stone-200 pt-4 text-sm sm:grid-cols-2 dark:border-stone-800">
                <div className="flex items-center justify-between gap-3">
                    <span className="text-stone-500 dark:text-stone-400">{t("profile.usage.maxFile")}</span>
                    <span>{formatBytes(plan.maxFileBytes)}</span>
                </div>
                <div className="flex items-center justify-between gap-3">
                    <span className="text-stone-500 dark:text-stone-400">{t("profile.usage.retention")}</span>
                    <span>{t("profile.usage.retentionValue", { count: plan.retentionDays })}</span>
                </div>
            </div>
        </section>
    );
}
