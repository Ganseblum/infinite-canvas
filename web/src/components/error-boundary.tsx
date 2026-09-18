import { Home, RotateCcw } from "lucide-react";
import { Button, Result } from "antd";
import { useTranslation } from "react-i18next";
import { isRouteErrorResponse, useNavigate, useRouteError } from "react-router-dom";

// 根路由 errorElement：任意页面渲染抛错时兜底，替代 React Router 默认的英文错误页。
export function RouteErrorBoundary() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const error = useRouteError();
    const detail = error instanceof Error ? error.message : isRouteErrorResponse(error) ? `${error.status} ${error.statusText}` : undefined;

    return (
        <div className="flex h-dvh items-center justify-center overflow-y-auto bg-background px-6 text-foreground">
            <Result
                status="error"
                title={t("errorBoundary.title")}
                subTitle={
                    <span className="inline-flex flex-col items-center gap-2">
                        {t("errorBoundary.description")}
                        {detail ? <span className="max-w-md break-all font-mono text-xs text-stone-500 dark:text-stone-400">{detail}</span> : null}
                    </span>
                }
                extra={[
                    <Button key="home" icon={<Home className="size-4" />} onClick={() => void navigate("/")}>
                        {t("notFound.home")}
                    </Button>,
                    <Button key="retry" type="primary" icon={<RotateCcw className="size-4" />} onClick={() => window.location.reload()}>
                        {t("common.retry")}
                    </Button>,
                ]}
            />
        </div>
    );
}
