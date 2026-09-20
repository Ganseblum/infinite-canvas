import { Alert, Button } from "antd";
import dayjs, { type Dayjs } from "dayjs";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { useMeQuery } from "@/pages/profile/use-me";
import type { MeResponse } from "@/services/api/account";

/** 把到期时间格式化成相对文案：2 天以上按天、1 小时以上按小时、其余按分钟。 */
export function formatMediaExpiryTime(target: Dayjs, t: TFunction): string {
    const minutes = Math.max(1, target.diff(dayjs(), "minute"));
    if (minutes >= 48 * 60) return t("profile.usage.expiryDays", { count: Math.ceil(minutes / (24 * 60)) });
    if (minutes >= 60) return t("profile.usage.expiryHours", { count: Math.ceil(minutes / 60) });
    return t("profile.usage.expiryMinutes", { count: minutes });
}

// 判断 /api/me 里的 mediaExpiry 是否落在未来 2 天内。个人中心与画布列表共用。
export function mediaExpiresSoon(me: MeResponse | undefined): { soon: boolean; expiryDate: Dayjs | null; count: number } {
    const expiryAt = me?.mediaExpiry?.nearestAt ?? null;
    const expiryDate = expiryAt ? dayjs(expiryAt) : null;
    const count = me?.mediaExpiry?.expiringCount ?? 0;
    const soon = !!expiryDate && count > 0 && expiryDate.isAfter(dayjs()) && expiryDate.diff(dayjs(), "minute") <= 2 * 24 * 60;
    return { soon, expiryDate, count };
}

// use-media-expiry 是跨页面复用的媒体到期查询，倒计时逻辑只写一份。
export function useMediaExpiry() {
    return useMeQuery();
}

// MediaExpiryAlert 给画布列表等页面复用的常驻到期预警条。
export function MediaExpiryAlert() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const meQuery = useMeQuery();
    const { soon, expiryDate, count } = mediaExpiresSoon(meQuery.data);

    if (!soon || !expiryDate) return null;

    return (
        <Alert
            type="warning"
            showIcon
            message={t("profile.usage.expiryAlert", { count, time: formatMediaExpiryTime(expiryDate, t) })}
            description={t("profile.usage.expiryHint")}
            action={
                <Button size="small" onClick={() => navigate("/canvas")}>
                    {t("profile.usage.export")}
                </Button>
            }
        />
    );
}
