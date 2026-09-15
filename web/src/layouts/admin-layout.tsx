import { Tabs } from "antd";
import { useTranslation } from "react-i18next";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

const TABS = [
    { key: "/admin", labelKey: "admin.tabs.dashboard" },
    { key: "/admin/users", labelKey: "admin.tabs.users" },
    { key: "/admin/models", labelKey: "admin.tabs.models" },
    { key: "/admin/channels", labelKey: "admin.tabs.channels" },
    { key: "/admin/moderation", labelKey: "admin.tabs.moderation" },
    { key: "/admin/system", labelKey: "admin.tabs.system" },
    { key: "/admin/credit-packages", labelKey: "admin.tabs.packages" },
    { key: "/admin/orders", labelKey: "admin.tabs.orders" },
] as const;

export default function AdminLayout() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const location = useLocation();
    const active = TABS.map((tab) => tab.key).find((key) => location.pathname === key || (key !== "/admin" && location.pathname.startsWith(key))) ?? "/admin";

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6">
                <h1 className="text-2xl font-semibold">{t("admin.title")}</h1>
                <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("admin.description")}</p>
                <Tabs className="mt-4" activeKey={active} onChange={(key) => navigate(key)} items={TABS.map((tab) => ({ key: tab.key, label: t(tab.labelKey) }))} />
                <div className="mt-4">
                    <Outlet />
                </div>
            </div>
        </main>
    );
}
