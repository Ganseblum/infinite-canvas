import { useEffect, useState } from "react";
import { Alert, App, Button, Checkbox, Form, Input } from "antd";
import type { FormInstance } from "antd";
import { Check, MailCheck } from "lucide-react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { AnimatedThemeToggler } from "@/components/ui/animated-theme-toggler";
import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import { isPasswordByteLengthValid } from "@/lib/password";
import { cn } from "@/lib/utils";
import { forgotPassword, sendVerifyEmail } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";
import { useThemeStore } from "@/stores/use-theme-store";

/** 登录页的两个主标签页。 */
type TabKey = "login" | "register";
/** 登录表单取值：account 兼容邮箱或用户名。 */
type LoginValues = { account: string; password: string };
/** 注册表单取值：agree 为条款与隐私同意勾选。 */
type RegisterValues = { email: string; username: string; password: string; confirmPassword: string; agree: boolean };

/** 把服务端返回的字段级校验错误铺回表单对应输入框。
 * @param form 目标表单实例
 * @param fields 字段名到错误文案的映射
 */
function applyFieldErrors(form: FormInstance, fields?: Record<string, string>) {
    if (!fields) return;
    form.setFields(Object.entries(fields).map(([name, message]) => ({ name, errors: [message] })));
}

/** 登录/注册页入口：左侧品牌区（仅桌面端）+ 右侧登录、注册、忘记密码三种视图切换。
 * 注册成功停留在「验证邮件已发送」页；字段级错误尽量回填到对应输入框。
 */
