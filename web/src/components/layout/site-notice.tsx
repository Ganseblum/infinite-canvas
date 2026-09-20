import { useQuery } from "@tanstack/react-query";
import { Megaphone, Wrench } from "lucide-react";
import { useTranslation } from "react-i18next";

import { getPublicSettings } from "@/services/api/community";

// SiteNotice 展示站点公告与维护提示。两类信息都要常驻可见，不用一次性弹窗。
export function SiteNotice() {
    const { t } = useTranslation();
    const settingsQuery = useQuery({
        queryKey: ["settings", "public"],
        queryFn: ({ signal }) => getPublicSettings(signal),
        // 公告/维护提示 5 分钟内不重拉；但回焦点时刷新，保证运营改动能较快被用户看到。
        staleTime: 5 * 60 * 1000,
        refetchOnWindowFocus: true,
    });
    const settings = settingsQuery.data;
    if (!settings) return null;
    const hasAnnouncement = settings.announcement.trim().length > 0;
    const maintenance = settings.maintenanceMode;
    if (!hasAnnouncement && !maintenance) return null;

    return (
        <div className="flex flex-col">
            {maintenance ? (
                <div className="flex items-start gap-2 border-b border-amber-200/70 bg-amber-50 px-4 py-2 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/40 dark:text-amber-200">
                    <Wrench className="mt-0.5 size-3.5 shrink-0" />
                    <span>{settings.maintenanceNotice || t("siteNotice.maintenance")}</span>
                </div>
            ) : null}
            {hasAnnouncement ? (
                <div className="flex items-start gap-2 border-b border-stone-200/70 bg-black/[0.02] px-4 py-2 text-xs text-stone-600 dark:border-stone-800/70 dark:bg-white/[0.03] dark:text-stone-300">
                    <Megaphone className="mt-0.5 size-3.5 shrink-0" />
                    <span className="whitespace-pre-wrap">{settings.announcement}</span>
                </div>
            ) : null}
        </div>
    );
}
