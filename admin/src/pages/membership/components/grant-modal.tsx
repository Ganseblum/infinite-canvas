import { useEffect } from "react";
import { App, Form, Input, Modal, Select } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { compensateAdminMembership, grantAdminMembership } from "@admin/services/api/admin";

// 发放/续期与补偿共用一个弹窗：契约上两个接口的请求、响应形状相同，仅审计口径不同，mode 决定调用哪个。
// 补偿语义见「补偿发放」：同一份表单，落库的审计来源不同。
export function GrantModal({ open, mode, onClose }: { open: boolean; mode: "grant" | "compensate"; onClose: () => void }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();

    type GrantFormValues = { user: string; planId: string; reason: string };
    const [form] = Form.useForm<GrantFormValues>();

    // 弹窗每次打开都回到初始值：档位默认付费档，其余清空。
    useEffect(() => {
        if (open) form.setFieldsValue({ user: "", planId: "paid", reason: "" });
    }, [open, form]);

    const mutation = useMutation({
        mutationFn: (values: GrantFormValues) => {
            const input = { userId: values.user.trim(), planId: values.planId, reason: values.reason.trim() };
            return mode === "compensate" ? compensateAdminMembership(input) : grantAdminMembership(input);
        },
        onSuccess: async () => {
            message.success(t(mode === "compensate" ? "admin.membership.compensated" : "admin.membership.granted"));
            onClose();
            await queryClient.invalidateQueries({ queryKey: ["admin", "membership"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    return (
        <Modal
            open={open}
            title={t(mode === "compensate" ? "admin.membership.compensateTitle" : "admin.membership.grantTitle")}
            okText={t("admin.membership.grantSubmit")}
            cancelText={t("common.cancel")}
            confirmLoading={mutation.isPending}
            onCancel={onClose}
            onOk={async () => {
                const values = await form.validateFields();
                await mutation.mutateAsync(values);
            }}
        >
            <Form form={form} layout="vertical">
                <Form.Item name="user" label={t("admin.membership.user")} extra={t("admin.membership.userHint")} rules={[{ required: true, message: t("admin.membership.userRequired") }]}>
                    <Input maxLength={200} placeholder={t("admin.membership.userPlaceholder")} />
                </Form.Item>
                <Form.Item name="planId" label={t("admin.membership.plan")} rules={[{ required: true, message: t("admin.membership.planRequired") }]}>
                    <Select
                        options={(["free", "paid", "sunset"] as const).map((value) => ({ value, label: t(`admin.users.plans.${value}`) }))}
                    />
                </Form.Item>
                <Form.Item name="reason" label={t("admin.membership.reason")} rules={[{ required: true, message: t("admin.membership.reasonRequired") }]}>
                    <Input maxLength={200} placeholder={t("admin.membership.reasonPlaceholder")} />
                </Form.Item>
            </Form>
        </Modal>
    );
}
