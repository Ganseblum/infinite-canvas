import { useState } from "react";
import { App, Button, Popconfirm, Space, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { QueryError } from "@admin/components/query-error";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { PERM } from "@admin/lib/admin-nav";
import { deleteAdminSsoClient, listAdminSsoClients, resetAdminSsoClientSecret, type AdminSsoClient } from "@admin/services/api/admin";
import { ClientModal } from "./components/client-modal";
import { SecretModal } from "./components/secret-modal";

export default function AdminSsoPage() {
    const { message, modal } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const access = useConsoleAccess();
    // 前端只隐藏入口：没有 sso.write 就不给新增/编辑/重置密钥/删除，服务端仍会独立校验。
    const canWrite = access.phase === "admin" && access.permissions.includes(PERM.ssoWrite);
    const [creating, setCreating] = useState(false);
    const [editing, setEditing] = useState<AdminSsoClient | null>(null);
    // 一次性明文 secret（新建 / 重置密钥）：关掉弹窗就再也看不到，只能重置。
    const [secretView, setSecretView] = useState<{ clientId: string; secret: string } | null>(null);

    const clientsQuery = useQuery({
        queryKey: ["admin", "sso", "clients"],
        queryFn: ({ signal }) => listAdminSsoClients(signal),
    });

    const invalidate = async () => {
        await queryClient.invalidateQueries({ queryKey: ["admin", "sso"] });
    };

    const resetMutation = useMutation({
        mutationFn: (id: string) => resetAdminSsoClientSecret(id),
        onSuccess: async (data, id) => {
            const client = clientsQuery.data?.items.find((item) => item.id === id);
            message.success(t("admin.sso.secretReset"));
            setSecretView({ clientId: client?.clientId ?? "", secret: data.clientSecret });
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const deleteMutation = useMutation({
        mutationFn: (id: string) => deleteAdminSsoClient(id),
        onSuccess: async () => {
            message.success(t("admin.sso.deleted"));
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const confirmReset = (row: AdminSsoClient) => {
        modal.confirm({
            title: t("admin.sso.resetConfirmTitle", { name: row.name }),
            content: t("admin.sso.resetConfirmContent"),
            okText: t("admin.sso.resetOk"),
            okButtonProps: { danger: true },
            cancelText: t("common.cancel"),
            onOk: () => resetMutation.mutateAsync(row.id),
        });
    };

    const columns: ColumnsType<AdminSsoClient> = [
        { title: t("admin.sso.columns.name"), dataIndex: "name", render: (value: string) => <span className="font-medium">{value}</span> },
        {
            title: t("admin.sso.columns.clientId"),
            dataIndex: "clientId",
            render: (value: string) => <span className="font-mono text-xs">{value}</span>,
        },
        {
            title: t("admin.sso.columns.product"),
            dataIndex: "productId",
            width: 120,
            render: (value: string) => <Tag>{t(`products.items.${value}.name`, { ns: "admin", defaultValue: value })}</Tag>,
        },
        {
            title: t("admin.sso.columns.redirectUris"),
            dataIndex: "redirectUris",
            width: 110,
            render: (_, row) => (
                <span title={row.redirectUris.join("\n")} className="font-mono text-xs">
                    {t("admin.sso.redirectUrisCount", { count: row.redirectUris.length })}
                </span>
            ),
        },
        {
            title: t("admin.sso.columns.enabled"),
            dataIndex: "enabled",
            width: 100,
            render: (value: boolean) => <Tag color={value ? "success" : "default"}>{value ? t("admin.sso.on") : t("admin.sso.off")}</Tag>,
        },
        { title: t("admin.sso.columns.createdAt"), dataIndex: "createdAt", width: 120, render: (value: string) => dayjs(value).format("YYYY-MM-DD") },
        { title: t("admin.sso.columns.updatedAt"), dataIndex: "updatedAt", width: 120, render: (value: string) => dayjs(value).format("YYYY-MM-DD") },
        {
            title: t("admin.sso.columns.actions"),
            key: "actions",
            width: 190,
            render: (_, row) =>
                canWrite ? (
                    <Space size={2}>
                        <Button size="small" type="link" className="!px-1" onClick={() => setEditing(row)}>
                            {t("admin.edit")}
                        </Button>
                        <Button size="small" type="link" className="!px-1" onClick={() => confirmReset(row)}>
                            {t("admin.sso.resetSecret")}
                        </Button>
                        <Popconfirm
                            title={t("admin.sso.deleteConfirm")}
                            description={t("admin.sso.deleteHint")}
                            onConfirm={() => deleteMutation.mutate(row.id)}
                        >
                            <Button size="small" type="link" danger className="!px-1">
                                {t("admin.delete")}
                            </Button>
                        </Popconfirm>
                    </Space>
                ) : null,
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
                <p className="text-sm text-stone-500 dark:text-stone-400">{t("admin.sso.hint")}</p>
                {canWrite ? (
                    <Button
                        type="primary"
                        onClick={() => {
                            setEditing(null);
                            setCreating(true);
                        }}
                    >
                        {t("admin.sso.create")}
                    </Button>
                ) : null}
            </div>
            {clientsQuery.isError ? (
                <QueryError error={clientsQuery.error} message={t("admin.sso.loadFailed")} onRetry={() => void clientsQuery.refetch()} />
            ) : (
                <Table<AdminSsoClient>
                    rowKey="id"
                    size="middle"
                    loading={clientsQuery.isPending}
                    columns={columns}
                    dataSource={clientsQuery.data?.items ?? []}
                    pagination={{ pageSize: 20, hideOnSinglePage: true }}
                />
            )}

            <ClientModal
                open={creating || !!editing}
                editing={editing}
                onClose={() => {
                    setCreating(false);
                    setEditing(null);
                }}
                onCreated={(client, clientSecret) => setSecretView({ clientId: client.clientId, secret: clientSecret })}
            />
            <SecretModal view={secretView} onClose={() => setSecretView(null)} />
        </div>
    );
}
