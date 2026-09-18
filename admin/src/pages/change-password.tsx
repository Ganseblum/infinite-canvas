import { useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Navigate } from "react-router-dom";

import { FullScreenLoading } from "@admin/components/full-screen-loading";
import { useConsoleAccess, type AuthUserWithGate } from "@admin/hooks/use-console-access";
import { changeMyPassword } from "@admin/services/api/admin";
import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import { useAuthStore } from "@/stores/use-auth-store";

type ChangePasswordValues = {
    oldPassword: string;
    newPassword: string;
    confirmPassword: string;
};

// 服务端要求新密码 8-72 字节（UTF-8）。web 的 isPasswordByteLengthValid 不在 admin 复用白名单里，
// 这里用 TextEncoder 内联同一断言，避免客户端放行、服务端才拒绝。
function isPasswordByteLengthValid(value: string) {
    const bytes = new TextEncoder().encode(value).length;
    return bytes >= 8 && bytes <= 72;
}

// 强制改密页：must_change_password 置位后服务端只放行登出/刷新/改密（其余接口一律 403
// PASSWORD_CHANGE_REQUIRED），所以这里不放后台外壳（没有侧边栏、没有去别的页面的入口），
// 只提供两条出路：改密成功自动进后台，或退出登录。
export default function ChangePasswordPage() {
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const access = useConsoleAccess();
    const logout = useAuthStore((state) => state.logout);
    const [form] = Form.useForm<ChangePasswordValues>();
    const [error, setError] = useState("");
    const [leaving, setLeaving] = useState(false);

    const mutation = useMutation({
        mutationFn: (values: ChangePasswordValues) => changeMyPassword({ oldPassword: values.oldPassword, newPassword: values.newPassword }),
        // 改密是 must_change_password 的唯一清除点：先同步清掉会话状态里的标记（store 没有专门的
        // 动作；该字段登录/刷新响应一直下发，只是 web 的类型没声明），让 useConsoleAccess 的前置
        // 闸门立即解除、/admin/me 恢复放行；拉到 200 后相位翻成 admin，本页的守卫直接把用户送去
        // /admin，由首页守卫落总览或第一个有权限的页面。
        onSuccess: () => {
            const user = useAuthStore.getState().user as AuthUserWithGate | null;
            if (user) {
                const cleared: AuthUserWithGate = { ...user, mustChangePassword: false };
                useAuthStore.setState({ user: cleared });
            }
            void queryClient.refetchQueries({ queryKey: ["admin", "me"], exact: true });
        },
        onError: (err) => {
            if (err instanceof ApiError && err.code === "INVALID_CREDENTIALS") {
                form.setFields([{ name: "oldPassword", errors: [t("changePassword.oldPasswordWrong", { ns: "admin" })] }]);
            } else if (err instanceof ApiError && err.code === "VALIDATION_FAILED" && err.fields) {
                // 服务端该接口的字段名（newPassword）与表单字段一一对应。
                form.setFields(Object.entries(err.fields).map(([name, text]) => ({ name: name as keyof ChangePasswordValues, errors: [text] })));
            } else {
                setError(getApiErrorMessage(err));
            }
        },
    });

    // 所有 hook 都在提前 return 之前调用（与登录页同一纪律），下面的相位分支才不会破坏 hooks 顺序。
    if (access.phase === "booting") return <FullScreenLoading />;
    if (access.phase === "signed-out") return <Navigate to="/login" replace />;
    // 标志已清除（改密成功，或管理员后台解除）时 /admin/me 重新放行：回正常后台。
    if (access.phase === "admin") return <Navigate to="/admin" replace />;
    if (access.phase === "no-console-access") return <Navigate to="/forbidden" replace />;

    return (
        <div className="flex h-dvh items-center justify-center bg-background px-4 text-stone-950 dark:text-stone-100">
            <div className="w-full max-w-md rounded-xl border border-stone-200 p-6 dark:border-stone-800">
                <h1 className="text-lg font-semibold">{t("changePassword.title", { ns: "admin" })}</h1>
                <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("changePassword.description", { ns: "admin" })}</p>

                <Form
                    form={form}
                    layout="vertical"
                    requiredMark={false}
                    className="mt-6"
                    onFinish={(values) => {
                        setError("");
                        mutation.mutate(values);
                    }}
                >
                    {error ? <Alert className="mb-4" type="error" showIcon message={error} /> : null}
                    <Form.Item name="oldPassword" label={t("changePassword.oldPassword", { ns: "admin" })} rules={[{ required: true, message: t("changePassword.oldPasswordRequired", { ns: "admin" }) }]}>
                        <Input.Password size="large" placeholder={t("changePassword.oldPasswordPlaceholder", { ns: "admin" })} autoComplete="current-password" />
                    </Form.Item>
                    <Form.Item
                        name="newPassword"
                        label={t("changePassword.newPassword", { ns: "admin" })}
                        rules={[
                            { required: true, message: t("changePassword.newPasswordRequired", { ns: "admin" }) },
                            { validator: (_, value: string) => (!value || isPasswordByteLengthValid(value) ? Promise.resolve() : Promise.reject(new Error(t("changePassword.newPasswordLength", { ns: "admin" })))) },
                        ]}
                    >
                        <Input.Password size="large" placeholder={t("changePassword.newPasswordPlaceholder", { ns: "admin" })} autoComplete="new-password" />
                    </Form.Item>
                    <Form.Item
                        name="confirmPassword"
                        label={t("changePassword.confirmPassword", { ns: "admin" })}
                        dependencies={["newPassword"]}
                        rules={[
                            { required: true, message: t("changePassword.confirmPasswordRequired", { ns: "admin" }) },
                            ({ getFieldValue }) => ({
                                validator: (_, value) => (!value || getFieldValue("newPassword") === value ? Promise.resolve() : Promise.reject(new Error(t("changePassword.confirmPasswordMismatch", { ns: "admin" })))),
                            }),
                        ]}
                    >
                        <Input.Password size="large" placeholder={t("changePassword.confirmPasswordRequired", { ns: "admin" })} autoComplete="new-password" />
                    </Form.Item>
                    <Button className="mt-2" type="primary" size="large" block htmlType="submit" loading={mutation.isPending}>
                        {t("changePassword.submit", { ns: "admin" })}
                    </Button>
                </Form>

                {/* 登录态与主站共用，退出会影响主站，必须在按钮旁写明（与后台外壳、说明页同一份文案）。 */}
                <div className="mt-4">
                    <Button
                        type="text"
                        size="small"
                        loading={leaving}
                        onClick={() => {
                            setLeaving(true);
                            void logout().finally(() => setLeaving(false));
                        }}
                    >
                        {t("userMenu.logout")}
                    </Button>
                    <p className="mt-1 text-[11px] leading-4 text-stone-400 dark:text-stone-500">{t("sharedSession.logoutNote", { ns: "admin" })}</p>
                </div>
            </div>
        </div>
    );
}
