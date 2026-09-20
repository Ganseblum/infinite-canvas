import { useState } from "react";
import { App, Button, Form, Input } from "antd";
import type { FormInstance } from "antd";
import { CheckCircle2, CircleAlert } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { AnimatedThemeToggler } from "@/components/ui/animated-theme-toggler";
import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import { isPasswordByteLengthValid } from "@/lib/password";
import { resetPassword } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";
import { useThemeStore } from "@/stores/use-theme-store";

/** 重置密码表单取值。 */
type ResetValues = { password: string; confirmPassword: string };

/** 把服务端返回的字段级校验错误铺回表单对应输入框。
 * @param form 目标表单实例
 * @param fields 字段名到错误文案的映射
 */
function applyFieldErrors(form: FormInstance, fields?: Record<string, string>) {
    if (!fields) return;
    form.setFields(Object.entries(fields).map(([name, message]) => ({ name, errors: [message] })));
}

/** 重置密码页入口：携带邮件链接里的 token，分「表单 / 成功 / 失败」三种视图；
 * 无 token 或 token 失效直接进入失败视图并提供重新申请入口。
 */
export default function ResetPasswordPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const theme = useThemeStore((state) => state.theme);
    const setTheme = useThemeStore((state) => state.setTheme);
    const clearSession = useAuthStore((state) => state.clearSession);
    const [searchParams] = useSearchParams();
    const token = searchParams.get("token") || "";
    // 初始 phase 取决于链接里有没有 token：缺 token 直接进入失败视图。
    const [phase, setPhase] = useState<"form" | "success" | "error">(token ? "form" : "error");
    const [failureMessage, setFailureMessage] = useState(() => (token ? t("auth.reset.invalidToken") : t("auth.reset.missingToken")));
    const [submitting, setSubmitting] = useState(false);
    const [form] = Form.useForm<ResetValues>();

    const handleSubmit = async (values: ResetValues) => {
        setSubmitting(true);
        try {
            await resetPassword(token, values.password);
            // 重置成功后服务端撤销该用户全部 refresh token，本地登录态一并清空。
            clearSession();
            setPhase("success");
        } catch (error) {
            if (error instanceof ApiError && error.code === "VALIDATION_FAILED" && error.fields) {
                applyFieldErrors(form, error.fields);
            } else if (error instanceof ApiError && error.code === "TOKEN_INVALID") {
                setFailureMessage(getApiErrorMessage(error));
                setPhase("error");
            } else {
                message.error(getApiErrorMessage(error));
            }
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <div className="relative flex min-h-dvh items-center justify-center overflow-y-auto bg-background bg-[radial-gradient(#e5e7eb_1px,transparent_1px)] px-6 py-10 text-foreground [background-size:16px_16px] dark:bg-[radial-gradient(rgba(245,245,244,0.18)_1px,transparent_1px)]">
            <AnimatedThemeToggler
                theme={theme}
                onThemeChange={setTheme}
                className="absolute right-4 top-4 inline-flex size-8 items-center justify-center rounded-md text-stone-600 transition hover:bg-black/5 hover:text-stone-950 dark:text-stone-300 dark:hover:bg-white/10 dark:hover:text-white [&_svg]:size-4"
            />
            <div className="w-full max-w-sm">
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

                {phase === "success" ? (
                    <div className="text-center">
                        <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-black/5 dark:bg-white/10">
                            <CheckCircle2 className="size-5" />
                        </div>
                        <h1 className="mt-5 text-2xl font-semibold">{t("auth.reset.success")}</h1>
                        <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{t("auth.reset.successDescription")}</p>
                        <Button className="mt-8" type="primary" onClick={() => navigate("/login")}>
                            {t("auth.reset.goLogin")}
                        </Button>
                    </div>
                ) : phase === "error" ? (
                    <div className="text-center">
                        <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-black/5 dark:bg-white/10">
                            <CircleAlert className="size-5" />
                        </div>
                        <h1 className="mt-5 text-2xl font-semibold">{t("auth.reset.title")}</h1>
                        <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{failureMessage}</p>
                        <div className="mt-8 flex flex-wrap justify-center gap-3">
                            <Button type="primary" onClick={() => navigate("/login", { state: { forgot: true } })}>
                                {t("auth.reset.requestAgain")}
                            </Button>
                            <Button type="text" onClick={() => navigate("/login")}>
                                {t("auth.reset.goLogin")}
                            </Button>
                        </div>
                    </div>
                ) : (
                    <div>
                        <h1 className="text-2xl font-semibold">{t("auth.reset.title")}</h1>
                        <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{t("auth.reset.description")}</p>
                        <Form form={form} layout="vertical" requiredMark={false} className="mt-6" onFinish={handleSubmit}>
                            <Form.Item
                                name="password"
                                label={t("auth.reset.newPassword")}
                                rules={[
                                    { required: true, message: t("auth.validation.passwordRequired") },
                                    // 与服务端一致按字节长度校验，避免多字节密码提交后才报错。
                                    { validator: (_, value: string) => (!value || isPasswordByteLengthValid(value) ? Promise.resolve() : Promise.reject(new Error(t("auth.validation.password")))) },
                                ]}
                            >
                                <Input.Password size="large" placeholder={t("auth.reset.newPasswordPlaceholder")} autoComplete="new-password" />
                            </Form.Item>
                            <Form.Item
                                name="confirmPassword"
                                label={t("auth.reset.confirm")}
                                dependencies={["password"]}
                                rules={[
                                    { required: true, message: t("auth.validation.confirmRequired") },
                                    ({ getFieldValue }) => ({
                                        validator: (_, value) => (value === getFieldValue("password") ? Promise.resolve() : Promise.reject(new Error(t("auth.validation.confirm")))),
                                    }),
                                ]}
                            >
                                <Input.Password size="large" placeholder={t("auth.reset.confirmPlaceholder")} autoComplete="new-password" />
                            </Form.Item>
                            <Button className="mt-2" type="primary" size="large" block htmlType="submit" loading={submitting}>
                                {t("auth.reset.submit")}
                            </Button>
                        </Form>
                    </div>
                )}
            </div>
        </div>
    );
}
