import { Button, Layout, Menu } from "antd";
import { useTranslation } from "react-i18next";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { visibleNavItems } from "@admin/lib/admin-nav";
import { useAuthStore } from "@/stores/use-auth-store";

// 侧边栏的菜单项与权限映射统一放在 lib/admin-nav.ts（路由守卫用同一份），这里只负责渲染。
// 前端只隐藏入口，服务端才是权限的权威；角色调整后重新聚焦窗口会重新拉 /admin/me 并刷新菜单。
export default function AdminLayout() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const location = useLocation();
    const email = useAuthStore((state) => state.user?.email ?? "");
    const logout = useAuthStore((state) => state.logout);
    const access = useConsoleAccess();

    const menuItems = access.phase === "admin" ? visibleNavItems(access.permissions) : [];
    const active = menuItems.map((item) => item.path).find((path) => location.pathname === path || (path !== "/admin" && location.pathname.startsWith(path))) ?? "/admin";

    return (
        <Layout className="h-dvh">
            <Layout.Sider theme="light" width={224}>
                <div className="flex h-dvh flex-col">
                    <div className="px-5 py-5 text-sm font-semibold">{t("admin.title")}</div>
                    <Menu
                        className="flex-1 overflow-y-auto"
                        mode="inline"
                        selectedKeys={[active]}
                        items={menuItems.map((item) => ({ key: item.path, label: t(item.labelKey) }))}
                        onClick={({ key }) => navigate(key)}
                    />
                    <div className="px-4 py-3">
                        <span className="block truncate text-xs text-stone-500 dark:text-stone-400">{email}</span>
                        {access.phase === "admin" ? (
                            <span className="block truncate text-[11px] text-stone-400 dark:text-stone-500">{t("currentRole", { ns: "admin", role: access.role.name })}</span>
                        ) : null}
                        {/* 登录态与主站共用，退出会影响主站，必须在按钮旁写明，不能只靠管理员自己知道。 */}
                        <p className="mt-1 text-[11px] leading-4 text-stone-400 dark:text-stone-500">{t("sharedSession.logoutNote", { ns: "admin" })}</p>
                        <Button className="mt-1" type="text" size="small" onClick={() => void logout()}>
                            {t("userMenu.logout")}
                        </Button>
                    </div>
                </div>
            </Layout.Sider>
            <Layout.Content className="overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
                <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6">
                    <h1 className="text-2xl font-semibold">{t("admin.title")}</h1>
                    <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("admin.description")}</p>
                    <div className="mt-6">
                        <Outlet />
                    </div>
                </div>
            </Layout.Content>
        </Layout>
    );
}
