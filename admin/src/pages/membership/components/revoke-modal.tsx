import { useEffect } from "react";
import { App, Form, Input, Modal } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { revokeAdminMembership, type AdminMembershipSubscription } from "@admin/services/api/admin";

// 作废订阅：先填原因，提交时再弹一次确认——两步都过了才会真正调 DELETE。
export function RevokeModal({ subscription, onClose }: { subscription: AdminMembershipSubscription | null; onClose: () => void }) {
    const { message, modal } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();

    type RevokeFormValues = { reason: string };
    const [form] = Form.useForm<RevokeFormValues>();

    useEffect(() => {
        if (subscription) form.setFieldsValue({ reason: "" });
    }, [subscription, form]);

    const mutation = useMutation({
        mutationFn: (values: RevokeFormValues) => revokeAdminMembership(subscription!.id, values.reason.trim()),
        onSuccess: async () => {
            message.success(t("admin.membership.revoked"));
            onClose();
            await queryClient.invalidateQueries({ queryKey: ["admin", "membership"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    return (
        <Modal
            open={!!subscription}
            title={t("admin.membership.revokeTitle", { name: subscription?.userUsername ?? "" })}
            okText={t("admin.membership.revokeSubmit")}
            cancelText={t("common.cancel")}
            confirmLoading={mutation.isPending}
            onCancel={onClose}
            onOk={async () => {
                const values = await form.validateFields();
                modal.confirm({
                    title: t("admin.membership.revokeConfirmTitle"),
                    content: t("admin.membership.revokeConfirmContent"),
                    okText: t("admin.membership.revokeOk"),
                    okButtonProps: { danger: true },
                    cancelText: t("common.cancel"),
                    onOk: () => mutation.mutateAsync(values),
                });
            }}
        >
            <Form form={form} layout="vertical">
                <Form.Item name="reason" label={t("admin.membership.revokeReason")} rules={[{ required: true, message: t("admin.membership.revokeReasonRequired") }]}>
                    <Input maxLength={200} placeholder={t("admin.membership.reasonPlaceholder")} />
                </Form.Item>
            </Form>
        </Modal>
    );
}
