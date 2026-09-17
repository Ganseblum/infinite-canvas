import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";

import { FullScreenLoading } from "@admin/components/full-screen-loading";
import { useConsoleAccess } from "@admin/hooks/use-console-access";

// 只放行管理员。身份判定统一走 useConsoleAccess()，这里只负责四种落点：
//   未登录 → /login（admin 域自己的登录页，不再引导用户回主站登录）
//   强制改密置位 → /admin/change-password（闸门页；此时除改密/登出/刷新外全部接口 403，后台外壳不能渲染）
//   已登录但无后台角色 → /forbidden（说明页；不代用户登出，他还是合法的主站用户）
//   管理员 → 放行
// 与 web 的 RequireAdmin 不同，这里不能跳 "/"：admin 域没有首页，跳过去只会再回到本守卫。
export function RequireAdmin({ children }: { children: ReactNode }) {
    const access = useConsoleAccess();

    if (access.phase === "booting") return <FullScreenLoading />;
    if (access.phase === "signed-out") return <Navigate to="/login" replace />;
    if (access.phase === "must-change-password") return <Navigate to="/admin/change-password" replace />;
    if (access.phase === "no-console-access") return <Navigate to="/forbidden" replace />;
    return <>{children}</>;
}
