import { Alert, Button, Empty, Segmented } from "antd";
import { useTranslation } from "react-i18next";

import { formatMoney, formatPoints } from "@/lib/credits-format";
import type { CreditPackage } from "@/services/api/credits";
import type { PaymentProvider } from "@/services/api/orders";

type PackageGridProps = {
    packages: CreditPackage[];
    provider: PaymentProvider;
    providers: PaymentProvider[];
    onProviderChange: (provider: PaymentProvider) => void;
    onBuy: (packageId: string) => void;
    buyingPackageId?: string;
    disabled?: boolean;
};

export function PackageGrid({ packages, provider, providers, onProviderChange, onBuy, buyingPackageId, disabled }: PackageGridProps) {
    const { t } = useTranslation();

    return (
        <section className="mt-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 className="text-lg font-semibold">{t("billing.packagesTitle")}</h2>
                {providers.length > 1 ? (
                    <div className="flex items-center gap-2">
                        <span className="text-sm text-stone-500 dark:text-stone-400">{t("billing.provider")}</span>
                        <Segmented
                            value={provider}
                            onChange={(value) => onProviderChange(value as PaymentProvider)}
                            options={providers.map((value) => ({ label: t(`billing.providers.${value}`), value }))}
                        />
                    </div>
                ) : null}
            </div>
            {providers.length === 0 ? <Alert className="mt-4" type="info" showIcon message={t("billing.noProvider")} /> : null}
            {packages.length === 0 ? (
                <Empty className="mt-6" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("billing.packagesEmpty")} />
            ) : (
                <div className="mt-4 grid gap-4 md:grid-cols-3">
                    {packages.map((item) => (
                        <div key={item.id} className="flex flex-col rounded-xl border border-stone-200 p-6 dark:border-stone-800">
                            <h3 className="font-medium">{item.name}</h3>
                            <p className="mt-3 text-3xl font-semibold">{formatMoney(item.priceMicros, item.currency)}</p>
                            <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("billing.points", { points: formatPoints(item.purchasedMicros) })}</p>
                            {item.bonusMicros > 0 ? <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("billing.bonus", { points: formatPoints(item.bonusMicros) })}</p> : null}
                            <p className="mt-1 text-xs text-stone-400 dark:text-stone-500">{t("billing.entitlement", { days: item.entitlementDays })}</p>
                            <Button
                                className="mt-5"
                                type="primary"
                                loading={buyingPackageId === item.id}
                                disabled={disabled || (!!buyingPackageId && buyingPackageId !== item.id)}
                                onClick={() => onBuy(item.id)}
                            >
                                {buyingPackageId === item.id ? t("billing.buying") : t("billing.buy")}
                            </Button>
                        </div>
                    ))}
                </div>
            )}
        </section>
    );
}
