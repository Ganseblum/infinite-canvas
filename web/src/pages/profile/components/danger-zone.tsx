import { useState } from "react";
import { Alert, App, Button, Form, Input, Modal } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { ApiError, getApiErrorMessage } from "@/lib/api-error";
import type { DeletionState } from "@/services/api/account";
import { cancelAccountDeletion, requestAccountDeletion } from "@/services/api/deletion";
import { useAuthStore } from "@/stores/use-auth-store";

type DeletionFormValues = { password: string };

export function DangerZone({ deletion }: { deletion?: DeletionState }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const [open, setOpen] = useState(false);
    const [form] = Form.useForm<DeletionFormValues>();

    const scheduledAt = deletion?.status === "pending" ? deletion.scheduledAt : null;
    const pending = !!scheduledAt;
    const remainingDays = scheduledAt ? Math.max(0, Math.ceil(dayjs(scheduledAt).diff(dayjs(), "hour") / 24)) : 0;

    const cancelMutation = useMutation({
        mutationFn: () => cancelAccountDeletion(),
        onSuccess: async () => {
            message.success(t("profile.danger.cancelSuccess"));
            await queryClient.invalidateQueries({ queryKey: ["me", userId] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const deleteMutation = useMutation({
        mutationFn: (password: string) => requestAccountDeletion(password),
        onSuccess: async () => {
            message.success(t("profile.danger.submitted"));
            setOpen(false);
            form.resetFields();
            await queryClient.invalidateQueries({ queryKey: ["me", userId] });
        },
        onError: (error) => {
            if (error instanceof ApiError && error.code === "INVALID_CREDENTIALS") {
                form.setFields([{ name: "password", errors: [getApiErrorMessage(error)] }]);
                return;
            }
            message.error(getApiErrorMessage(error));
        },
    });

    return (
        <section id="profile-danger" className="scroll-mt-4 rounded-xl border border-red-200 p-6 dark:border-red-900/60">
            <h2 className="text-lg font-semibold text-red-600 dark:text-red-400">{t("profile.danger.title")}</h2>
            {pending ? (
                <Alert
                    className="mt-4"
                    type="warning"
                    showIcon
                    message={t("profile.danger.pendingTitle")}
                    description={
                        <span>
                            {t("profile.danger.pendingDescription", { date: dayjs(scheduledAt).format("YYYY-MM-DD HH:mm") })}
                            {t("profile.danger.remaining", { count: remainingDays })}
                        </span>
                    }
                    action={
                        <Button size="small" danger loading={cancelMutation.isPending} onClick={() => cancelMutation.mutate()}>
                            {t("profile.danger.cancel")}
                        </Button>
                    }
                />
            ) : (
                <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
                    <p className="text-sm text-stone-500 dark:text-stone-400">{t("profile.danger.description")}</p>
                    <Button danger onClick={() => setOpen(true)}>
                        {t("profile.danger.deleteAccount")}
                    </Button>
                </div>
            )}
            <Modal
                open={open}
                title={t("profile.danger.modalTitle")}
                okText={t("profile.danger.submit")}
                cancelText={t("common.cancel")}
                okButtonProps={{ danger: true }}
                confirmLoading={deleteMutation.isPending}
                onCancel={() => {
                    setOpen(false);
                    form.resetFields();
                }}
                onOk={async () => {
                    const values = await form.validateFields();
                    await deleteMutation.mutateAsync(values.password);
                }}
            >
                <ul className="list-disc space-y-2 pl-5 text-sm leading-6 text-stone-600 dark:text-stone-300">
                    <li>{t("profile.danger.bulletCredits")}</li>
                    <li>{t("profile.danger.bulletGrace")}</li>
                    <li>{t("profile.danger.bulletImmediate")}</li>
                    <li>{t("profile.danger.bulletData")}</li>
                </ul>
                <Form form={form} layout="vertical" className="mt-5">
                    <Form.Item name="password" label={t("profile.danger.password")} rules={[{ required: true, message: t("profile.danger.passwordRequired") }]}>
                        <Input.Password autoComplete="current-password" />
                    </Form.Item>
                </Form>
            </Modal>
        </section>
    );
}
