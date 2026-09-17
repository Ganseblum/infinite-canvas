import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";

import { FullScreenLoading } from "@admin/components/full-screen-loading";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { hasAnyPermission } from "@admin/lib/admin-nav";

// 页面级守卫：直输无权限的路径时落到 /admin/no-permission，不重试也不渲染空白页。
// 外层 RequireAdmin 已经处理登录态与「有没有后台角色」，这里只判断权限点。
// 前端只负责不给入口，服务端的 RequirePermission 中间件才是权威。
export function RequirePermission({ required, children }: { required: string[]; children: ReactNode }) {
    const access = useConsoleAccess();

    if (access.phase === "booting") return <FullScreenLoading />;
    // 非 admin 相位理论上到不了这里（外层守卫已拦）；兜底跳说明页，避免渲染出半截页面。
    if (access.phase !== "admin") return <Navigate to="/forbidden" replace />;
    if (!hasAnyPermission(access.permissions, required)) return <Navigate to="/admin/no-permission" replace />;
    return <>{children}</>;
}
