import { useTranslation } from "react-i18next";

import { IS_PRODUCTION_SITE, SITE_ENV } from "@/constant/runtime-config";

// 环境标识（管理后台版）：正式环境恒显醒目的红色角标——后台操作大多不可逆，管理员要始终看得见
// 自己站在正式环境；测试/开发沿用主站顶栏的黄色小标签样式（web 的 site-env-badge 是正式环境不显示，
// 后台这里正式环境反而要更显眼，所以不复用）。
export function EnvBadge() {
    const { t } = useTranslation();
    if (IS_PRODUCTION_SITE) {
        return (
            <span
                className="shrink-0 rounded-md bg-red-600 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white"
                title={t("envBadge.productionTitle", { ns: "admin" })}
            >
                {t("envBadge.production", { ns: "admin" })}
            </span>
        );
    }
    const label = SITE_ENV === "test" ? t("envBadge.test", { ns: "admin" }) : SITE_ENV === "development" ? t("envBadge.dev", { ns: "admin" }) : SITE_ENV;
    return (
        <span
            className="shrink-0 rounded-md bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium leading-none text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
            title={t("envBadge.nonProductionTitle", { ns: "admin" })}
        >
            {label}
        </span>
    );
}
