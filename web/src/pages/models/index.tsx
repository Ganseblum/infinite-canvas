import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Empty, Segmented, Skeleton } from "antd";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { getApiErrorMessage } from "@/lib/api-error";
import { listModels, type ModelCapability } from "@/services/api/catalog";
import { useAuthStore } from "@/stores/use-auth-store";

import { ModelCard } from "./components/model-card";

const FILTERS: Array<ModelCapability | "all"> = ["all", "image", "video", "text", "audio"];

export default function ModelsPage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const status = useAuthStore((state) => state.status);
    const authenticated = status === "authenticated";
    const [capability, setCapability] = useState<ModelCapability | "all">("all");

    const modelsQuery = useQuery({
        queryKey: ["models", capability],
        queryFn: ({ signal }) => listModels({ capability: capability === "all" ? undefined : capability }, signal),
        enabled: authenticated,
        refetchOnWindowFocus: true,
    });
    const models = modelsQuery.data?.items ?? [];
    const refetch = modelsQuery.refetch;

    const nextPricingChangeAt = useMemo(() => {
        const now = Date.now();
        const times = models
            .map((model) => model.nextPricingChangeAt)
            .filter((value): value is string => !!value)
            .map((value) => dayjs(value).valueOf())
            .filter((value) => value > now);
        return times.length ? Math.min(...times) : null;
    }, [models]);

    useEffect(() => {
        if (!nextPricingChangeAt) return;
        const delay = Math.max(1000, nextPricingChangeAt - Date.now() + 1000);
        const timer = window.setTimeout(() => void refetch(), delay);
        return () => window.clearTimeout(timer);
    }, [nextPricingChangeAt, refetch]);

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-7xl px-4 py-10 sm:px-6">
                <div className="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
                    <div>
                        <h1 className="text-3xl font-semibold">{t("models.title")}</h1>
                        <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("models.description")}</p>
                    </div>
                </div>

                {status === "unauthenticated" ? (
                    <Alert
                        className="mt-6"
                        type="warning"
                        showIcon
                        message={t("models.signInRequired")}
                        action={
                            <Button size="small" type="primary" onClick={() => navigate("/login")}>
                                {t("models.goLogin")}
                            </Button>
                        }
                    />
                ) : null}

                <div className="hide-scrollbar mt-6 overflow-x-auto pb-1">
                    <Segmented
                        value={capability}
                        onChange={(value) => setCapability(value as ModelCapability | "all")}
                        options={FILTERS.map((value) => ({ label: t(`models.filters.${value}`), value }))}
                    />
                </div>

                {status === "unauthenticated" ? null : modelsQuery.isError ? (
                    <Alert
                        className="mt-6"
                        type="error"
                        showIcon
                        message={t("models.loadFailed")}
                        description={getApiErrorMessage(modelsQuery.error)}
                        action={
                            <Button size="small" onClick={() => void modelsQuery.refetch()}>
                                {t("models.retry")}
                            </Button>
                        }
                    />
                ) : modelsQuery.isPending ? (
                    <div className="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
                        <Skeleton active paragraph={{ rows: 6 }} />
                        <Skeleton active paragraph={{ rows: 6 }} />
                        <Skeleton active paragraph={{ rows: 6 }} />
                    </div>
                ) : models.length === 0 ? (
                    <Empty className="mt-16" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("models.empty")} />
                ) : (
                    <div className="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
                        {models.map((model) => (
                            <ModelCard key={model.id} model={model} />
                        ))}
                    </div>
                )}
            </div>
        </main>
    );
}
