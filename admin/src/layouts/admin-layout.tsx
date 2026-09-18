import { Button, Dropdown, Layout, Menu } from "antd";
import type { MenuProps } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { EnvBadge } from "@admin/components/env-badge";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { visibleNavItems } from "@admin/lib/admin-nav";
import { ADMIN_PRODUCTS, CURRENT_PRODUCT_KEY, getAdminProduct } from "@admin/lib/products";
import { fetchAdminMeta } from "@admin/services/api/admin";
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

    // 产品切换器的版本号来自 /admin/meta：免权限点，失败（比如旧版后端还没上线）只是不显示版本，不影响切换。
    const metaQuery = useQuery({ queryKey: ["admin", "meta"], queryFn: ({ signal }) => fetchAdminMeta(signal) });
    const currentProduct = getAdminProduct(CURRENT_PRODUCT_KEY)!;
    const version = metaQuery.data?.product.version;
    // 停在规划中产品的占位页时，点回当前产品要离开占位路由，回到后台首页。
    const onProductPlaceholder = location.pathname.startsWith("/admin/product/");

    const productMenuItems: MenuProps["items"] = ADMIN_PRODUCTS.map((product) => ({
        key: product.key,
        label: (
            <span className="flex items-center justify-between gap-3">
                <span className="truncate">{t(product.nameKey, { ns: "admin" })}</span>
                {product.status === "connected" ? (
                    version ? <span className="shrink-0 text-xs font-normal text-stone-400 dark:text-stone-500">v{version}</span> : null
                ) : (
                    <span className="shrink-0 text-[11px] font-normal text-stone-400 dark:text-stone-500">{t("products.planned", { ns: "admin" })}</span>
                )}
            </span>
        ),
    }));

    const menuItems = access.phase === "admin" ? visibleNavItems(access.permissions) : [];
    const active = menuItems.map((item) => item.path).find((path) => location.pathname === path || (path !== "/admin" && location.pathname.startsWith(path))) ?? "/admin";

    return (
        <Layout className="h-dvh">
            <Layout.Sider theme="light" width={224}>
                <div className="flex h-dvh flex-col">
                    {/* 环境标识放在侧边栏头部而不是内容区标题旁：内容区会随滚动移出视口，环境角标必须常驻。 */}
                    <div className="flex items-center gap-2 px-5 py-5 text-sm font-semibold">
                        <Dropdown
                            trigger={["click"]}
                            placement="bottomLeft"
                            menu={{
                                items: productMenuItems,
                                // 当前产品在菜单里高亮；规划中产品只跳占位页，不给任何管理菜单。
                                selectedKeys: [CURRENT_PRODUCT_KEY],
                                onClick: ({ key }) => {
                                    if (key === CURRENT_PRODUCT_KEY) {
                                        if (onProductPlaceholder) navigate("/admin");
                                        return;
                                    }
                                    navigate(`/admin/product/${key}`);
                                },
                            }}
                        >
                            {/* 扁平样式：透明背景、仅 hover 轻微反馈，靠名称 + 箭头表达可切换。 */}
                            <button
                                type="button"
                                className="flex min-w-0 items-center gap-1 rounded-md py-0.5 pr-1 hover:bg-black/5 dark:hover:bg-white/10"
                            >
                                <span className="truncate">{t(currentProduct.nameKey, { ns: "admin" })}</span>
                                <span aria-hidden className="shrink-0 text-[10px] font-normal text-stone-400 dark:text-stone-500">
                                    ▾
                                </span>
                            </button>
                        </Dropdown>
                        <EnvBadge />
                    </div>
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
