import { useState } from "react";
import { App, Button, Form, Input, Modal } from "antd";
import { useMutation } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import { isPasswordByteLengthValid } from "@/lib/password";
import { changePassword } from "@/services/api/account";

/** 修改密码弹窗表单的取值结构。 */
type PasswordFormValues = {
    oldPassword: string;
    newPassword: string;
    confirmPassword: string;
};

/** 个人中心安全区块：修改密码入口与弹窗表单（旧密码 + 新密码 + 确认）。 */
export function SecuritySection() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);
    const [form] = Form.useForm<PasswordFormValues>();

    const mutation = useMutation({
        mutationFn: (values: PasswordFormValues) => changePassword({ oldPassword: values.oldPassword, newPassword: values.newPassword }),
        onSuccess: () => {
            message.success(t("profile.security.changed"));
            setOpen(false);
            form.resetFields();
        },
        onError: (error) => {
            // 旧密码错误定位到对应输入框内提示，其余错误走全局 toast。
            if (error instanceof ApiError && error.code === "INVALID_CREDENTIALS") {
                form.setFields([{ name: "oldPassword", errors: [getApiErrorMessage(error)] }]);
                return;
            }
            message.error(getApiErrorMessage(error));
        },
    });

    const close = () => {
        setOpen(false);
        form.resetFields();
    };

    return (
        <section id="profile-security" className="scroll-mt-4 rounded-xl border border-stone-200 p-6 dark:border-stone-800">
            <h2 className="text-lg font-semibold">{t("profile.security.title")}</h2>
            <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
                <div>
                    <p className="text-sm font-medium">{t("profile.security.password")}</p>
                    <p className="mt-1 text-xs text-stone-500 dark:text-stone-400">{t("profile.security.passwordHint")}</p>
                </div>
                <Button onClick={() => setOpen(true)}>{t("profile.security.change")}</Button>
            </div>
            <Modal
                open={open}
                title={t("profile.security.change")}
                okText={t("profile.security.submit")}
                cancelText={t("common.cancel")}
                confirmLoading={mutation.isPending}
                onCancel={close}
                onOk={async () => {
                    const values = await form.validateFields();
                    await mutation.mutateAsync(values);
                }}
            >
                <Form form={form} layout="vertical" className="mt-4">
                    <Form.Item name="oldPassword" label={t("profile.security.current")} rules={[{ required: true, message: t("profile.security.currentRequired") }]}>
                        <Input.Password autoComplete="current-password" />
                    </Form.Item>
                    <Form.Item
                        name="newPassword"
                        label={t("profile.security.next")}
                        rules={[
                            { required: true, message: t("profile.security.nextRequired") },
                            // 服务端按字节长度限制密码，前端按同一口径预校验，避免提交后才发现超长。
                            { validator: (_, value: string) => (!value || isPasswordByteLengthValid(value) ? Promise.resolve() : Promise.reject(new Error(t("profile.security.nextLength")))) },
                        ]}
                    >
                        <Input.Password autoComplete="new-password" />
                    </Form.Item>
                    <Form.Item
                        name="confirmPassword"
                        label={t("profile.security.confirm")}
                        dependencies={["newPassword"]}
                        rules={[
                            { required: true, message: t("profile.security.confirmRequired") },
                            ({ getFieldValue }) => ({
                                validator: (_, value) => (!value || getFieldValue("newPassword") === value ? Promise.resolve() : Promise.reject(new Error(t("profile.security.confirmMismatch")))),
                            }),
                        ]}
                    >
                        <Input.Password autoComplete="new-password" />
                    </Form.Item>
                </Form>
            </Modal>
        </section>
    );
}
