import { useState } from "react";
import { Alert, App, Button, Form, Input, InputNumber, Modal, Space, Switch, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import { createAdminPackage, listAdminPackages, updateAdminPackage, type AdminPackage } from "@/services/api/admin";

type PackageFormValues = {
    id: string;
    name: string;
    priceMicros: number;
    bonusMicros: number;
    entitlementDays: number;
    enabled: boolean;
    sort: number;
};

export default function AdminPackagesPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [editing, setEditing] = useState<AdminPackage | null>(null);
    const [creating, setCreating] = useState(false);
    const [form] = Form.useForm<PackageFormValues>();

    const packagesQuery = useQuery({
        queryKey: ["admin", "packages"],
        queryFn: ({ signal }) => listAdminPackages(signal),
    });

    const invalidate = async () => {
        await Promise.all([
            queryClient.invalidateQueries({ queryKey: ["admin", "packages"] }),
            queryClient.invalidateQueries({ queryKey: ["credit-packages"] }),
        ]);
    };

    const saveMutation = useMutation({
        mutationFn: (values: PackageFormValues) => {
            if (editing) {
                return updateAdminPackage(editing.id, {
                    name: values.name,
                    priceMicros: values.priceMicros,
                    bonusMicros: values.bonusMicros,
                    entitlementDays: values.entitlementDays,
                    enabled: values.enabled,
                    sort: values.sort,
                });
            }
            return createAdminPackage(values);
        },
        onSuccess: async () => {
            message.success(t("admin.packages.saved"));
            setEditing(null);
            setCreating(false);
            form.resetFields();
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const openCreate = () => {
        setCreating(true);
        setEditing(null);
        form.setFieldsValue({ id: "", name: "", priceMicros: 10000000, bonusMicros: 0, entitlementDays: 30, enabled: true, sort: 0 });
    };

    const openEdit = (item: AdminPackage) => {
        setEditing(item);
        setCreating(false);
        form.setFieldsValue({ ...item });
    };

    const columns: ColumnsType<AdminPackage> = [
        { title: t("admin.packages.columns.id"), dataIndex: "id", width: 140 },
        { title: t("admin.packages.columns.name"), dataIndex: "name" },
        {
            title: t("admin.packages.columns.price"),
            dataIndex: "priceMicros",
            align: "right",
            width: 120,
            render: (value: number, row) => formatMoney(value, row.currency),
        },
        {
            title: t("admin.packages.columns.points"),
            dataIndex: "priceMicros",
            align: "right",
            width: 140,
            render: (value: number) => formatPoints(value),
        },
        {
            title: t("admin.packages.columns.bonus"),
            dataIndex: "bonusMicros",
            align: "right",
            width: 120,
            render: (value: number) => formatPoints(value),
        },
        { title: t("admin.packages.columns.entitlement"), dataIndex: "entitlementDays", width: 110, render: (value: number) => t("billing.entitlement", { days: value }) },
        {
            title: t("admin.packages.columns.enabled"),
            dataIndex: "enabled",
            width: 100,
            render: (value: boolean) => <Tag color={value ? "success" : "default"}>{value ? t("admin.packages.on") : t("admin.packages.off")}</Tag>,
        },
        { title: t("admin.packages.columns.sort"), dataIndex: "sort", width: 80 },
        {
            title: t("admin.packages.columns.actions"),
            key: "actions",
            width: 100,
            render: (_, row) => (
                <Button size="small" type="link" className="!px-1" onClick={() => openEdit(row)}>
                    {t("admin.edit")}
                </Button>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
                <p className="text-sm text-stone-500 dark:text-stone-400">{t("admin.packages.hint")}</p>
                <Button type="primary" onClick={openCreate}>
                    {t("admin.packages.create")}
                </Button>
            </div>
            {packagesQuery.isError ? (
                <Alert
                    type="error"
                    showIcon
                    message={t("admin.packages.loadFailed")}
                    description={getApiErrorMessage(packagesQuery.error)}
                    action={<Button size="small" onClick={() => void packagesQuery.refetch()}>{t("admin.retry")}</Button>}
                />
            ) : (
                <Table<AdminPackage>
                    rowKey="id"
                    size="middle"
                    loading={packagesQuery.isPending}
                    columns={columns}
                    dataSource={packagesQuery.data?.items ?? []}
                    pagination={false}
                />
            )}

            <Modal
                open={creating || !!editing}
                title={editing ? t("admin.packages.editTitle", { name: editing.name }) : t("admin.packages.createTitle")}
                okText={t("admin.save")}
                cancelText={t("common.cancel")}
                confirmLoading={saveMutation.isPending}
                onCancel={() => {
                    setEditing(null);
                    setCreating(false);
                }}
                onOk={async () => {
                    const values = await form.validateFields();
                    await saveMutation.mutateAsync(values);
                }}
            >
                <Form form={form} layout="vertical">
                    <Form.Item name="id" label={t("admin.packages.fields.id")} extra={t("admin.packages.fields.idExtra")} rules={[{ required: true }]}>
                        <Input disabled={!!editing} maxLength={32} />
                    </Form.Item>
                    <Form.Item name="name" label={t("admin.packages.fields.name")} rules={[{ required: true }]}>
                        <Input maxLength={80} />
                    </Form.Item>
                    <Form.Item
                        name="priceMicros"
                        label={t("admin.packages.fields.priceMicros")}
                        extra={t("admin.packages.fields.priceHint")}
                        rules={[{ required: true }]}
                    >
                        <InputNumber className="w-full" step={1000000} precision={0} min={0} />
                    </Form.Item>
                    <Form.Item name="bonusMicros" label={t("admin.packages.fields.bonusMicros")}>
                        <InputNumber className="w-full" step={100000} precision={0} min={0} />
                    </Form.Item>
                    <Form.Item name="entitlementDays" label={t("admin.packages.fields.entitlementDays")}>
                        <InputNumber className="w-full" precision={0} min={1} max={3650} />
                    </Form.Item>
                    <Form.Item name="sort" label={t("admin.packages.fields.sort")}>
                        <InputNumber className="w-full" precision={0} />
                    </Form.Item>
                    <Form.Item name="enabled" label={t("admin.packages.fields.enabled")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}
