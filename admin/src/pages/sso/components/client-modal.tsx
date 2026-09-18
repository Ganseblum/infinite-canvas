import { useEffect } from "react";
import { App, Form, Input, Modal, Switch } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { createAdminSsoClient, updateAdminSsoClient, type AdminSsoClient } from "@admin/services/api/admin";
import { CURRENT_PRODUCT_KEY } from "@admin/lib/products";

// http 只放宽到本机回环地址：本地联调接入方的回调允许 http，其余一律 https。
const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]", "::1"]);

// 新建与编辑共用一个弹窗：表单里回调地址按多行文本输入（一行一个），
// 校验通过后才拆成数组提交；新建成功把一次性明文 secret 交给上层弹窗展示。
export function ClientModal({
    open,
    editing,
    onClose,
    onCreated,
}: {
    open: boolean;
    editing: AdminSsoClient | null;
    onClose: () => void;
    onCreated: (client: AdminSsoClient, clientSecret: string) => void;
}) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();

    type ClientFormValues = { name: string; redirectUris: string; enabled: boolean };
    const [form] = Form.useForm<ClientFormValues>();

    // 逐行校验并拆出回调地址数组；校验失败抛出的 Error 文案会原样展示给管理员。
    const toRedirectUris = (value: string): string[] => {
        const lines = (value ?? "")
            .split("\n")
            .map((line) => line.trim())
            .filter(Boolean);
        for (const line of lines) {
            let url: URL;
            try {
                url = new URL(line);
            } catch {
                throw new Error(t("admin.sso.fields.redirectUrisInvalid", { uri: line }));
            }
            if (url.protocol !== "https:" && !(url.protocol === "http:" && LOCAL_HOSTS.has(url.hostname))) {
                throw new Error(t("admin.sso.fields.redirectUrisInsecure", { uri: line }));
            }
        }
        return lines;
    };

    useEffect(() => {
        if (!open) return;
        form.setFieldsValue(
            editing
                ? { name: editing.name, redirectUris: editing.redirectUris.join("\n"), enabled: editing.enabled }
                : { name: "", redirectUris: "", enabled: true },
        );
    }, [open, editing, form]);

    const mutation = useMutation({
        mutationFn: async (values: ClientFormValues) => {
            const name = values.name.trim();
            const redirectUris = toRedirectUris(values.redirectUris);
            if (editing) {
                await updateAdminSsoClient(editing.id, { name, redirectUris, enabled: values.enabled });
                return null;
            }
            return createAdminSsoClient({ name, redirectUris, productId: CURRENT_PRODUCT_KEY });
        },
        onSuccess: async (data) => {
            if (data) {
                message.success(t("admin.sso.created"));
                onClose();
                onCreated(data.client, data.clientSecret);
            } else {
                message.success(t("admin.sso.saved"));
                onClose();
            }
            await queryClient.invalidateQueries({ queryKey: ["admin", "sso"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    return (
        <Modal
            open={open}
            title={editing ? t("admin.sso.editTitle", { name: editing.name }) : t("admin.sso.createTitle")}
            okText={t("admin.save")}
            cancelText={t("common.cancel")}
            confirmLoading={mutation.isPending}
            onCancel={onClose}
            onOk={async () => {
                const values = await form.validateFields();
                await mutation.mutateAsync(values);
            }}
        >
            <Form form={form} layout="vertical">
                <Form.Item name="name" label={t("admin.sso.fields.name")} rules={[{ required: true, message: t("admin.sso.fields.nameRequired") }]}>
                    <Input maxLength={80} placeholder={t("admin.sso.fields.namePlaceholder")} />
                </Form.Item>
                <Form.Item
                    name="redirectUris"
                    label={t("admin.sso.fields.redirectUris")}
                    extra={t("admin.sso.fields.redirectUrisHint")}
                    rules={[
                        { required: true, message: t("admin.sso.fields.redirectUrisRequired") },
                        {
                            validator: async (_, value: string) => {
                                toRedirectUris(value ?? "");
                            },
                        },
                    ]}
                >
                    <Input.TextArea rows={4} placeholder={"https://app.example.com/auth/callback\nhttp://localhost:3000/auth/callback"} />
                </Form.Item>
                {editing ? (
                    <Form.Item name="enabled" label={t("admin.sso.fields.enabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                ) : null}
            </Form>
        </Modal>
    );
}
