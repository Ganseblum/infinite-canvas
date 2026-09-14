import { Alert, Button, Space } from "antd";
import dayjs, { type Dayjs } from "dayjs";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import type { MeResponse } from "@/services/api/account";

function formatExpiryTime(target: Dayjs, t: TFunction): string {
    const minutes = Math.max(1, target.diff(dayjs(), "minute"));
    if (minutes >= 48 * 60) return t("profile.usage.expiryDays", { count: Math.ceil(minutes / (24 * 60)) });
    if (minutes >= 60) return t("profile.usage.expiryHours", { count: Math.ceil(minutes / 60) });
    return t("profile.usage.expiryMinutes", { count: minutes });
}

export function StatusAlerts({ me }: { me: MeResponse }) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const expiryAt = me.mediaExpiry?.nearestAt ?? null;
    const expiryDate = expiryAt ? dayjs(expiryAt) : null;
    const expiresSoon = !!expiryDate && (me.mediaExpiry?.expiringCount ?? 0) > 0 && expiryDate.isAfter(dayjs()) && expiryDate.diff(dayjs(), "minute") <= 2 * 24 * 60;

    if (!me.readOnly && !expiresSoon) return null;

    return (
        <div className="mt-6 flex flex-col gap-3">
            {me.readOnly ? (
                <Alert
                    type="error"
                    showIcon
                    message={t("profile.usage.readOnlyTitle")}
                    description={
                        <span>
                            {t("profile.usage.readOnlyHint")}
                            {me.graceEndsAt ? ` ${t("profile.usage.graceEndsAt", { date: dayjs(me.graceEndsAt).format("YYYY-MM-DD HH:mm") })}` : ""}
                        </span>
                    }
                    action={
                        <Space wrap>
                            <Button size="small" type="primary" onClick={() => navigate("/billing")}>
                                {t("profile.usage.upgrade")}
                            </Button>
                            <Button size="small" onClick={() => navigate("/canvas")}>
                                {t("profile.usage.export")}
                            </Button>
                        </Space>
                    }
                />
            ) : null}
            {expiresSoon && expiryDate ? (
                <Alert
                    type="warning"
                    showIcon
                    message={t("profile.usage.expiryAlert", { count: me.mediaExpiry?.expiringCount ?? 0, time: formatExpiryTime(expiryDate, t) })}
                    description={t("profile.usage.expiryHint")}
                    action={
                        <Button size="small" onClick={() => navigate("/canvas")}>
                            {t("profile.usage.export")}
                        </Button>
                    }
                />
            ) : null}
        </div>
    );
}
