import { useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import type { FormInstance } from "antd";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate } from "react-router-dom";

import { FullScreenLoading } from "@admin/components/full-screen-loading";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import { useAuthStore } from "@/stores/use-auth-store";

type LoginValues = { account: string; password: string };

// VALIDATION_FAILED 的字段错误直接落到对应输入框（与主站登录页同一套处理）。
function applyFieldErrors(form: FormInstance, fields?: Record<string, string>) {
    if (!fields) return;
    form.setFields(Object.entries(fields).map(([name, text]) => ({ name, errors: [text] })));
}

// admin 域自己的登录页：复用主站的登录接口（store 的 login → services/api/auth），
// 刻意不提供注册入口——后台账号由管理员开通。
//
// 身份判定仍是唯一的 useConsoleAccess()：已登录的管理员直接进后台，已登录的非管理员去说明页，
// 只有未登录才渲染表单；booting 期间只显示 loading，避免刷新时闪一下登录表单。
//
// 会话跨应用共用（页面底部必须写明）：refresh cookie host-only 挂在 API 域（sim-art.youc.online）的
// /api/auth 下，admin 与主站的前端打的是同一个 API 域，所以两处拿到的是同一个登录态。
export default function AdminLoginPage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const access = useConsoleAccess();
    const login = useAuthStore((state) => state.login);
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState("");
    const [form] = Form.useForm<LoginValues>();

    if (access.phase === "booting") return <FullScreenLoading />;
    if (access.phase === "admin") return <Navigate to="/admin" replace />;
    // 已登录但被强制改密闸门拦下：送去改密页，而不是把表单再渲染一遍。
    if (access.phase === "must-change-password") return <Navigate to="/admin/change-password" replace />;
    if (access.phase === "no-console-access") return <Navigate to="/forbidden" replace />;

    const handleLogin = async (values: LoginValues) => {
        setSubmitting(true);
        setError("");
        try {
            await login(values.account.trim(), values.password);
            // 登录成功即进后台；若账号无后台角色，RequireAdmin 会把它转到 /forbidden。
            navigate("/admin", { replace: true });
        } catch (err) {
            if (err instanceof ApiError && err.code === "VALIDATION_FAILED") {
                applyFieldErrors(form, err.fields);
            } else if (err instanceof ApiError && err.code === "INVALID_CREDENTIALS") {
                form.setFields([{ name: "password", errors: [getApiErrorMessage(err)] }]);
            } else {
                setError(getApiErrorMessage(err));
            }
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <div className="flex h-dvh items-center justify-center bg-background px-4 text-stone-950 dark:text-stone-100">
            <div className="w-full max-w-md rounded-xl border border-stone-200 p-6 dark:border-stone-800">
                <h1 className="text-lg font-semibold">{t("login.title", { ns: "admin" })}</h1>
                <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("login.description", { ns: "admin" })}</p>

                <Form form={form} layout="vertical" requiredMark={false} className="mt-6" onFinish={handleLogin}>
                    {error ? <Alert className="mb-4" type="error" showIcon message={error} /> : null}
                    <Form.Item name="account" label={t("login.account", { ns: "admin" })} rules={[{ required: true, message: t("login.accountRequired", { ns: "admin" }) }]}>
                        <Input size="large" placeholder={t("login.accountPlaceholder", { ns: "admin" })} autoComplete="username" />
                    </Form.Item>
                    <Form.Item name="password" label={t("login.password", { ns: "admin" })} rules={[{ required: true, message: t("login.passwordRequired", { ns: "admin" }) }]}>
                        <Input.Password size="large" placeholder={t("login.passwordPlaceholder", { ns: "admin" })} autoComplete="current-password" />
                    </Form.Item>
                    <Button className="mt-2" type="primary" size="large" block htmlType="submit" loading={submitting}>
                        {t("login.submit", { ns: "admin" })}
                    </Button>
                </Form>

                <p className="mt-4 text-xs leading-5 text-stone-400 dark:text-stone-500">{t("sharedSession.loginNote", { ns: "admin" })}</p>
            </div>
        </div>
    );
}
