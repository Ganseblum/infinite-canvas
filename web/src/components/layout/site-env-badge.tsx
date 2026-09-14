import { useTranslation } from "react-i18next";

import { IS_PRODUCTION_SITE, SITE_ENV } from "@/constant/runtime-config";

// 非正式环境在界面上明确标注：测试/预发布站被误当成正式站操作，是这类系统最常见的事故来源。
export function SiteEnvBadge() {
    const { t } = useTranslation();
    if (IS_PRODUCTION_SITE) return null;
    const label = SITE_ENV === "test" ? t("topNav.siteEnvTest") : SITE_ENV === "development" ? t("topNav.siteEnvDev") : SITE_ENV;
    return (
        <span className="shrink-0 rounded-md bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium leading-none text-amber-700 dark:bg-amber-400/15 dark:text-amber-300" title={t("topNav.siteEnvTitle")}>
            {label}
        </span>
    );
}
