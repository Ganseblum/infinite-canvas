import { Alert, Button } from "antd";
import { useTranslation } from "react-i18next";
import { ApiError, getApiErrorMessage } from "@/lib/api-error";

// 查询失败的统一展示：403 是「没有权限」而不是故障，不给重试入口——重复请求同一个被拒的接口
// 没有意义，服务端才是权威。其他错误保持原有的失败文案 + 重试。
export function QueryError({ error, message, onRetry, className }: { error: unknown; message: string; onRetry?: () => void; className?: string }) {
    const { t } = useTranslation();

    if (error instanceof ApiError && error.status === 403) {
        return (
            <Alert
                className={className}
                type="warning"
                showIcon
                message={t("permissionDenied.title", { ns: "admin" })}
                description={t("permissionDenied.description", { ns: "admin" })}
            />
        );
    }

    return (
        <Alert
            className={className}
            type="error"
            showIcon
            message={message}
            description={getApiErrorMessage(error)}
            action={onRetry ? <Button size="small" onClick={onRetry}>{t("admin.retry")}</Button> : undefined}
        />
    );
}
