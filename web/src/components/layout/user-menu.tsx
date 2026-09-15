import { App, Avatar, Badge, Dropdown } from "antd";
import type { MenuProps } from "antd";
import { CreditCard, LogOut, MailWarning, Settings2, UserRound } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { sendVerifyEmail } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";

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
        else if (key === "admin") navigate("/admin");
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
        ...(user.role === "admin" ? [{ key: "admin", icon: <Settings2 className="size-4" />, label: t("userMenu.admin") }] : []),
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
