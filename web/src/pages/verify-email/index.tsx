import { useEffect, useRef, useState } from "react";
import { App, Button } from "antd";
import { CheckCircle2, CircleAlert, LoaderCircle } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { AnimatedThemeToggler } from "@/components/ui/animated-theme-toggler";
import { getApiErrorMessage } from "@/lib/api-error";
import { sendVerifyEmail, verifyEmail } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";
import { useThemeStore } from "@/stores/use-theme-store";

export default function VerifyEmailPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const theme = useThemeStore((state) => state.theme);
    const setTheme = useThemeStore((state) => state.setTheme);
    const authStatus = useAuthStore((state) => state.status);
    const [searchParams] = useSearchParams();
    const token = searchParams.get("token") || "";
    const [status, setStatus] = useState<"verifying" | "success" | "error">(token ? "verifying" : "error");
    const [failureMessage, setFailureMessage] = useState(() => (token ? "" : t("auth.verify.failureDescription")));
    const [resending, setResending] = useState(false);
    const started = useRef(false);

    // 进页自动提交，成功或失败只执行一次（StrictMode 下 effect 会重复触发）。
    useEffect(() => {
        if (started.current) return;
        started.current = true;
        if (!token) return;
        verifyEmail(token)
            .then(({ user }) => {
                const store = useAuthStore.getState();
                if (store.status === "authenticated" && store.plan && store.accessToken && store.user?.id === user.id) {
                    store.setSession({ user, plan: store.plan, accessToken: store.accessToken });
                }
                setStatus("success");
            })
            .catch((error) => {
                setFailureMessage(getApiErrorMessage(error));
                setStatus("error");
            });
    }, [token]);

    const handleResend = async () => {
        setResending(true);
        try {
            await sendVerifyEmail();
            message.success(t("auth.verify.resent"));
        } catch (error) {
            message.error(getApiErrorMessage(error));
        } finally {
            setResending(false);
        }
    };

    return (
        <div className="relative flex min-h-dvh items-center justify-center overflow-y-auto bg-background bg-[radial-gradient(#e5e7eb_1px,transparent_1px)] px-6 py-10 text-foreground [background-size:16px_16px] dark:bg-[radial-gradient(rgba(245,245,244,0.18)_1px,transparent_1px)]">
            <AnimatedThemeToggler
                theme={theme}
                onThemeChange={setTheme}
                className="absolute right-4 top-4 inline-flex size-8 items-center justify-center rounded-md text-stone-600 transition hover:bg-black/5 hover:text-stone-950 dark:text-stone-300 dark:hover:bg-white/10 dark:hover:text-white [&_svg]:size-4"
            />
            <div className="w-full max-w-sm text-center">
                <Link to="/" className="mb-8 flex items-center justify-center gap-2">
                    <span
                        className="size-5 shrink-0 bg-current"
                        style={{
                            mask: "url(/logo.svg) center / contain no-repeat",
                            WebkitMask: "url(/logo.svg) center / contain no-repeat",
                        }}
                    />
                    <span className="text-base font-medium">{t("meta.title")}</span>
                </Link>

                <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-black/5 dark:bg-white/10">
                    {status === "verifying" ? <LoaderCircle className="size-5 animate-spin" /> : status === "success" ? <CheckCircle2 className="size-5" /> : <CircleAlert className="size-5" />}
                </div>
                <h1 className="mt-5 text-2xl font-semibold">{status === "verifying" ? t("auth.verify.verifying") : status === "success" ? t("auth.verify.success") : t("auth.verify.failure")}</h1>
                {status === "success" ? <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{t("auth.verify.successDescription")}</p> : null}
                {status === "error" ? <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{failureMessage || t("auth.verify.failureDescription")}</p> : null}

                {status === "verifying" ? null : (
                    <div className="mt-8 flex flex-wrap justify-center gap-3">
                        {status === "success" ? (
                            <Button type="primary" onClick={() => navigate("/")}>
                                {t("auth.verify.goHome")}
                            </Button>
                        ) : authStatus === "authenticated" ? (
                            <Button type="primary" loading={resending} onClick={handleResend}>
                                {t("auth.verify.resend")}
                            </Button>
                        ) : (
                            <Button type="primary" onClick={() => navigate("/login")}>
                                {t("auth.verify.goLogin")}
                            </Button>
                        )}
                        <Button type="text" onClick={() => navigate("/login")}>
                            {t("auth.verify.backToLogin")}
                        </Button>
                    </div>
                )}
            </div>
        </div>
    );
}
