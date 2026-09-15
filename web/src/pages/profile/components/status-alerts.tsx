import { Alert, Button, Space } from "antd";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { formatMediaExpiryTime, mediaExpiresSoon } from "@/hooks/use-media-expiry";
import type { MeResponse } from "@/services/api/account";

export function StatusAlerts({ me }: { me: MeResponse }) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const { soon: expiresSoon, expiryDate, count } = mediaExpiresSoon(me);

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
                    message={t("profile.usage.expiryAlert", { count, time: formatMediaExpiryTime(expiryDate, t) })}
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
