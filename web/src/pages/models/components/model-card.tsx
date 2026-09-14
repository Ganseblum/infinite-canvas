import { useMemo } from "react";
import { Button, Tag } from "antd";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { formatDiscount, formatPoints } from "@/lib/credits-format";
import type { CatalogModel, ModelCapability, ModelPriceDiscount } from "@/services/api/catalog";

import { formatConstraintValue, formatPriceParams } from "../model-format";

const CAPABILITY_ROUTES: Record<ModelCapability, string> = {
    image: "/image",
    video: "/video",
    text: "/canvas",
    audio: "/canvas",
};

export function ModelCard({ model }: { model: CatalogModel }) {
    const { t } = useTranslation();
    const navigate = useNavigate();

    const constraintLabels: Record<string, string> = {
        size: t("models.constraintLabels.size"),
        ratio: t("models.constraintLabels.ratio"),
        resolution: t("models.constraintLabels.resolution"),
        quality: t("models.constraintLabels.quality"),
        duration: t("models.constraintLabels.duration"),
        n: t("models.constraintLabels.n"),
        features: t("models.constraintLabels.features"),
    };
    const qualityLabels: Record<string, string> = {
        auto: t("models.quality.auto"),
        low: t("models.quality.low"),
        medium: t("models.quality.medium"),
        high: t("models.quality.high"),
    };
    const featureLabels: Record<string, string> = {
        mask: t("models.features.mask"),
        referenceImage: t("models.features.referenceImage"),
    };

    const constraints = model.constraints;
    const constraintEntries: Array<{ label: string; values: string[] }> = [];
    if (constraints?.size?.length) constraintEntries.push({ label: constraintLabels.size, values: constraints.size.map(formatConstraintValue) });
    if (constraints?.ratio?.length) constraintEntries.push({ label: constraintLabels.ratio, values: constraints.ratio.map(formatConstraintValue) });
    if (constraints?.resolution?.length) constraintEntries.push({ label: constraintLabels.resolution, values: constraints.resolution.map(formatConstraintValue) });
    if (constraints?.quality?.length) {
        constraintEntries.push({ label: constraintLabels.quality, values: constraints.quality.map((value) => qualityLabels[formatConstraintValue(value)] ?? formatConstraintValue(value)) });
    }
    if (constraints?.duration?.length) {
        const values = constraints.duration.map((value) => {
            const raw = typeof value === "object" && value !== null ? value.value : value;
            const seconds = typeof raw === "number" ? raw : Number(raw);
            return Number.isFinite(seconds) ? t("models.durationValue", { count: seconds }) : String(raw);
        });
        constraintEntries.push({ label: constraintLabels.duration, values });
    }
    if (constraints?.n?.max) constraintEntries.push({ label: constraintLabels.n, values: [t("models.countValue", { count: constraints.n.max })] });
    if (constraints?.features?.length) constraintEntries.push({ label: constraintLabels.features, values: constraints.features.map((feature) => featureLabels[feature] ?? feature) });

    const priceEntries = useMemo(() => {
        if (model.effectivePrices?.length) {
            return model.effectivePrices.map((price) => ({
                params: price.params,
                baseMicros: price.baseCostMicros,
                finalMicros: price.finalCostMicros,
                discount: price.discount ?? null,
            }));
        }
        return (model.creditCost?.prices ?? []).map((price) => ({
            params: price.params,
            baseMicros: price.costMicros,
            finalMicros: price.costMicros,
            discount: null,
        }));
    }, [model]);

    const discounts = useMemo(() => {
        const unique = new Map<string, ModelPriceDiscount>();
        priceEntries.forEach((entry) => {
            if (entry.discount) unique.set(`${entry.discount.name}:${entry.discount.endsAt}`, entry.discount);
        });
        return Array.from(unique.values());
    }, [priceEntries]);

    return (
        <div className="flex flex-col rounded-xl border border-stone-200 p-5 transition hover:bg-black/[0.02] dark:border-stone-800 dark:hover:bg-white/[0.04]">
            <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                        <h3 className="truncate font-medium">{model.displayName}</h3>
                        {model.freeTrialEligible ? <Tag color="success" className="m-0">{t("models.freeTrial")}</Tag> : null}
                    </div>
                    <p className="mt-0.5 truncate text-xs text-stone-400 dark:text-stone-500">
                        {model.name} · {t("models.provider")} {model.provider}
                    </p>
                </div>
                <Tag className="m-0 shrink-0">{t(`models.capabilities.${model.capability}`)}</Tag>
            </div>

            <div className="mt-4">
                <h4 className="text-xs font-medium text-stone-500 dark:text-stone-400">{t("models.constraintsTitle")}</h4>
                {constraintEntries.length === 0 ? (
                    <p className="mt-1.5 text-sm text-stone-400 dark:text-stone-500">—</p>
                ) : (
                    <div className="mt-2 flex flex-wrap gap-1.5">
                        {constraintEntries.map((entry) => (
                            <Tag key={entry.label} className="m-0 !text-[11px]">
                                {entry.label}: {entry.values.join(" / ")}
                            </Tag>
                        ))}
                    </div>
                )}
            </div>

            <div className="mt-4">
                <h4 className="text-xs font-medium text-stone-500 dark:text-stone-400">{t("models.priceTitle")}</h4>
                {discounts.length > 0 ? (
                    <div className="mt-2 flex flex-wrap gap-1.5">
                        {discounts.map((discount) => {
                            const { discount: rate, percentOff } = formatDiscount(discount.discountBps);
                            return (
                                <Tag key={`${discount.name}:${discount.endsAt}`} color="success" className="m-0 !text-[11px]">
                                    {t("models.discount", { discount: rate, percentOff })} · {t("models.discountEnds", { date: dayjs(discount.endsAt).format("MM-DD HH:mm") })}
                                </Tag>
                            );
                        })}
                    </div>
                ) : null}
                {priceEntries.length === 0 ? (
                    <p className="mt-1.5 text-sm text-stone-400 dark:text-stone-500">{t("models.noPrices")}</p>
                ) : (
                    <ul className="mt-2 max-h-40 space-y-1.5 overflow-y-auto pr-1">
                        {priceEntries.map((entry, index) => (
                            <li key={index} className="flex items-center justify-between gap-3 text-sm">
                                <span className="truncate text-stone-500 dark:text-stone-400">{formatPriceParams(entry.params, constraintLabels)}</span>
                                <span className="flex shrink-0 items-center gap-2">
                                    {entry.finalMicros < entry.baseMicros ? <span className="text-xs text-stone-400 line-through dark:text-stone-500">{formatPoints(entry.baseMicros)}</span> : null}
                                    <span className="font-medium">{t("models.cost", { points: formatPoints(entry.finalMicros) })}</span>
                                    <span className="text-xs text-stone-400 dark:text-stone-500">{t(`models.unit.${model.capability}`)}</span>
                                </span>
                            </li>
                        ))}
                    </ul>
                )}
                {model.nextPricingChangeAt ? (
                    <p className="mt-2 text-xs text-stone-400 dark:text-stone-500">{t("models.priceChangeAt", { date: dayjs(model.nextPricingChangeAt).format("MM-DD HH:mm") })}</p>
                ) : null}
            </div>

            <div className="mt-4 flex items-center justify-end border-t border-stone-100 pt-4 dark:border-stone-800/60">
                <Button size="small" onClick={() => navigate(CAPABILITY_ROUTES[model.capability])}>
                    {t("models.use")}
                </Button>
            </div>
        </div>
    );
}
