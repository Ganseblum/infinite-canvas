import { useEffect, useState, type ReactNode } from "react";
import { Alert, App, Button, Card, Checkbox, Col, Descriptions, Divider, Form, Input, InputNumber, Modal, Popconfirm, Row, Select, Space, Switch, Table, Tabs, Tag, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { ADMIN_BASE_URL } from "@/constant/runtime-config";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import { QueryError } from "@admin/components/query-error";
import { useConsoleAccess } from "@admin/hooks/use-console-access";
import { PERM } from "@admin/lib/admin-nav";
import {
    addAdmin,
    createAdminRole,
    deleteAdminRole,
    getRevenueStats,
    getSettings,
    listAdminCommunityReports,
    listAdminCommunityWorks,
    listAdminPermissions,
    listAdminRoles,
    listAdmins,
    listAuditLogs,
    patchAdminCommunityReport,
    patchAdminCommunityWork,
    removeAdmin,
    updateAdminRole,
    updateSettings,
    type AdminRole,
    type AdminSummary,
    type AuditLog,
    type CommunityAdminReport,
    type CommunityAdminWork,
    type SiteSettings,
} from "@admin/services/api/admin";

// 主站基址：作者主页等主站路由挂在主站域上，在 admin 域下用相对路径必然 404；而 ADMIN_BASE_URL
// 的语义是后台自己的域名，拿它拼主站路径只会落回后台域（todo 批次 4 决策项①）。统一改用运行期
// config.js 注入的 MAIN_SITE_BASE_URL；缺省时先退 ADMIN_BASE_URL（旧环境主站链接就是拿它拼的，
// 保持原行为），再退当前 origin（主站与后台同域部署、本地开发落在这里）。浏览器里 origin 恒有值，
// 所以下面直接拼绝对地址，不再需要「空值降级为纯文本」的分支。
const MAIN_SITE_BASE =
    (window.__RUNTIME_CONFIG__ as { MAIN_SITE_BASE_URL?: string } | undefined)?.MAIN_SITE_BASE_URL?.trim().replace(/\/+$/, "") ||
    ADMIN_BASE_URL.replace(/\/+$/, "") ||
    window.location.origin;

export default function AdminSystemPage() {
    const { t } = useTranslation();
    const access = useConsoleAccess();
    const permissions = access.phase === "admin" ? access.permissions : [];
    const has = (key: string) => permissions.includes(key);

    // tab 与权限点一一对应：没有权限的 tab 不渲染，避免点进去才吃 403。
    // 路由守卫保证这里至少有一个 tab 可用，一个都没有时返回 null 只是防御性兜底。
    const items: Array<{ key: string; label: ReactNode; children: ReactNode }> = [];
    if (has(PERM.settingsRead)) items.push({ key: "settings", label: <SettingsTabLabel />, children: <SettingsTab /> });
    if (has(PERM.rolesRead)) items.push({ key: "roles", label: <RolesTabLabel />, children: <RolesTab /> });
    if (has(PERM.communityRead)) items.push({ key: "community", label: <CommunityTabLabel />, children: <CommunityTab /> });
    if (has(PERM.auditRead)) items.push({ key: "audit", label: <AuditTabLabel />, children: <AuditTab /> });
    if (items.length === 0) return null;
    return <Tabs items={items} />;
}

function SettingsTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.settingsTab")}</>;
}
function RolesTabLabel() {
    const { t } = useTranslation();
    return <>{t("roles.tab", { ns: "admin" })}</>;
}
function CommunityTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.communityTab")}</>;
}
function AuditTabLabel() {
    const { t } = useTranslation();
    return <>{t("admin.system.auditTab")}</>;
}

// ===== 站点设置 =====

function SettingsTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const access = useConsoleAccess();
    const canWrite = access.phase === "admin" && access.permissions.includes(PERM.settingsWrite);
    const [form] = Form.useForm<SiteSettings>();

    const settingsQuery = useQuery({ queryKey: ["admin", "settings"], queryFn: ({ signal }) => getSettings(signal) });
    const revenueQuery = useQuery({ queryKey: ["admin", "revenue"], queryFn: ({ signal }) => getRevenueStats(signal) });

    useEffect(() => {
        if (settingsQuery.data) form.setFieldsValue(settingsQuery.data);
    }, [form, settingsQuery.data]);

    const saveMutation = useMutation({
        mutationFn: (values: Partial<SiteSettings>) => updateSettings(values),
        onSuccess: async () => {
            message.success(t("admin.system.saved"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "settings"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    if (settingsQuery.isError) {
        return <QueryError error={settingsQuery.error} message={t("admin.system.loadFailed")} onRetry={() => void settingsQuery.refetch()} />;
    }

    const revenue = revenueQuery.data;

    return (
        <div className="flex flex-col gap-4">
            {revenue ? (
                <Row gutter={[16, 16]}>
                    {[
                        { key: "revenueToday", value: formatMoney(revenue.revenueMicrosToday), suffix: t("admin.system.ordersToday", { count: revenue.ordersToday }) },
                        { key: "revenueWeek", value: formatMoney(revenue.revenueMicrosWeek), suffix: t("admin.system.ordersWeek", { count: revenue.ordersWeek }) },
                        { key: "revenueTotal", value: formatMoney(revenue.revenueMicrosTotal), suffix: t("admin.system.ordersTotal", { count: revenue.ordersTotal }) },
                        { key: "paidUsers", value: formatPoints(revenue.paidUsers), suffix: t("admin.system.conversion", { percent: (revenue.conversionRate * 100).toFixed(1) }) },
                    ].map((item) => (
                        <Col key={item.key} xs={12} lg={6}>
                            <Card size="small">
                                <div className="text-xs text-stone-500 dark:text-stone-400">{t(`admin.system.${item.key}`)}</div>
                                <div className="mt-1 text-xl font-semibold">{item.value}</div>
                                <div className="mt-1 text-xs text-stone-500 dark:text-stone-400">{item.suffix}</div>
                            </Card>
                        </Col>
                    ))}
                </Row>
            ) : null}

            <Form form={form} layout="vertical" className="max-w-3xl" onFinish={(values) => saveMutation.mutate(values)}>
                <Form.Item name="announcement" label={t("admin.system.fields.announcement")} extra={t("admin.system.fields.announcementHint")}>
                    <Input.TextArea rows={2} maxLength={500} />
                </Form.Item>
                <div className="grid grid-cols-2 gap-x-4">
                    <Form.Item name="registrationEnabled" label={t("admin.system.fields.registrationEnabled")} valuePropName="checked">
                        <Switch disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="maintenanceMode" label={t("admin.system.fields.maintenanceMode")} valuePropName="checked">
                        <Switch disabled={!canWrite} />
                    </Form.Item>
                </div>
                <Form.Item name="maintenanceNotice" label={t("admin.system.fields.maintenanceNotice")} extra={t("admin.system.fields.maintenanceNoticeHint")}>
                    <Input maxLength={200} disabled={!canWrite} />
                </Form.Item>
                <div className="grid grid-cols-2 gap-x-4">
                    <Form.Item name="communityEnabled" label={t("admin.system.fields.communityEnabled")} valuePropName="checked">
                        <Switch disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="checkinEnabled" label={t("admin.system.fields.checkinEnabled")} valuePropName="checked">
                        <Switch disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="checkinRewardMicros" label={t("admin.system.fields.checkinRewardMicros")} extra={t("admin.system.fields.grantedHint")}>
                        <InputNumber className="w-full" min={0} step={10000} precision={0} disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="inviteEnabled" label={t("admin.system.fields.inviteEnabled")} valuePropName="checked">
                        <Switch disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="inviteRewardMicros" label={t("admin.system.fields.inviteRewardMicros")} extra={t("admin.system.fields.grantedHint")}>
                        <InputNumber className="w-full" min={0} step={10000} precision={0} disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="inviteeRewardMicros" label={t("admin.system.fields.inviteeRewardMicros")} extra={t("admin.system.fields.grantedHint")}>
                        <InputNumber className="w-full" min={0} step={10000} precision={0} disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="maxUploadBytes" label={t("admin.system.fields.maxUploadBytes")} extra={t("admin.system.fields.maxUploadBytesHint")}>
                        <InputNumber className="w-full" min={0} step={1024 * 1024} precision={0} disabled={!canWrite} />
                    </Form.Item>
                    <Form.Item name="generationConcurrency" label={t("admin.system.fields.generationConcurrency")} extra={t("admin.system.fields.generationConcurrencyHint")}>
                        <InputNumber className="w-full" min={0} max={50} precision={0} disabled={!canWrite} />
                    </Form.Item>
                </div>
                <div className="flex items-center gap-3">
                    <Button type="primary" htmlType="submit" loading={saveMutation.isPending} disabled={!canWrite}>
                        {t("admin.save")}
                    </Button>
                    {/* 只有 settings.read 的角色是只读视角：按钮禁用并说明原因，而不是点下去才报 403。 */}
                    {!canWrite ? <span className="text-xs text-stone-500 dark:text-stone-400">{t("roles.readOnlyHint", { ns: "admin" })}</span> : null}
                </div>
            </Form>
        </div>
    );
}

// ===== 角色与成员管理 =====

type RoleFormValues = { key: string; name: string; description: string; permissions: string[] };

function RolesTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const access = useConsoleAccess();
    const canManage = access.phase === "admin" && access.permissions.includes(PERM.rolesManage);

    const [editing, setEditing] = useState<AdminRole | null>(null);
    const [creating, setCreating] = useState(false);
    const [form] = Form.useForm<RoleFormValues>();
    const modalOpen = creating || !!editing;

    const rolesQuery = useQuery({ queryKey: ["admin", "roles"], queryFn: ({ signal }) => listAdminRoles(signal) });
    // 权限目录只在弹窗打开时拉取（列表接口要求 roles.read，本 tab 已保证有）；它由后端代码注册表生成，界面只读。
    const permissionsQuery = useQuery({
        queryKey: ["admin", "permissions"],
        queryFn: ({ signal }) => listAdminPermissions(signal),
        enabled: modalOpen,
    });

    const invalidateRoles = async () => {
        await queryClient.invalidateQueries({ queryKey: ["admin", "roles"] });
        // 改到自己的角色时菜单要立刻跟着变；改别人则不必惊动 /admin/me。
        if (editing && access.phase === "admin" && access.role.key === editing.key) {
            await queryClient.invalidateQueries({ queryKey: ["admin", "me"] });
        }
    };

    const createMutation = useMutation({
        mutationFn: (values: RoleFormValues) => createAdminRole({ key: values.key.trim(), name: values.name.trim(), description: (values.description ?? "").trim() }),
        onSuccess: async () => {
            message.success(t("roles.created", { ns: "admin" }));
            setCreating(false);
            form.resetFields();
            await invalidateRoles();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const updateMutation = useMutation({
        mutationFn: (values: RoleFormValues) =>
            updateAdminRole(editing!.key, {
                name: values.name.trim(),
                description: (values.description ?? "").trim(),
                // 系统角色隐式拥有全部权限：不发 permissions 字段，服务端也拒绝改。
                ...(editing!.isSystem ? {} : { permissions: values.permissions ?? [] }),
            }),
        onSuccess: async () => {
            message.success(t("roles.updated", { ns: "admin" }));
            setEditing(null);
            form.resetFields();
            await invalidateRoles();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const deleteMutation = useMutation({
        mutationFn: (key: string) => deleteAdminRole(key),
        onSuccess: async () => {
            message.success(t("roles.deleted", { ns: "admin" }));
            await invalidateRoles();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const openCreate = () => {
        setEditing(null);
        setCreating(true);
        form.resetFields();
        form.setFieldsValue({ key: "", name: "", description: "", permissions: [] });
    };

    const openEdit = (role: AdminRole) => {
        setCreating(false);
        setEditing(role);
        form.resetFields();
        form.setFieldsValue({ key: role.key, name: role.name, description: role.description, permissions: role.permissions });
    };

    const columns: ColumnsType<AdminRole> = [
        {
            title: t("roles.columns.role", { ns: "admin" }),
            dataIndex: "name",
            render: (value: string, row) => (
                <div className="min-w-0">
                    <div className="truncate font-medium">{value}</div>
                    <div className="truncate font-mono text-xs text-stone-500 dark:text-stone-400">{row.key}</div>
                </div>
            ),
        },
        { title: t("roles.columns.description", { ns: "admin" }), dataIndex: "description", render: (value: string) => value || "—" },
        {
            title: t("roles.columns.type", { ns: "admin" }),
            dataIndex: "isSystem",
            width: 110,
            render: (value: boolean) => (value ? <Tag color="gold">{t("roles.system", { ns: "admin" })}</Tag> : <Tag>{t("roles.custom", { ns: "admin" })}</Tag>),
        },
        {
            title: t("roles.columns.permissions", { ns: "admin" }),
            dataIndex: "permissions",
            width: 110,
            render: (value: string[], row) => (row.isSystem ? t("roles.allPermissions", { ns: "admin" }) : value.length),
        },
        { title: t("roles.columns.members", { ns: "admin" }), dataIndex: "memberCount", width: 90, align: "right" },
        {
            title: t("roles.columns.actions", { ns: "admin" }),
            key: "actions",
            width: 160,
            render: (_, row) => (
                <Space size={2}>
                    <Button size="small" type="link" className="!px-1" disabled={!canManage} onClick={() => openEdit(row)}>
                        {row.isSystem ? t("roles.view", { ns: "admin" }) : t("roles.edit", { ns: "admin" })}
                    </Button>
                    <Tooltip title={row.isSystem ? t("roles.systemNoDelete", { ns: "admin" }) : row.memberCount > 0 ? t("roles.hasMembers", { ns: "admin" }) : ""}>
                        {/* 禁用态的按钮不接收鼠标事件，套一层 span 让禁用原因还能被 hover 出来。 */}
                        <span className="inline-flex">
                            <Popconfirm
                                title={t("roles.deleteConfirm", { ns: "admin", name: row.name })}
                                onConfirm={() => deleteMutation.mutate(row.key)}
                                disabled={!canManage || row.isSystem || row.memberCount > 0}
                            >
                                {/* 系统角色与仍有成员的角色不给删除入口；服务端也会拒，这里只是不给入口。 */}
                                <Button size="small" type="link" danger className="!px-1" disabled={!canManage || row.isSystem || row.memberCount > 0}>
                                    {t("roles.delete", { ns: "admin" })}
                                </Button>
                            </Popconfirm>
                        </span>
                    </Tooltip>
                </Space>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-6">
            <section className="flex flex-col gap-4">
                <div className="flex items-center justify-between">
                    <p className="text-sm text-stone-500 dark:text-stone-400">{t("roles.hint", { ns: "admin" })}</p>
                    <Button type="primary" disabled={!canManage} onClick={openCreate}>
                        {t("roles.create", { ns: "admin" })}
                    </Button>
                </div>
                {rolesQuery.isError ? (
                    <QueryError error={rolesQuery.error} message={t("admin.system.loadFailed")} onRetry={() => void rolesQuery.refetch()} />
                ) : (
                    <Table<AdminRole> rowKey="key" size="middle" loading={rolesQuery.isPending} columns={columns} dataSource={rolesQuery.data?.items ?? []} pagination={false} />
                )}
            </section>

            <Divider className="!my-0" />

            <MembersSection canManage={canManage} />

            <Modal
                open={modalOpen}
                title={creating ? t("roles.createTitle", { ns: "admin" }) : t("roles.editTitle", { ns: "admin", name: editing?.name ?? "" })}
                okText={t("admin.save")}
                cancelText={t("common.cancel")}
                confirmLoading={createMutation.isPending || updateMutation.isPending}
                // 只有 roles.read 的角色是只读视角：不显示保存按钮，字段全部禁用。
                footer={canManage ? undefined : <Button onClick={() => { setCreating(false); setEditing(null); }}>{t("admin.close")}</Button>}
                onCancel={() => {
                    setCreating(false);
                    setEditing(null);
                }}
                onOk={async () => {
                    const values = await form.validateFields();
                    if (creating) await createMutation.mutateAsync(values);
                    else await updateMutation.mutateAsync(values);
                }}
            >
                <Form form={form} layout="vertical">
                    <Form.Item
                        name="key"
                        label={t("roles.fields.key", { ns: "admin" })}
                        extra={creating ? t("roles.fields.keyHint", { ns: "admin" }) : t("roles.fields.keyLocked", { ns: "admin" })}
                        rules={
                            creating
                                ? [
                                      { required: true, message: t("roles.fields.keyRequired", { ns: "admin" }) },
                                      { pattern: /^[a-z][a-z0-9._-]{1,63}$/, message: t("roles.fields.keyPattern", { ns: "admin" }) },
                                  ]
                                : []
                        }
                    >
                        <Input disabled={!creating} />
                    </Form.Item>
                    <Form.Item name="name" label={t("roles.fields.name", { ns: "admin" })} rules={[{ required: true, message: t("roles.fields.nameRequired", { ns: "admin" }) }]}>
                        <Input maxLength={64} disabled={!canManage} />
                    </Form.Item>
                    <Form.Item name="description" label={t("roles.fields.description", { ns: "admin" })}>
                        <Input.TextArea rows={2} maxLength={200} disabled={!canManage} />
                    </Form.Item>
                    <Form.Item name="permissions" label={t("roles.fields.permissions", { ns: "admin" })} extra={t("roles.fields.permissionsHint", { ns: "admin" })}>
                        <Checkbox.Group className="w-full" disabled={!canManage || !!editing?.isSystem}>
                            <div className="flex flex-col gap-3">
                                {permissionsQuery.data?.items.map((group) => (
                                    <div key={group.module}>
                                        <div className="mb-1 text-xs font-medium text-stone-500 dark:text-stone-400">{group.moduleName}</div>
                                        <div className="flex flex-col gap-1">
                                            {group.permissions.map((permission) => (
                                                <Checkbox key={permission.key} value={permission.key}>
                                                    <span>{permission.name}</span>
                                                    <span className="ml-2 text-xs text-stone-500 dark:text-stone-400">{permission.description}</span>
                                                </Checkbox>
                                            ))}
                                        </div>
                                    </div>
                                ))}
                            </div>
                        </Checkbox.Group>
                    </Form.Item>
                    {editing?.isSystem ? <Alert type="info" showIcon message={t("roles.systemNoEdit", { ns: "admin" })} /> : null}
                </Form>
            </Modal>
        </div>
    );
}

// 系统角色成员：沿用过渡期的 /admin/admins 接口，只增删系统角色成员；其他用户的角色在用户页调整。
function MembersSection({ canManage }: { canManage: boolean }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [open, setOpen] = useState(false);
    const [email, setEmail] = useState("");

    const membersQuery = useQuery({ queryKey: ["admin", "admins"], queryFn: ({ signal }) => listAdmins(signal) });
    const addMutation = useMutation({
        mutationFn: (value: string) => addAdmin(value),
        onSuccess: async () => {
            message.success(t("admin.system.adminAdded"));
            setOpen(false);
            setEmail("");
            await queryClient.invalidateQueries({ queryKey: ["admin", "admins"] });
            await queryClient.invalidateQueries({ queryKey: ["admin", "roles"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });
    const removeMutation = useMutation({
        mutationFn: (id: string) => removeAdmin(id),
        onSuccess: async () => {
            message.success(t("admin.system.adminRemoved"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "admins"] });
            await queryClient.invalidateQueries({ queryKey: ["admin", "roles"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const columns: ColumnsType<AdminSummary> = [
        { title: t("admin.system.adminColumns.email"), dataIndex: "email" },
        { title: t("admin.system.adminColumns.username"), dataIndex: "username" },
        {
            title: t("admin.system.adminColumns.lastLogin"),
            dataIndex: "lastLoginAt",
            render: (value: string | null) => (value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—"),
        },
        {
            title: t("admin.system.adminColumns.createdAt"),
            dataIndex: "createdAt",
            render: (value: string) => dayjs(value).format("YYYY-MM-DD"),
        },
        {
            title: t("admin.system.adminColumns.actions"),
            key: "actions",
            width: 120,
            render: (_, row) => (
                <Popconfirm title={t("admin.system.removeAdminConfirm")} onConfirm={() => removeMutation.mutate(row.id)} disabled={!canManage}>
                    <Button size="small" type="link" danger className="!px-1" disabled={!canManage}>
                        {t("admin.system.removeAdmin")}
                    </Button>
                </Popconfirm>
            ),
        },
    ];

    return (
        <section className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
                <div>
                    <h3 className="text-sm font-medium">{t("roles.membersTitle", { ns: "admin" })}</h3>
                    <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("admin.system.adminsHint")}</p>
                </div>
                <Button type="primary" disabled={!canManage} onClick={() => setOpen(true)}>
                    {t("admin.system.addAdmin")}
                </Button>
            </div>
            {membersQuery.isError ? (
                <QueryError error={membersQuery.error} message={t("admin.system.loadFailed")} onRetry={() => void membersQuery.refetch()} />
            ) : (
                <Table<AdminSummary> rowKey="id" size="middle" loading={membersQuery.isPending} columns={columns} dataSource={membersQuery.data?.items ?? []} pagination={false} />
            )}
            <Modal
                open={open}
                title={t("admin.system.addAdminTitle")}
                okText={t("admin.system.addAdmin")}
                cancelText={t("common.cancel")}
                confirmLoading={addMutation.isPending}
                onCancel={() => setOpen(false)}
                onOk={() => {
                    if (!email.trim()) {
                        message.warning(t("admin.system.adminEmailRequired"));
                        return;
                    }
                    addMutation.mutate(email.trim());
                }}
            >
                <Alert className="mb-4" type="info" showIcon message={t("admin.system.addAdminHint")} />
                <Input placeholder="admin@example.com" value={email} onChange={(event) => setEmail(event.target.value)} />
            </Modal>
        </section>
    );
}

// ===== 社区管理 =====

function CommunityTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const access = useConsoleAccess();
    const canWrite = access.phase === "admin" && access.permissions.includes(PERM.communityWrite);
    const [page, setPage] = useState(1);
    const [status, setStatus] = useState<string | undefined>("published");

    const reportsQuery = useQuery({
        queryKey: ["admin", "community", "reports"],
        queryFn: ({ signal }) => listAdminCommunityReports({ size: 20, status: "pending" }, signal),
    });
    const worksQuery = useQuery({
        queryKey: ["admin", "community", "works", page, status],
        queryFn: ({ signal }) => listAdminCommunityWorks({ page, size: 20, status }, signal),
        placeholderData: (previous) => previous,
    });

    const patchWorkMutation = useMutation({
        mutationFn: ({ id, next }: { id: string; next: "published" | "hidden" | "removed" }) => patchAdminCommunityWork(id, next),
        onSuccess: async () => {
            message.success(t("admin.system.workUpdated"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "community"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });
    const patchReportMutation = useMutation({
        mutationFn: ({ id, next, removeWork }: { id: string; next: "handled" | "dismissed"; removeWork?: boolean }) =>
            patchAdminCommunityReport(id, { status: next, removeWork }),
        onSuccess: async () => {
            message.success(t("admin.system.reportHandled"));
            await queryClient.invalidateQueries({ queryKey: ["admin", "community"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const reportColumns: ColumnsType<CommunityAdminReport> = [
        { title: t("admin.system.reportColumns.work"), dataIndex: "workTitle" },
        { title: t("admin.system.reportColumns.reason"), dataIndex: "reason" },
        {
            title: t("admin.system.reportColumns.createdAt"),
            dataIndex: "createdAt",
            width: 160,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD HH:mm"),
        },
        {
            title: t("admin.system.reportColumns.actions"),
            key: "actions",
            width: 220,
            render: (_, row) => (
                <Space size={2}>
                    <Popconfirm title={t("admin.system.dismissConfirm")} onConfirm={() => patchReportMutation.mutate({ id: row.id, next: "dismissed" })} disabled={!canWrite}>
                        <Button size="small" type="link" className="!px-1" disabled={!canWrite}>
                            {t("admin.system.dismiss")}
                        </Button>
                    </Popconfirm>
                    <Popconfirm title={t("admin.system.removeWorkConfirm")} onConfirm={() => patchReportMutation.mutate({ id: row.id, next: "handled", removeWork: true })} disabled={!canWrite}>
                        <Button size="small" type="link" danger className="!px-1" disabled={!canWrite}>
                            {t("admin.system.handleAndRemove")}
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    const workColumns: ColumnsType<CommunityAdminWork> = [
        {
            title: t("admin.system.workColumns.work"),
            dataIndex: "title",
            render: (value: string, row) => (
                <div className="min-w-0">
                    <div className="truncate font-medium">{value}</div>
                    {/* 作者主页是主站路由：一律拼主站基址的绝对地址（MAIN_SITE_BASE 恒非空，见文件顶部说明）。 */}
                    <a
                        href={`${MAIN_SITE_BASE}/community/users/${row.userId}`}
                        className="block truncate text-xs text-stone-500 hover:underline dark:text-stone-400"
                    >
                        {row.userId}
                    </a>
                </div>
            ),
        },
        {
            title: t("admin.system.workColumns.status"),
            dataIndex: "status",
            width: 110,
            render: (value: string) => <Tag>{t(`admin.system.workStatuses.${value}`)}</Tag>,
        },
        { title: t("admin.system.workColumns.likes"), dataIndex: "likeCount", width: 90, align: "right" },
        { title: t("admin.system.workColumns.remix"), dataIndex: "remixCount", width: 90, align: "right" },
        {
            title: t("admin.system.workColumns.reports"),
            dataIndex: "reportCount",
            width: 90,
            align: "right",
            render: (value: number) => (value > 0 ? <Tag color="warning">{value}</Tag> : 0),
        },
        {
            title: t("admin.system.workColumns.actions"),
            key: "actions",
            width: 200,
            render: (_, row) => (
                <Space size={2}>
                    {row.status !== "removed" ? (
                        <Popconfirm title={t("admin.system.removeWorkConfirm")} onConfirm={() => patchWorkMutation.mutate({ id: row.id, next: "removed" })} disabled={!canWrite}>
                            <Button size="small" type="link" danger className="!px-1" disabled={!canWrite}>
                                {t("admin.system.removeWork")}
                            </Button>
                        </Popconfirm>
                    ) : null}
                    {row.status === "removed" ? (
                        <Button size="small" type="link" className="!px-1" disabled={!canWrite} onClick={() => patchWorkMutation.mutate({ id: row.id, next: "published" })}>
                            {t("admin.system.restoreWork")}
                        </Button>
                    ) : null}
                </Space>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-6">
            <section>
                <h3 className="mb-3 text-sm font-medium">{t("admin.system.pendingReports")}</h3>
                {reportsQuery.isError ? (
                    <QueryError error={reportsQuery.error} message={t("admin.system.loadFailed")} onRetry={() => void reportsQuery.refetch()} />
                ) : (
                    <Table<CommunityAdminReport>
                        rowKey="id"
                        size="small"
                        loading={reportsQuery.isPending}
                        columns={reportColumns}
                        dataSource={reportsQuery.data?.items ?? []}
                        pagination={false}
                        locale={{ emptyText: t("admin.system.noReports") }}
                    />
                )}
            </section>
            <section>
                <div className="mb-3 flex items-center justify-between">
                    <h3 className="text-sm font-medium">{t("admin.system.worksTitle")}</h3>
                    <Select
                        allowClear
                        className="w-36"
                        placeholder={t("admin.system.workStatusFilter")}
                        value={status}
                        onChange={(value) => {
                            setStatus(value);
                            setPage(1);
                        }}
                        options={(["published", "hidden", "removed"] as const).map((value) => ({ value, label: t(`admin.system.workStatuses.${value}`) }))}
                    />
                </div>
                {worksQuery.isError ? (
                    <QueryError error={worksQuery.error} message={t("admin.system.loadFailed")} onRetry={() => void worksQuery.refetch()} />
                ) : (
                    <Table<CommunityAdminWork>
                        rowKey="id"
                        size="small"
                        loading={worksQuery.isPending}
                        columns={workColumns}
                        dataSource={worksQuery.data?.items ?? []}
                        pagination={{
                            current: page,
                            pageSize: 20,
                            total: worksQuery.data?.total ?? 0,
                            onChange: (nextPage) => setPage(nextPage),
                        }}
                    />
                )}
            </section>
            <Descriptions size="small" column={1}>
                <Descriptions.Item label={t("admin.system.communityHintLabel")}>{t("admin.system.communityHint")}</Descriptions.Item>
            </Descriptions>
        </div>
    );
}

// ===== 审计日志 =====

function AuditTab() {
    const { t } = useTranslation();
    const [page, setPage] = useState(1);
    const [action, setAction] = useState<string | undefined>();

    const logsQuery = useQuery({
        queryKey: ["admin", "audit", page, action],
        queryFn: ({ signal }) => listAuditLogs({ page, size: 20, action }, signal),
        placeholderData: (previous) => previous,
    });

    const columns: ColumnsType<AuditLog> = [
        {
            title: t("admin.system.auditColumns.time"),
            dataIndex: "createdAt",
            width: 170,
            render: (value: string) => dayjs(value).format("YYYY-MM-DD HH:mm:ss"),
        },
        { title: t("admin.system.auditColumns.action"), dataIndex: "action", width: 200, render: (value: string) => <code className="text-xs">{value}</code> },
        { title: t("admin.system.auditColumns.target"), dataIndex: "targetId", width: 260, render: (value: string, row) => <span className="font-mono text-xs">{row.targetType}·{value.slice(0, 8)}…</span> },
        { title: t("admin.system.auditColumns.actor"), dataIndex: "actorUserId", width: 260, render: (value: string) => <span className="font-mono text-xs">{value}</span> },
        { title: t("admin.system.auditColumns.reason"), dataIndex: "reason", render: (value?: string) => value || "—" },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center gap-3">
                <Select
                    allowClear
                    className="w-64"
                    placeholder={t("admin.system.auditActionFilter")}
                    value={action}
                    onChange={(value) => {
                        setAction(value);
                        setPage(1);
                    }}
                    options={[
                        "settings.update",
                        "admin.add",
                        "admin.remove",
                        "user.status",
                        "user.credits_adjust",
                        "user.password_reset",
                        "user.role",
                        "role.create",
                        "role.update",
                        "role.permissions",
                        "role.delete",
                        "model.create",
                        "model.update",
                        "model.delete",
                        "promotion.create",
                        "promotion.update",
                        "package.create",
                        "package.update",
                        "channel.create",
                        "channel.update",
                        "channel.delete",
                        "moderation.review",
                        "moderation.compensate",
                        "community.work_status",
                        "community.report",
                    ].map((value) => ({ value, label: value }))}
                />
            </div>
            {logsQuery.isError ? (
                <QueryError error={logsQuery.error} message={t("admin.system.loadFailed")} onRetry={() => void logsQuery.refetch()} />
            ) : (
                <Table<AuditLog>
                    rowKey="id"
                    size="small"
                    loading={logsQuery.isPending}
                    columns={columns}
                    dataSource={logsQuery.data?.items ?? []}
                    scroll={{ x: 1100 }}
                    pagination={{
                        current: page,
                        pageSize: 20,
                        total: logsQuery.data?.total ?? 0,
                        onChange: (nextPage) => setPage(nextPage),
                    }}
                />
            )}
        </div>
    );
}

// ===== 总览页复用的收入卡片 =====

export function RevenueCards() {
    const { t } = useTranslation();
    const revenueQuery = useQuery({ queryKey: ["admin", "revenue"], queryFn: ({ signal }) => getRevenueStats(signal), refetchOnWindowFocus: true });
    const revenue = revenueQuery.data;
    if (!revenue) return null;
    return (
        <Row gutter={[16, 16]}>
            {[
                { key: "revenueToday", value: formatMoney(revenue.revenueMicrosToday), suffix: t("admin.system.ordersToday", { count: revenue.ordersToday }) },
                { key: "revenueWeek", value: formatMoney(revenue.revenueMicrosWeek), suffix: t("admin.system.ordersWeek", { count: revenue.ordersWeek }) },
                { key: "revenueTotal", value: formatMoney(revenue.revenueMicrosTotal), suffix: t("admin.system.ordersTotal", { count: revenue.ordersTotal }) },
                { key: "paidUsers", value: formatPoints(revenue.paidUsers), suffix: t("admin.system.conversion", { percent: (revenue.conversionRate * 100).toFixed(1) }) },
            ].map((item) => (
                <Col key={item.key} xs={12} lg={6}>
                    <Card size="small">
                        <div className="text-xs text-stone-500 dark:text-stone-400">{t(`admin.system.${item.key}`)}</div>
                        <div className="mt-1 text-xl font-semibold">{item.value}</div>
                        <div className="mt-1 text-xs text-stone-500 dark:text-stone-400">{item.suffix}</div>
                    </Card>
                </Col>
            ))}
        </Row>
    );
}
