import { App, Avatar, Badge, Dropdown } from "antd";
import type { MenuProps } from "antd";
import { CreditCard, LogOut, MailWarning, Settings2, UserRound } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { ADMIN_BASE_URL } from "@/constant/runtime-config";
import { sendVerifyEmail } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";

// 管理后台是独立域名，入口必须是绝对地址；运行期未配置（该环境没有独立后台）时整个入口隐藏。
// 可见性仍按 user.role 判断：它是 role_key 的保守投影，只有系统角色会投影成 admin，
// 自定义角色的管理员不会在主站看到入口（宁可少给入口，也不多给）。
const adminConsoleUrl = ADMIN_BASE_URL.replace(/\/+$/, "");

export function UserMenu() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const status = useAuthStore((state) => state.status);
    const user = useAuthStore((state) => state.user);
    const logout = useAuthStore((state) => state.logout);

    if (status === "booting") return null;

    if (status !== "authenticated" || !user) {
        return (
            <Link to="/login" className="inline-flex h-8 shrink-0 items-center rounded-md px-3 text-sm font-medium text-stone-700 transition hover:bg-black/5 hover:text-stone-950 dark:text-stone-200 dark:hover:bg-white/10 dark:hover:text-white">
                {t("userMenu.login")}
            </Link>
        );
    }

    const displayName = user.displayName || user.username;
    const initial = (displayName || user.email).slice(0, 1).toUpperCase();

    const handleMenuClick = async (key: string) => {
        if (key === "profile") navigate("/profile");
        else if (key === "billing") navigate("/billing");
        else if (key === "logout") await logout();
        else if (key === "verify") {
            try {
                await sendVerifyEmail();
                message.success(t("auth.verify.resent"));
            } catch (error) {
                message.error(getApiErrorMessage(error));
            }
        }
    };

    const items: MenuProps["items"] = [
        {
            key: "account",
            disabled: true,
            label: (
                <div className="min-w-40 py-0.5">
                    <div className="truncate text-sm font-medium text-stone-900 dark:text-stone-100">{displayName}</div>
                    <div className="truncate text-xs text-stone-500 dark:text-stone-400">{user.email}</div>
                </div>
            ),
        },
        { type: "divider" },
        ...(user.emailVerified
            ? []
            : [
                  {
                      key: "verify",
                      icon: <MailWarning className="size-4" />,
                      label: `${t("userMenu.emailUnverified")} · ${t("userMenu.resendVerify")}`,
                  },
              ]),
        { key: "profile", icon: <UserRound className="size-4" />, label: t("userMenu.profile") },
        { key: "billing", icon: <CreditCard className="size-4" />, label: t("userMenu.billing") },
        ...(user.role === "admin" && adminConsoleUrl
            ? [
                  {
                      key: "admin",
                      icon: <Settings2 className="size-4" />,
                      // 用 a 标签而不是 navigate：跨域跳转不该走 SPA 路由，也保留「在新标签打开 / 复制链接」。
                      label: (
                          <a href={adminConsoleUrl} className="text-inherit">
                              {t("userMenu.admin")}
                          </a>
                      ),
                  },
              ]
            : []),
        { type: "divider" },
        { key: "logout", icon: <LogOut className="size-4" />, label: t("userMenu.logout") },
    ];

    return (
        <Dropdown trigger={["click"]} placement="bottomRight" menu={{ items, onClick: ({ key }) => void handleMenuClick(key) }}>
            <button type="button" className="flex h-8 shrink-0 items-center gap-2 rounded-full pl-0.5 pr-2 transition hover:bg-black/5 dark:hover:bg-white/10" aria-label={t("userMenu.open")}>
                <Badge dot={!user.emailVerified} offset={[-2, 2]}>
                    <Avatar size={28} src={user.avatarUrl || undefined} className="bg-stone-200 text-xs font-medium text-stone-700 dark:bg-stone-700 dark:text-stone-200">
                        {initial}
                    </Avatar>
                </Badge>
                <span className="hidden max-w-24 truncate text-sm text-stone-700 sm:inline dark:text-stone-200">{displayName}</span>
            </button>
        </Dropdown>
    );
}
