import { useEffect, useRef, useState } from "react";
import { Alert, Button, Spin } from "antd";
import { useTranslation } from "react-i18next";
import { Navigate, useLocation, useSearchParams } from "react-router-dom";

import { getApiErrorMessage } from "@/lib/api-error";
import { apiRequest } from "@/services/api/client";
import { useAuthStore } from "@/stores/use-auth-store";

// OIDC 浏览器直跳承接页：第三方产品把用户浏览器重定向到这里，登录态下代用户
// 调 authorize（JSON 模式）换取授权码并回跳第三方；未登录先去登录页，登录后
// 经 state.from 回到本页继续完成授权。
export default function OAuthAuthorizePage() {
    const { t } = useTranslation();
    const status = useAuthStore((state) => state.status);
    const location = useLocation();
    const [searchParams] = useSearchParams();
    const [error, setError] = useState<string | null>(null);
    const startedRef = useRef(false);

    useEffect(() => {
        if (status !== "authenticated" || startedRef.current) return;
        startedRef.current = true;
        void (async () => {
            try {
                const result = await apiRequest<{ redirect: string }>("/oidc/authorize", {
                    query: Object.fromEntries(searchParams.entries()),
                    headers: { Accept: "application/json" },
                });
                window.location.replace(result.redirect);
            } catch (err) {
                setError(getApiErrorMessage(err));
            }
        })();
    }, [status, searchParams]);

    if (status === "booting") {
        return (
            <div className="flex h-dvh items-center justify-center bg-background text-foreground">
                <Spin size="large" />
            </div>
        );
    }
    if (status === "unauthenticated") {
        return <Navigate to="/login" replace state={{ from: `${location.pathname}${location.search}` }} />;
    }
    if (error) {
        return (
            <div className="mx-auto flex h-dvh max-w-md flex-col items-center justify-center gap-4 bg-background px-6 text-foreground">
                <Alert type="error" showIcon message={t("oauth.authorizeFailed")} description={error} />
                <Button onClick={() => window.location.reload()}>{t("oauth.authorizeRetry")}</Button>
            </div>
        );
    }
    return (
        <div className="flex h-dvh flex-col items-center justify-center gap-3 bg-background text-foreground">
            <Spin size="large" />
            <p className="text-sm text-stone-500 dark:text-stone-400">{t("oauth.authorizePending")}</p>
        </div>
    );
}