export default function LoginPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const location = useLocation();
    const theme = useThemeStore((state) => state.theme);
    const setTheme = useThemeStore((state) => state.setTheme);
    const status = useAuthStore((state) => state.status);
    const login = useAuthStore((state) => state.login);
    const register = useAuthStore((state) => state.register);
    const [tab, setTab] = useState<TabKey>("login");
    const [submitting, setSubmitting] = useState(false);
    const [registerSent, setRegisterSent] = useState(false);
    // 忘记密码视图可由其它页面通过路由 state（{ forgot: true }）直接唤起。
    const [forgotOpen, setForgotOpen] = useState(Boolean((location.state as { forgot?: boolean } | null)?.forgot));
    const [forgotSent, setForgotSent] = useState(false);
    const [loginError, setLoginError] = useState("");
    const [registerError, setRegisterError] = useState("");
    const [loginForm] = Form.useForm<LoginValues>();
    const [registerForm] = Form.useForm<RegisterValues>();
    const [forgotForm] = Form.useForm<{ email: string }>();
    const from = (location.state as { from?: string } | null)?.from || "/";

    // 已登录用户访问登录页直接跳回；注册成功要停留展示验证邮件提示，不触发跳转。
    useEffect(() => {
        if (status === "authenticated" && !registerSent) navigate(from, { replace: true });
    }, [status, registerSent, from, navigate]);

    const handleLogin = async (values: LoginValues) => {
        setSubmitting(true);
        setLoginError("");
        try {
            await login(values.account.trim(), values.password);
            message.success(t("auth.login.success"));
        } catch (error) {
            // 校验失败回填字段错误；凭据错误定位到密码框；其余走表单顶部 Alert。
            if (error instanceof ApiError && error.code === "VALIDATION_FAILED" && error.fields) {
                applyFieldErrors(loginForm, error.fields);
            } else if (error instanceof ApiError && error.code === "INVALID_CREDENTIALS") {
                loginForm.setFields([{ name: "password", errors: [getApiErrorMessage(error)] }]);
            } else {
                setLoginError(getApiErrorMessage(error));
            }
        } finally {
            setSubmitting(false);
        }
    };

    const handleRegister = async (values: RegisterValues) => {
        setSubmitting(true);
        setRegisterError("");
        try {
            await register({ email: values.email.trim(), username: values.username.trim(), password: values.password });
            setRegisterSent(true);
            message.success(t("auth.register.success"));
        } catch (error) {
            // 邮箱/用户名被占用定位到对应输入框，其余走表单顶部 Alert。
            if (error instanceof ApiError && error.code === "VALIDATION_FAILED" && error.fields) {
                applyFieldErrors(registerForm, error.fields);
            } else if (error instanceof ApiError && error.code === "EMAIL_TAKEN") {
                registerForm.setFields([{ name: "email", errors: [getApiErrorMessage(error)] }]);
            } else if (error instanceof ApiError && error.code === "USERNAME_TAKEN") {
                registerForm.setFields([{ name: "username", errors: [getApiErrorMessage(error)] }]);
            } else {
                setRegisterError(getApiErrorMessage(error));
            }
        } finally {
            setSubmitting(false);
        }
    };

    const handleForgot = async (values: { email: string }) => {
        setSubmitting(true);
        try {
            // 成功与「邮箱不存在」都返回 204，统一提示，不泄露账号是否存在。
            await forgotPassword(values.email.trim());
            setForgotSent(true);
        } catch (error) {
            message.error(getApiErrorMessage(error));
        } finally {
            setSubmitting(false);
        }
    };

    const handleResendVerify = async () => {
        try {
            await sendVerifyEmail();
            message.success(t("auth.verify.resent"));
        } catch (error) {
            message.error(getApiErrorMessage(error));
        }
    };

    const brandTitle = tab === "login" ? t("auth.brand.loginTitle") : t("auth.brand.registerTitle");
    const brandDescription = tab === "login" ? t("auth.brand.loginDescription") : t("auth.brand.registerDescription");
    const brandPoints = tab === "login" ? [t("auth.brand.loginPoint1"), t("auth.brand.loginPoint2"), t("auth.brand.loginPoint3")] : [t("auth.brand.registerPoint1"), t("auth.brand.registerPoint2"), t("auth.brand.registerPoint3")];

    return (
        <div className="flex h-dvh bg-background text-foreground">
            <aside className="relative hidden w-[44%] max-w-2xl flex-col justify-between overflow-hidden border-r border-stone-200 bg-[radial-gradient(#e5e7eb_1px,transparent_1px)] p-10 [background-size:16px_16px] lg:flex dark:border-stone-800 dark:bg-[radial-gradient(rgba(245,245,244,0.18)_1px,transparent_1px)]">
                <Link to="/" className="relative flex items-center gap-2 text-sm font-semibold tracking-tight">
                    <span
                        className="size-5 shrink-0 bg-current"
                        style={{
                            mask: "url(/logo.svg) center / contain no-repeat",
                            WebkitMask: "url(/logo.svg) center / contain no-repeat",
                        }}
                    />
                    <span className="text-base font-medium">{t("meta.title")}</span>
                </Link>
                <div className="relative">
                    <h1 className="ai-title-aurora whitespace-pre-line text-4xl font-semibold leading-tight xl:text-5xl">{brandTitle}</h1>
                    <p className="mt-5 max-w-md text-sm leading-7 text-stone-500 dark:text-stone-400">{brandDescription}</p>
                    <ul className="mt-8 space-y-3 text-sm text-stone-600 dark:text-stone-300">
                        {brandPoints.map((point) => (
                            <li key={point} className="flex items-center gap-2.5">
                                <Check className="size-4" />
                                {point}
                            </li>
                        ))}
                    </ul>
                </div>
                <p className="relative text-xs text-stone-400 dark:text-stone-500">{t("auth.brand.copyright")}</p>
            </aside>

            <main className="relative flex min-w-0 flex-1 items-center justify-center overflow-y-auto p-6">
                <AnimatedThemeToggler
                    theme={theme}
                    onThemeChange={setTheme}
                    className="absolute right-4 top-4 inline-flex size-8 items-center justify-center rounded-md text-stone-600 transition hover:bg-black/5 hover:text-stone-950 dark:text-stone-300 dark:hover:bg-white/10 dark:hover:text-white [&_svg]:size-4"
                />
                <div className="w-full max-w-sm py-8">
                    <Link to="/" className="mb-8 flex items-center justify-center gap-2 lg:hidden">
                        <span
                            className="size-5 shrink-0 bg-current"
                            style={{
                                mask: "url(/logo.svg) center / contain no-repeat",
                                WebkitMask: "url(/logo.svg) center / contain no-repeat",
                            }}
                        />
                        <span className="text-base font-medium">{t("meta.title")}</span>
                    </Link>

                    {registerSent ? (
                        <div>
                            <div className="flex size-12 items-center justify-center rounded-full bg-black/5 dark:bg-white/10">
                                <MailCheck className="size-5" />
                            </div>
                            <h2 className="mt-5 text-2xl font-semibold">{t("auth.register.successTitle")}</h2>
                            <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{t("auth.register.successDescription")}</p>
                            <div className="mt-6 flex flex-wrap gap-3">
                                <Button type="primary" onClick={handleResendVerify}>
                                    {t("auth.verify.resend")}
                                </Button>
                                <Button onClick={() => navigate("/")}>{t("auth.register.goHome")}</Button>
                            </div>
                        </div>
                    ) : forgotOpen ? (
                        <div>
                            <h2 className="text-2xl font-semibold">{t("auth.login.forgotTitle")}</h2>
                            <p className="mt-2 text-sm leading-6 text-stone-500 dark:text-stone-400">{t("auth.login.forgotDescription")}</p>
                            {forgotSent ? (
                                <Alert className="mt-6" type="success" showIcon message={t("auth.login.forgotSent")} />
                            ) : (
                                <Form form={forgotForm} layout="vertical" requiredMark={false} className="mt-6" onFinish={handleForgot}>
                                    <Form.Item name="email" label={t("auth.register.email")} rules={[{ required: true, message: t("auth.validation.emailRequired") }, { pattern: /^[^@\s]+@[^@\s]+\.[^@\s]+$/, message: t("auth.validation.email") }]}>
                                        <Input size="large" placeholder={t("auth.register.emailPlaceholder")} autoComplete="email" />
                                    </Form.Item>
                                    <Button type="primary" size="large" block htmlType="submit" loading={submitting}>
                                        {t("auth.login.forgotSubmit")}
                                    </Button>
                                </Form>
                            )}
                            <button
                                type="button"
                                className="mt-6 text-sm text-stone-500 transition hover:text-stone-900 dark:text-stone-400 dark:hover:text-stone-100"
                                onClick={() => {
                                    setForgotOpen(false);
                                    setForgotSent(false);
                                }}
                            >
                                {t("auth.login.backToLogin")}
                            </button>
                        </div>
                    ) : (
                        <div>
                            <h2 className="text-2xl font-semibold">{tab === "login" ? t("auth.login.title") : t("auth.register.title")}</h2>
                            <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{tab === "login" ? t("auth.login.subtitle") : t("auth.register.subtitle")}</p>

                            <div className="mt-8 flex gap-1 rounded-xl border border-stone-200 p-1 dark:border-stone-800">
                                {(["login", "register"] as TabKey[]).map((key) => (
                                    <button
                                        key={key}
                                        type="button"
                                        onClick={() => {
                                            setTab(key);
                                            setLoginError("");
                                            setRegisterError("");
                                        }}
                                        className={cn(
                                            "flex-1 rounded-lg py-2 text-center text-sm transition",
                                            tab === key ? "bg-black/5 font-medium text-stone-950 dark:bg-white/10 dark:text-stone-50" : "text-stone-500 hover:text-stone-900 dark:text-stone-400 dark:hover:text-stone-100",
                                        )}
                                    >
                                        {key === "login" ? t("auth.login.tab") : t("auth.register.tab")}
                                    </button>
                                ))}
                            </div>

                            {tab === "login" ? (
                                <Form form={loginForm} layout="vertical" requiredMark={false} className="mt-6" onFinish={handleLogin}>
                                    {loginError ? <Alert className="mb-4" type="error" showIcon message={loginError} /> : null}
                                    <Form.Item name="account" label={t("auth.login.account")} rules={[{ required: true, message: t("auth.validation.accountRequired") }]}>
                                        <Input size="large" placeholder={t("auth.login.accountPlaceholder")} autoComplete="username" />
                                    </Form.Item>
                                    <Form.Item
                                        name="password"
                                        label={
                                            <span className="flex w-full items-center justify-between">
                                                <span>{t("auth.login.password")}</span>
                                                <button type="button" className="text-xs text-stone-500 transition hover:text-stone-900 dark:text-stone-400 dark:hover:text-stone-100" onClick={() => setForgotOpen(true)}>
                                                    {t("auth.login.forgot")}
                                                </button>
                                            </span>
                                        }
                                        rules={[{ required: true, message: t("auth.validation.passwordRequired") }]}
                                    >
                                        <Input.Password size="large" placeholder={t("auth.login.passwordPlaceholder")} autoComplete="current-password" />
                                    </Form.Item>
                                    <Button className="mt-2" type="primary" size="large" block htmlType="submit" loading={submitting}>
                                        {t("auth.login.submit")}
                                    </Button>
                                </Form>
                            ) : (
                                <Form form={registerForm} layout="vertical" requiredMark={false} className="mt-6" onFinish={handleRegister}>
                                    {registerError ? <Alert className="mb-4" type="error" showIcon message={registerError} /> : null}
                                    <Form.Item name="email" label={t("auth.register.email")} rules={[{ required: true, message: t("auth.validation.emailRequired") }, { pattern: /^[^@\s]+@[^@\s]+\.[^@\s]+$/, message: t("auth.validation.email") }]}>
                                        <Input size="large" placeholder={t("auth.register.emailPlaceholder")} autoComplete="email" />
                                    </Form.Item>
                                    <Form.Item name="username" label={t("auth.register.username")} rules={[{ required: true, message: t("auth.validation.usernameRequired") }, { pattern: /^[a-zA-Z0-9_-]{3,32}$/, message: t("auth.validation.username") }]}>
                                        <Input size="large" placeholder={t("auth.register.usernamePlaceholder")} autoComplete="username" />
                                    </Form.Item>
                                    <Form.Item
                                        name="password"
                                        label={t("auth.register.password")}
                                        rules={[
                                            { required: true, message: t("auth.validation.passwordRequired") },
                                            // 与服务端一致按字节长度校验，避免多字节密码提交后才报错。
                                            { validator: (_, value: string) => (!value || isPasswordByteLengthValid(value) ? Promise.resolve() : Promise.reject(new Error(t("auth.validation.password")))) },
                                        ]}
                                    >
                                        <Input.Password size="large" placeholder={t("auth.register.passwordPlaceholder")} autoComplete="new-password" />
                                    </Form.Item>
                                    <Form.Item
                                        name="confirmPassword"
                                        label={t("auth.register.confirm")}
                                        // 依赖 password 字段，密码变更时联动重新校验两次输入是否一致。
                                        dependencies={["password"]}
                                        rules={[
                                            { required: true, message: t("auth.validation.confirmRequired") },
                                            ({ getFieldValue }) => ({
                                                validator: (_, value) => (value === getFieldValue("password") ? Promise.resolve() : Promise.reject(new Error(t("auth.validation.confirm")))),
                                            }),
                                        ]}
                                    >
                                        <Input.Password size="large" placeholder={t("auth.register.confirmPlaceholder")} autoComplete="new-password" />
                                    </Form.Item>
                                    {/* 条款与隐私同意勾选：未勾选无法提交注册（差异清单 #113），链接新窗口打开公开页。 */}
                                    <Form.Item
                                        name="agree"
                                        valuePropName="checked"
                                        rules={[{ validator: (_, value: boolean) => (value ? Promise.resolve() : Promise.reject(new Error(t("auth.register.agreeRequired")))) }]}
                                    >
                                        <Checkbox className="text-sm text-stone-500 dark:text-stone-400">
                                            {t("auth.register.agreePrefix")}
                                            <a href="/terms" target="_blank" rel="noreferrer" className="mx-1">
                                                {t("auth.register.terms")}
                                            </a>
                                            {t("auth.register.agreeAnd")}
                                            <a href="/privacy" target="_blank" rel="noreferrer" className="mx-1">
                                                {t("auth.register.privacy")}
                                            </a>
                                        </Checkbox>
                                    </Form.Item>
                                    <Button className="mt-2" type="primary" size="large" block htmlType="submit" loading={submitting}>
                                        {t("auth.register.submit")}
                                    </Button>
                                </Form>
                            )}
                        </div>
                    )}
                </div>
            </main>
        </div>
    );
}
