import { useState } from "react";
import { Button } from "antd";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate } from "react-router-dom";

import { FullScreenLoading } from "@admin/components/full-screen-loading";
import { NoticePage } from "@admin/components/notice-page";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { useAuthStore } from "@/stores/use-auth-store";

// 「已登录但没有后台角色」的说明页。这里刻意不自动登出：账号本身还是合法的主站用户，
// 后台无权把它踢下线；要不要退出只能用户自己点（按钮旁写明会同时退出主站）。
export default function ForbiddenPage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const access = useConsoleAccess();
    const logout = useAuthStore((state) => state.logout);
    const [pending, setPending] = useState(false);

    if (access.phase === "booting") return <FullScreenLoading />;
    if (access.phase === "admin") return <Navigate to="/admin" replace />;
    // 强制改密置位时 /admin/me 返回的也是 403，但那不是「没有后台权限」：送去改密页而不是停留在这里。
    if (access.phase === "must-change-password") return <Navigate to="/admin/change-password" replace />;
    if (access.phase === "signed-out") return <Navigate to="/login" replace />;

    const leave = async () => {
        setPending(true);
        try {
            await logout();
            navigate("/login", { replace: true });
        } finally {
            setPending(false);
        }
    };

    return (
        <NoticePage
            title={t("forbidden.title", { ns: "admin" })}
            description={t("forbidden.description", { ns: "admin", email: access.user.email })}
            action={
                <div>
                    <Button loading={pending} onClick={() => void leave()}>
                        {t("userMenu.logout")}
                    </Button>
                    <p className="mt-2 text-xs leading-5 text-stone-400 dark:text-stone-500">{t("sharedSession.logoutNote", { ns: "admin" })}</p>
                </div>
            }
        />
    );
}
