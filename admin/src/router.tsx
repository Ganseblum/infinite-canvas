import { Suspense, lazy, useEffect } from "react";
import { createBrowserRouter, Navigate, Outlet } from "react-router-dom";

import { FullScreenLoading } from "@admin/components/full-screen-loading";
import { RequireAdmin } from "@admin/components/require-admin";
import { RequirePermission } from "@admin/components/require-permission";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import AdminLayout from "@admin/layouts/admin-layout";
import { PERM, firstAccessiblePath, permissionsForPath } from "@admin/lib/admin-nav";
import AdminChannelsPage from "@admin/pages/channels";
import ChangePasswordPage from "@admin/pages/change-password";
import AdminPackagesPage from "@admin/pages/credit-packages";
import ForbiddenPage from "@admin/pages/forbidden";
import AdminDashboardPage from "@admin/pages/index";
import AdminLoginPage from "@admin/pages/login";
import AdminMembershipPage from "@admin/pages/membership";
import AdminModelsPage from "@admin/pages/models";
import AdminModerationPage from "@admin/pages/moderation";
import NoPermissionPage from "@admin/pages/no-permission";
import AdminOrdersPage from "@admin/pages/orders";
import AdminProductPlaceholderPage from "@admin/pages/product";
import AdminSystemPage from "@admin/pages/system";
import AdminUsersPage from "@admin/pages/users";
import { useAuthStore } from "@/stores/use-auth-store";

// 用量分析页独占 echarts：懒加载把这块体积隔离在该路由的分包里，其余页面不支付这份成本。
const AdminAnalyticsPage = lazy(() => import("@admin/pages/analytics"));

// 应用挂载后只执行一次 bootstrap；store 内部有模块级单飞守卫。
function RootBootstrap() {
    const bootstrap = useAuthStore((state) => state.bootstrap);

    useEffect(() => {
        void bootstrap();
    }, [bootstrap]);

    return <Outlet />;
}

// /admin 是总览页。没有 stats.read 的角色不该在这里吃 403：直接送去它第一个有权限的页面；
// 一个权限都没有（空角色）时落到「无权限」页说明原因。
function AdminHomeRoute() {
    const access = useConsoleAccess();

    if (access.phase === "booting") return <FullScreenLoading />;
    if (access.phase !== "admin") return <Navigate to="/forbidden" replace />;
    if (access.permissions.includes(PERM.statsRead)) return <AdminDashboardPage />;
    const target = firstAccessiblePath(access.permissions);
    return target && target !== "/admin" ? <Navigate to={target} replace /> : <NoPermissionPage />;
}

// 路由路径与拆分前保持一致（/admin/...）：切换批次删 web 路由时不必再动链接，书签也能直接用。
// 页面级权限从 lib/admin-nav.ts 的同一份菜单映射里取，直输无权限的路径会落到 /admin/no-permission。
export const router = createBrowserRouter([
    {
        element: <RootBootstrap />,
        children: [
            // 登录与「无后台权限」是 admin 域自己的页面：未登录落到 /login，已登录但没有后台角色落到 /forbidden。
            { path: "/login", element: <AdminLoginPage /> },
            { path: "/forbidden", element: <ForbiddenPage /> },
            // 强制改密页在 RequireAdmin 外壳之外：闸门放下时后台接口全部 403，
            // 不能渲染侧边栏外壳，用户在这里只有改密与退出两条出路（RequireAdmin 会把 /admin/* 都送过来）。
            { path: "/admin/change-password", element: <ChangePasswordPage /> },
            {
                element: (
                    <RequireAdmin>
                        <AdminLayout />
                    </RequireAdmin>
                ),
                children: [
                    { path: "/admin", element: <AdminHomeRoute /> },
                    {
                        path: "/admin/analytics",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/analytics")}>
                                <Suspense fallback={<FullScreenLoading />}>
                                    <AdminAnalyticsPage />
                                </Suspense>
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/users",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/users")}>
                                <AdminUsersPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/membership",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/membership")}>
                                <AdminMembershipPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/models",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/models")}>
                                <AdminModelsPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/channels",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/channels")}>
                                <AdminChannelsPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/moderation",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/moderation")}>
                                <AdminModerationPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/system",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/system")}>
                                <AdminSystemPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/credit-packages",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/credit-packages")}>
                                <AdminPackagesPage />
                            </RequirePermission>
                        ),
                    },
                    {
                        path: "/admin/orders",
                        element: (
                            <RequirePermission required={permissionsForPath("/admin/orders")}>
                                <AdminOrdersPage />
                            </RequirePermission>
                        ),
                    },
                    // 无权限说明页本身不能要权限，否则会自我重定向成死循环。
                    { path: "/admin/no-permission", element: <NoPermissionPage /> },
                    // 规划中产品的占位页同样不挂权限点：页面自己校验 key，未登记或已接入的产品直接回 /admin。
                    { path: "/admin/product/:key", element: <AdminProductPlaceholderPage /> },
                ],
            },
            // admin 是独立应用：根路径与未知路径都交给总览页，守卫先处理登录态。
            { path: "*", element: <Navigate to="/admin" replace /> },
        ],
    },
]);
