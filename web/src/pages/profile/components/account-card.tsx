import { Avatar, Button } from "antd";
import dayjs from "dayjs";
import { UserRound } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import type { MeResponse } from "@/services/api/account";

export function AccountCard({ me }: { me: MeResponse }) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const user = me.user;

    return (
        <div className="rounded-xl border border-stone-200 p-5 dark:border-stone-800">
            <div className="flex items-center gap-3">
                <Avatar size={48} src={user.avatarUrl || undefined} icon={<UserRound className="size-5" />} />
                <div className="min-w-0">
                    <p className="truncate font-medium">{user.displayName || user.username}</p>
                    <p className="mt-0.5 truncate text-xs text-stone-500 dark:text-stone-400">{user.email}</p>
                </div>
            </div>
            <div className="mt-4 rounded-lg bg-black/[0.03] px-3 py-2.5 text-sm dark:bg-white/[0.06]">
                <div className="flex items-center justify-between gap-3">
                    <span className="text-stone-500 dark:text-stone-400">{t("profile.account.plan")}</span>
                    <span className="truncate font-medium">{me.plan?.name || me.plan?.id || "—"}</span>
                </div>
                <p className="mt-1 text-xs text-stone-500 dark:text-stone-400">
                    {me.credits?.paidUntil ? t("profile.account.paidUntil", { date: dayjs(me.credits.paidUntil).format("YYYY-MM-DD") }) : t("profile.account.noPaidUntil")}
                </p>
            </div>
            <Button className="mt-4 w-full" type="primary" onClick={() => navigate("/billing")}>
                {t("profile.account.topUp")}
            </Button>
        </div>
    );
}
