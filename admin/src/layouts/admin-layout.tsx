import { Button, Layout, Menu } from "antd";
import type { MenuProps } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { EnvBadge } from "@admin/components/env-badge";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import {
    PERM,
    ADMIN_NAV_PARENTS,
    activeNavKey,
    requiredOpenKeys,
    visibleNavItems,
    type AdminNavCapability,
} from "@admin/lib/admin-nav";
import { CURRENT_PRODUCT_KEY, getAdminProduct } from "@admin/lib/products";
import { listAdminModels } from "@admin/services/api/admin";
import { useAuthStore } from "@/stores/use-auth-store";

// 侧边栏 = 产品切换器：父级菜单是产品（映射见 lib/admin-nav.ts 的 ADMIN_NAV_PARENTS，路由守卫用同一份），
// 已接入产品的子项是按权限过滤后的管理菜单，规划中产品只跳占位页。这里只负责渲染。
// 前端只隐藏入口，服务端才是权限的权威；角色调整后重新聚焦窗口会重新拉 /admin/me 并刷新菜单。
export default function AdminLayout() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const location = useLocation();
    const email = useAuthStore((state) => state.user?.email ?? "");
    const logout = useAuthStore((state) => state.logout);
    const access = useConsoleAccess();

    const currentProduct = getAdminProduct(CURRENT_PRODUCT_KEY)!;

    const visibleItems = access.phase === "admin" ? visibleNavItems(access.permissions) : [];

    // 「模型」子菜单各能力的数量：与模型页共用同一份 ["admin", "models"] 缓存，模型增删后
    // 页面 invalidate 会连带刷新这里的计数；没有 models.read 就不显示子菜单、也不发这个查询。
    const canReadModels = access.phase === "admin" && access.permissions.includes(PERM.modelsRead);
    const modelsQuery = useQuery({ queryKey: ["admin", "models"], queryFn: ({ signal }) => listAdminModels(signal), enabled: canReadModels });
    const modelCounts = useMemo<Record<AdminNavCapability, number> | null>(() => {
        const items = modelsQuery.data?.items;
        if (!items) return null;
        const counts: Record<AdminNavCapability, number> = { image: 0, video: 0, audio: 0, text: 0 };
        for (const item of items) if (item.capability in counts) counts[item.capability as AdminNavCapability] += 1;
        return counts;
    }, [modelsQuery.data]);

    // 父级 = 产品：已接入产品是 SubMenu（子项 = 管理菜单，「模型」下再挂能力子菜单）；
    // 规划中产品是普通菜单项，带「规划中」小标签，点击进占位页。
    const menuItems: MenuProps["items"] = ADMIN_NAV_PARENTS.map((parent) => {
        const product = getAdminProduct(parent.key);
        if (!product) return null;
        if (parent.children.length === 0) {
            return {
                key: `/admin/product/${product.key}`,
                label: (
                    <span className="flex items-center gap-2">
                        <span className="truncate">{t(product.nameKey, { ns: "admin" })}</span>
                        <span className="shrink-0 text-[11px] font-normal text-stone-400 dark:text-stone-500">{t("products.planned", { ns: "admin" })}</span>
                    </span>
                ),
            };
        }
        return {
            key: product.key,
            label: t(product.nameKey, { ns: "admin" }),
            children: visibleItems.map((item) => {
                if (item.capabilityChildren?.length) {
                    return {
                        key: item.path,
                        label: t(item.labelKey),
                        children: item.capabilityChildren.map((child) => {
                            const label = t(child.labelKey);
                            const count = modelCounts?.[child.capability];
                            return { key: child.path, label: count === undefined ? label : t("admin.models.filters.labeled", { label, count }) };
                        }),
                    };
                }
                return { key: item.path, label: t(item.labelKey) };
            }),
        };
    });

    const selected = access.phase === "admin" ? activeNavKey(location.pathname, location.search) : null;
    // 停在模型页时两级 SubMenu 强制展开（选中的能力子项必须可见），其余页面展开状态归用户控制。
    const [openKeysState, setOpenKeysState] = useState<string[]>([CURRENT_PRODUCT_KEY]);
    const requiredOpen = requiredOpenKeys(location.pathname);
    const openKeys = requiredOpen.length > 0 ? [...new Set([...openKeysState, ...requiredOpen])] : openKeysState;

    return (
        <Layout className="h-dvh">
            <Layout.Sider theme="light" width={224}>
                <div className="flex h-dvh flex-col">
                    {/* 环境标识放在侧边栏头部而不是内容区标题旁：内容区会随滚动移出视口，环境角标必须常驻。 */}
                    <div className="flex items-center gap-2 px-5 py-5 text-sm font-semibold">
                        <span className="truncate">{t(currentProduct.nameKey, { ns: "admin" })}</span>
                        <EnvBadge />
                    </div>
                    <Menu
                        className="flex-1 overflow-y-auto"
                        mode="inline"
                        selectedKeys={selected ? [selected] : []}
                        openKeys={openKeys}
                        onOpenChange={setOpenKeysState}
                        items={menuItems}
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
