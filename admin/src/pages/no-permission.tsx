import { Button } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { firstAccessiblePath } from "@admin/lib/admin-nav";

// 已进后台、但当前角色没有这个页面所需权限时的落点（页面级守卫重定向到这里）。
// 刻意只放在 AdminLayout 内部：侧边栏还在，用户可以自己走去有权限的页面，不需要退出登录。
export default function NoPermissionPage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const access = useConsoleAccess();
    const fallback = access.phase === "admin" ? firstAccessiblePath(access.permissions) : null;

    return (
        <div className="rounded-xl border border-stone-200 p-6 dark:border-stone-800">
            <h2 className="text-base font-semibold">{t("noPermission.title", { ns: "admin" })}</h2>
            <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">
                {access.phase === "admin" && access.role.name
                    ? t("noPermission.description", { ns: "admin", role: access.role.name })
                    : t("noPermission.descriptionNoRole", { ns: "admin" })}
            </p>
            {fallback ? (
                <Button className="mt-4" type="primary" onClick={() => navigate(fallback)}>
                    {t("noPermission.back", { ns: "admin" })}
                </Button>
            ) : null}
        </div>
    );
}
