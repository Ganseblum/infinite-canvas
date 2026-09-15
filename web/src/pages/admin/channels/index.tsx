import { useState } from "react";
import { Alert, App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import {
    createAdminChannel,
    deleteAdminChannel,
    listAdminChannels,
    updateAdminChannel,
    type AdminChannel,
    type AdminChannelInput,
} from "@/services/api/admin";

type ChannelFormValues = AdminChannelInput & { apiKey?: string };

const FORMATS: AdminChannel["apiFormat"][] = ["openai", "gemini", "ark"];

export default function AdminChannelsPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [editing, setEditing] = useState<AdminChannel | null>(null);
    const [creating, setCreating] = useState(false);
    const [form] = Form.useForm<ChannelFormValues>();

    const channelsQuery = useQuery({
        queryKey: ["admin", "channels"],
        queryFn: ({ signal }) => listAdminChannels(signal),
    });

    const invalidate = async () => {
        await Promise.all([
            queryClient.invalidateQueries({ queryKey: ["admin", "channels"] }),
            queryClient.invalidateQueries({ queryKey: ["admin", "models"] }),
            queryClient.invalidateQueries({ queryKey: ["models"] }),
        ]);
    };

    const saveMutation = useMutation({
        mutationFn: (values: ChannelFormValues) => {
            if (editing) {
                // Key 只写不读：留空表示保持原值。
                const { apiKey, ...rest } = values;
                return updateAdminChannel(editing.id, apiKey ? { ...rest, apiKey } : rest);
            }
            return createAdminChannel({ ...values, apiKey: values.apiKey || "" });
        },
        onSuccess: async () => {
            message.success(t("admin.channels.saved"));
            closeEditor();
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const deleteMutation = useMutation({
        mutationFn: (id: string) => deleteAdminChannel(id),
        onSuccess: async () => {
            message.success(t("admin.channels.deleted"));
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const closeEditor = () => {
        setEditing(null);
        setCreating(false);
        form.resetFields();
    };

    const openCreate = () => {
        setCreating(true);
        setEditing(null);
        form.setFieldsValue({ name: "", baseUrl: "", apiFormat: "openai", apiKey: "", priority: 0, enabled: true });
    };

    const openEdit = (item: AdminChannel) => {
        setEditing(item);
        setCreating(false);
        form.setFieldsValue({
            name: item.name,
            baseUrl: item.baseUrl,
            apiFormat: item.apiFormat,
            apiKey: "",
            priority: item.priority,
            enabled: item.enabled,
        });
    };

    const columns: ColumnsType<AdminChannel> = [
        { title: t("admin.channels.columns.name"), dataIndex: "name" },
        { title: t("admin.channels.columns.format"), dataIndex: "apiFormat", width: 110, render: (value: string) => <Tag>{value}</Tag> },
        { title: t("admin.channels.columns.baseUrl"), dataIndex: "baseUrl", render: (value: string) => <span className="font-mono text-xs">{value}</span> },
        {
            title: t("admin.channels.columns.key"),
            dataIndex: "hasKey",
            width: 110,
            render: (value: boolean) => (value ? <Tag color="green">{t("admin.channels.keySet")}</Tag> : <Tag color="red">{t("admin.channels.keyMissing")}</Tag>),
        },
        { title: t("admin.channels.columns.priority"), dataIndex: "priority", width: 90 },
        {
            title: t("admin.channels.columns.enabled"),
            dataIndex: "enabled",
            width: 100,
            render: (value: boolean) => <Tag color={value ? "success" : "default"}>{value ? t("admin.channels.on") : t("admin.channels.off")}</Tag>,
        },
        {
            title: t("admin.channels.columns.actions"),
            key: "actions",
            width: 160,
            render: (_, row) => (
                <Space size={2}>
                    <Button size="small" type="link" className="!px-1" onClick={() => openEdit(row)}>
                        {t("admin.edit")}
                    </Button>
                    <Popconfirm title={t("admin.channels.deleteConfirm")} description={t("admin.channels.deleteHint")} onConfirm={() => deleteMutation.mutate(row.id)}>
                        <Button size="small" type="link" danger className="!px-1">
                            {t("admin.delete")}
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
                <p className="text-sm text-stone-500 dark:text-stone-400">{t("admin.channels.hint")}</p>
                <Button type="primary" onClick={openCreate}>
                    {t("admin.channels.create")}
                </Button>
            </div>
            {channelsQuery.isError ? (
                <Alert
                    type="error"
                    showIcon
                    message={t("admin.channels.loadFailed")}
                    description={getApiErrorMessage(channelsQuery.error)}
                    action={<Button size="small" onClick={() => void channelsQuery.refetch()}>{t("admin.retry")}</Button>}
                />
            ) : (
                <Table<AdminChannel>
                    rowKey="id"
                    size="middle"
                    loading={channelsQuery.isPending}
                    columns={columns}
                    dataSource={channelsQuery.data?.items ?? []}
                    pagination={false}
                />
            )}

            <Modal
                open={creating || !!editing}
                title={editing ? t("admin.channels.editTitle", { name: editing.name }) : t("admin.channels.createTitle")}
                okText={t("admin.save")}
                cancelText={t("common.cancel")}
                confirmLoading={saveMutation.isPending}
                onCancel={closeEditor}
                onOk={async () => {
                    const values = await form.validateFields();
                    await saveMutation.mutateAsync(values);
                }}
            >
                <Alert className="mb-4" type="info" showIcon message={t("admin.channels.keyHint")} />
                <Form form={form} layout="vertical">
                    <Form.Item name="name" label={t("admin.channels.fields.name")} rules={[{ required: true }]}>
                        <Input maxLength={80} />
                    </Form.Item>
                    <Form.Item name="baseUrl" label={t("admin.channels.fields.baseUrl")} extra={t("admin.channels.fields.baseUrlExtra")} rules={[{ required: true }]}>
                        <Input placeholder="https://api.example.com/v1" />
                    </Form.Item>
                    <Form.Item name="apiFormat" label={t("admin.channels.fields.format")} rules={[{ required: true }]}>
                        <Select options={FORMATS.map((value) => ({ value, label: value }))} />
                    </Form.Item>
                    <Form.Item
                        name="apiKey"
                        label={t("admin.channels.fields.apiKey")}
                        extra={editing ? t("admin.channels.fields.apiKeyKeep") : undefined}
                        rules={editing ? [] : [{ required: true, message: t("admin.channels.fields.apiKeyRequired") }]}
                    >
                        <Input.Password autoComplete="off" placeholder="sk-..." />
                    </Form.Item>
                    <Form.Item name="priority" label={t("admin.channels.fields.priority")} extra={t("admin.channels.fields.priorityExtra")}>
                        <InputNumber className="w-full" precision={0} />
                    </Form.Item>
                    <Form.Item name="enabled" label={t("admin.channels.fields.enabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}
