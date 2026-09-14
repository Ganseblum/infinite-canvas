import { Alert, Button, Skeleton } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { getApiErrorMessage } from "@/lib/api-error";
import { useAuthStore } from "@/stores/use-auth-store";

import { AccountCard } from "./components/account-card";
import { CreditsSection } from "./components/credits-section";
import { DangerZone } from "./components/danger-zone";
import { ProfileSection } from "./components/profile-section";
import { SecuritySection } from "./components/security-section";
import { StatusAlerts } from "./components/status-alerts";
import { UsageSection } from "./components/usage-section";
import { useMeQuery } from "./use-me";

const SECTIONS = [
    { id: "profile-basics", key: "basic" },
    { id: "profile-credits", key: "credits" },
    { id: "profile-usage", key: "usage" },
    { id: "profile-security", key: "security" },
    { id: "profile-danger", key: "danger" },
] as const;

export default function ProfilePage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const status = useAuthStore((state) => state.status);
    const meQuery = useMeQuery();
    const me = meQuery.data;
    const unauthenticated = status === "unauthenticated";

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6">
                <h1 className="text-2xl font-semibold">{t("profile.title")}</h1>
                <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("profile.description")}</p>

                {unauthenticated ? (
                    <Alert
                        className="mt-6"
                        type="warning"
                        showIcon
                        message={t("profile.signInRequired")}
                        action={
                            <Button size="small" type="primary" onClick={() => navigate("/login")}>
                                {t("profile.goLogin")}
                            </Button>
                        }
                    />
                ) : meQuery.isError ? (
                    <Alert
                        className="mt-6"
                        type="error"
                        showIcon
                        message={t("profile.loadFailed")}
                        description={getApiErrorMessage(meQuery.error)}
                        action={
                            <Button size="small" onClick={() => void meQuery.refetch()}>
                                {t("profile.retry")}
                            </Button>
                        }
                    />
                ) : null}

                {me ? (
                    <>
                        <StatusAlerts me={me} />
                        <div className="mt-6 grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
                            <aside className="flex flex-col gap-4">
                                <AccountCard me={me} />
                                <nav className="hidden flex-col gap-1 lg:flex">
                                    {SECTIONS.map((section) => (
                                        <a
                                            key={section.id}
                                            href={`#${section.id}`}
                                            className="rounded-lg px-3 py-2 text-sm text-stone-600 transition hover:bg-black/5 hover:text-stone-950 dark:text-stone-300 dark:hover:bg-white/10 dark:hover:text-stone-50"
                                        >
                                            {t(`profile.nav.${section.key}`)}
                                        </a>
                                    ))}
                                </nav>
                            </aside>
                            <div className="flex min-w-0 flex-col gap-6">
                                <ProfileSection user={me.user} />
                                <CreditsSection />
                                {me.plan ? <UsageSection plan={me.plan} usage={me.usage} /> : null}
                                <SecuritySection />
                                <DangerZone deletion={me.deletion} />
                            </div>
                        </div>
                    </>
                ) : unauthenticated || meQuery.isError ? null : (
                    <div className="mt-6 grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
                        <Skeleton active paragraph={{ rows: 5 }} />
                        <Skeleton active paragraph={{ rows: 14 }} />
                    </div>
                )}
            </div>
        </main>
    );
}
