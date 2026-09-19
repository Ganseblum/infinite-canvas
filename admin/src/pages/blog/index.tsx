import { useMemo, useState } from "react";
import { App, Button, Checkbox, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Switch, Table, Tabs, Tag, Typography } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import {
    createAdminBlogPost,
    createAdminBlogTopic,
    deleteAdminBlogComment,
    deleteAdminBlogPost,
    deleteAdminBlogTopic,
    getAdminBlogPost,
    hideAdminBlogComment,
    listAdminBlogComments,
    listAdminBlogPosts,
    listAdminBlogTopics,
    pinAdminBlogComment,
    previewAdminBlogPost,
    publishAdminBlogPost,
    restoreAdminBlogComment,
    unpublishAdminBlogPost,
    updateAdminBlogPost,
    updateAdminBlogTopic,
    type AdminBlogPost,
    type AdminBlogTopic,
} from "@admin/services/api/admin";

const PAGE_SIZE = 10;

export default function AdminBlogPage() {
    const { t } = useTranslation();
    return (
        <main className="min-h-full bg-background">
            <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6">
                <h1 className="text-2xl font-semibold">{t("admin.blog.title")}</h1>
                <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("admin.blog.description")}</p>
                <Tabs
                    className="mt-4"
                    items={[
                        { key: "posts", label: t("admin.blog.postsTab"), children: <PostsTab /> },
                        { key: "topics", label: t("admin.blog.topicsTab"), children: <TopicsTab /> },
                        { key: "comments", label: t("admin.blog.commentsTab"), children: <CommentsTab /> },
                    ]}
                />
            </div>
        </main>
    );
}

// ===== 文章列表与编辑器 =====

type Editing = { id: string | null; open: boolean };

function PostsTab() {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [page, setPage] = useState(1);
    const [status, setStatus] = useState<string>("all");
    const [q, setQ] = useState("");
    const [editing, setEditing] = useState<Editing | null>(null);

    const listQuery = useQuery({
        queryKey: ["admin", "blog", "posts", page, status, q],
        queryFn: ({ signal }) =>
            listAdminBlogPosts({ page, size: PAGE_SIZE, status: status === "all" ? undefined : status, q: q || undefined }, signal),
    });

    const publishMut = useMutation({
        mutationFn: ({ id, publish }: { id: string; publish: boolean }) =>
            publish ? publishAdminBlogPost(id) : unpublishAdminBlogPost(id),
        onSuccess: (_data, vars) => {
            message.success(t(vars.publish ? "admin.blog.published" : "admin.blog.unpublished"));
            void queryClient.invalidateQueries({ queryKey: ["admin", "blog"] });
        },
        onError: (err) => message.error(getApiErrorMessage(err)),
    });

    const deleteMut = useMutation({
        mutationFn: (id: string) => deleteAdminBlogPost(id),
        onSuccess: () => {
            message.success(t("admin.blog.deleted"));
            void queryClient.invalidateQueries({ queryKey: ["admin", "blog"] });
        },
        onError: (err) => message.error(getApiErrorMessage(err)),
    });

    if (editing) {
        return <BlogEditor id={editing.id} onDone={() => setEditing(null)} />;
    }

    return (
        <div>
            <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                <Space wrap>
                    <Segmented
                        value={status}
                        onChange={(v) => { setStatus(v as string); setPage(1); }}
                        options={[
                            { label: t("admin.blog.filter.all"), value: "all" },
                            { label: t("admin.blog.filter.published"), value: "published" },
                            { label: t("admin.blog.filter.draft"), value: "draft" },
                        ]}
                    />
                    <Input.Search
                        allowClear
                        placeholder={t("admin.blog.searchPlaceholder")}
                        className="w-56"
                        onSearch={(v) => { setQ(v); setPage(1); }}
                    />
                </Space>
                <Button type="primary" onClick={() => setEditing({ id: null, open: true })}>
                    {t("admin.blog.newPost")}
                </Button>
            </div>
            <Table
                rowKey="id"
                loading={listQuery.isLoading}
                dataSource={listQuery.data?.posts ?? []}
                pagination={{
                    current: page, pageSize: PAGE_SIZE, total: listQuery.data?.total ?? 0,
                    onChange: setPage, showSizeChanger: false,
                }}
                columns={[
                    {
                        title: t("admin.blog.columns.title"), dataIndex: "title",
                        render: (_: string, row: AdminBlogPost) => (
                            <div>
                                <div className="font-medium">
                                    {row.isPinned ? <Tag color="orange" className="mr-1">{t("admin.blog.pinned")}</Tag> : null}
                                    {row.title}
                                </div>
                                <div className="text-xs text-stone-400">/{row.slug}</div>
                            </div>
                        ),
                    },
                    { title: t("admin.blog.columns.topic"), dataIndex: "topicName", width: 120 },
                    {
                        title: t("admin.blog.columns.vol"), dataIndex: "volNo", width: 90,
                        render: (v: number) => (v > 0 ? `VOL.${String(v).padStart(3, "0")}` : "—"),
                    },
                    {
                        title: t("admin.blog.columns.status"), dataIndex: "status", width: 100,
                        render: (s: string) =>
                            s === "published"
                                ? <Tag color="green">{t("admin.blog.statuses.published")}</Tag>
                                : <Tag>{t("admin.blog.statuses.draft")}</Tag>,
                    },
                    { title: t("admin.blog.columns.words"), dataIndex: "wordCount", width: 90, render: (v: number) => v.toLocaleString() },
                    {
                        title: t("admin.blog.columns.updatedAt"), dataIndex: "updatedAt", width: 160,
                        render: (v: string) => new Date(v).toLocaleString(),
                    },
                    {
                        title: t("admin.blog.columns.actions"), key: "actions", width: 210,
                        render: (_: unknown, row: AdminBlogPost) => (
                            <Space>
                                <Button size="small" onClick={() => setEditing({ id: row.id, open: true })}>
                                    {t("admin.blog.edit")}
                                </Button>
                                {row.status === "published" ? (
                                    <Button size="small" onClick={() => publishMut.mutate({ id: row.id, publish: false })}>
                                        {t("admin.blog.unpublish")}
                                    </Button>
                                ) : (
                                    <Button size="small" type="primary" onClick={() => publishMut.mutate({ id: row.id, publish: true })}>
                                        {t("admin.blog.publish")}
                                    </Button>
                                )}
                                <Popconfirm
                                    title={t("admin.blog.deleteConfirm")}
                                    description={t("admin.blog.deleteHint")}
                                    onConfirm={() => deleteMut.mutate(row.id)}
                                >
                                    <Button size="small" danger>{t("admin.blog.delete")}</Button>
                                </Popconfirm>
                            </Space>
                        ),
                    },
                ]}
            />
        </div>
    );
}

function BlogEditor({ id, onDone }: { id: string | null; onDone: () => void }) {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [form] = Form.useForm();
    const [contentMd, setContentMd] = useState("");
    const [previewHtml, setPreviewHtml] = useState("");

    const detailQuery = useQuery({
        queryKey: ["admin", "blog", "post", id],
        queryFn: ({ signal }) => getAdminBlogPost(id!, signal),
        enabled: !!id,
    });
    // 编辑态：详情回来后回填一次（Form 已挂载）。
    if (id && detailQuery.data && form.getFieldValue("title") == null) {
        form.setFieldsValue({ ...detailQuery.data.post, tags: (detailQuery.data.post.tags ?? []).join(", ") });
        setContentMd(detailQuery.data.contentMd);
    }

    const topicsQuery = useQuery({
        queryKey: ["admin", "blog", "topics"],
        queryFn: ({ signal }) => listAdminBlogTopics(signal),
    });

    const previewMut = useMutation({
        mutationFn: (md: string) => previewAdminBlogPost(md),
        onSuccess: (data) => setPreviewHtml(data.html),
        onError: (err) => message.error(getApiErrorMessage(err)),
    });

    const saveMut = useMutation({
        mutationFn: async ({ publish }: { publish: boolean }) => {
            const values = await form.validateFields();
            const input = {
                slug: values.slug, title: values.title, summary: values.summary ?? "",
                topicId: values.topicId, volNo: values.volNo ?? 0,
                coverSeed: values.coverSeed ?? "", isAigcCover: !!values.isAigcCover,
                isPinned: !!values.isPinned, originUrl: values.originUrl ?? "",
                tags: values.tags ?? "", contentMd,
            };
            const saved = id ? await updateAdminBlogPost(id, input) : await createAdminBlogPost(input);
            if (publish) {
                const targetId = id ?? saved.post.id;
                return await publishAdminBlogPost(targetId);
            }
            return saved;
        },
        onSuccess: (_data, vars) => {
            message.success(t(vars.publish ? "admin.blog.publishSaved" : "admin.blog.saveSaved"));
            void queryClient.invalidateQueries({ queryKey: ["admin", "blog"] });
            onDone();
        },
        onError: (err) => message.error(getApiErrorMessage(err)),
    });

    const topicOptions = useMemo(
        () => (topicsQuery.data?.topics ?? []).map((tp) => ({ value: tp.id, label: `${tp.name}（${tp.slug}）` })),
        [topicsQuery.data],
    );

    return (
        <div className="grid gap-4 lg:grid-cols-[380px_1fr]">
            <div className="space-y-4">
                <Button onClick={onDone}>← {t("admin.blog.backToList")}</Button>
                <Form form={form} layout="vertical" className="rounded-xl border border-stone-200 p-4 dark:border-stone-800">
                    <Form.Item name="title" label={t("admin.blog.fields.title")} rules={[{ required: true }]}>
                        <Input />
                    </Form.Item>
                    <Form.Item name="slug" label={t("admin.blog.fields.slug")} rules={[{ required: true }]}
                        extra={t("admin.blog.fields.slugHint")}>
                        <Input />
                    </Form.Item>
                    <div className="grid grid-cols-2 gap-3">
                        <Form.Item name="topicId" label={t("admin.blog.fields.topic")} rules={[{ required: true }]}>
                            <Select options={topicOptions} />
                        </Form.Item>
                        <Form.Item name="volNo" label={t("admin.blog.fields.vol")} initialValue={0}>
                            <InputNumber min={0} className="w-full" />
                        </Form.Item>
                    </div>
                    <Form.Item name="summary" label={t("admin.blog.fields.summary")}
                        extra={t("admin.blog.fields.summaryHint")}>
                        <Input.TextArea rows={2} />
                    </Form.Item>
                    <div className="grid grid-cols-2 gap-3 items-end">
                        <Form.Item name="coverSeed" label={t("admin.blog.fields.coverSeed")}>
                            <Input />
                        </Form.Item>
                        <Form.Item name="isAigcCover" label={t("admin.blog.fields.isAigcCover")} valuePropName="checked">
                            <Checkbox>{t("admin.blog.fields.aigcBadge")}</Checkbox>
                        </Form.Item>
                    </div>
                    <Form.Item name="originUrl" label={t("admin.blog.fields.originUrl")}
                        extra={t("admin.blog.fields.originHint")}>
                        <Input />
                    </Form.Item>
                    <Form.Item name="tags" label={t("admin.blog.fields.tags")}>
                        <Input />
                    </Form.Item>
                    <Form.Item name="isPinned" label={t("admin.blog.fields.isPinned")} valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </Form>
            </div>
            <div className="rounded-xl border border-stone-200 dark:border-stone-800">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-stone-200 px-4 py-2.5 dark:border-stone-800">
                    <span className="text-sm font-medium">{t("admin.blog.contentTitle")}</span>
                    <Space>
                        <Button size="small" loading={previewMut.isPending} onClick={() => previewMut.mutate(contentMd)}>
                            {t("admin.blog.refreshPreview")}
                        </Button>
                        <Button size="small" onClick={() => saveMut.mutate({ publish: false })} loading={saveMut.isPending}>
                            {t("admin.blog.saveDraft")}
                        </Button>
                        <Button size="small" type="primary" onClick={() => saveMut.mutate({ publish: true })} loading={saveMut.isPending}>
                            {t("admin.blog.publishNow")}
                        </Button>
                    </Space>
                </div>
                <div className="grid min-h-[480px] lg:grid-cols-2 lg:divide-x lg:divide-stone-200 dark:lg:divide-stone-800">
                    <Input.TextArea
                        value={contentMd}
                        onChange={(ev) => setContentMd(ev.target.value)}
                        className="h-full min-h-[440px] rounded-none border-0 font-mono text-[13px] leading-7"
                        spellCheck={false}
                    />
                    <div className="hidden overflow-y-auto p-5 lg:block">
                        {previewHtml
                            ? <div className="blog-preview" dangerouslySetInnerHTML={{ __html: previewHtml }} />
                            : <Typography.Text type="secondary">{t("admin.blog.previewHint")}</Typography.Text>}
                    </div>
                </div>
            </div>
        </div>
    );
}

// ===== 栏目管理 =====

function TopicsTab() {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [modalOpen, setModalOpen] = useState(false);
    const [editing, setEditing] = useState<{ id: number; slug: string; name: string; enName: string; description: string; sort: number } | null>(null);
    const [form] = Form.useForm();

    const listQuery = useQuery({
        queryKey: ["admin", "blog", "topics"],
        queryFn: ({ signal }) => listAdminBlogTopics(signal),
    });

    const saveMut = useMutation({
        mutationFn: async () => {
            const values = await form.validateFields();
            return editing ? updateAdminBlogTopic(editing.id, values) : createAdminBlogTopic(values);
        },
        onSuccess: () => {
            message.success(t("admin.blog.topicSaved"));
            setModalOpen(false);
            void queryClient.invalidateQueries({ queryKey: ["admin", "blog"] });
        },
        onError: (err) => message.error(getApiErrorMessage(err)),
    });

    const deleteMut = useMutation({
        mutationFn: (id: number) => deleteAdminBlogTopic(id),
        onSuccess: () => {
            message.success(t("admin.blog.topicDeleted"));
            void queryClient.invalidateQueries({ queryKey: ["admin", "blog"] });
        },
        onError: (err) => message.error(getApiErrorMessage(err)),
    });

    const openModal = (row: AdminBlogTopic | null) => {
        setEditing(row ? { id: row.id, slug: row.slug, name: row.name, enName: row.enName, description: row.description, sort: row.sort } : null);
        form.setFieldsValue(row ?? { slug: "", name: "", enName: "", description: "", sort: 0 });
        setModalOpen(true);
    };

    return (
        <div>
            <div className="mb-4 flex justify-end">
                <Button type="primary" onClick={() => openModal(null)}>{t("admin.blog.newTopic")}</Button>
            </div>
            <Table
                rowKey="id"
                loading={listQuery.isLoading}
                dataSource={listQuery.data?.topics ?? []}
                pagination={false}
                columns={[
                    { title: t("admin.blog.topicColumns.name"), dataIndex: "name" },
                    { title: "slug", dataIndex: "slug", className: "font-mono text-xs" },
                    { title: t("admin.blog.topicColumns.enName"), dataIndex: "enName" },
                    { title: t("admin.blog.topicColumns.postCount"), dataIndex: "postCount", width: 100 },
                    { title: t("admin.blog.topicColumns.sort"), dataIndex: "sort", width: 80 },
                    {
                        title: t("admin.blog.columns.actions"), key: "actions", width: 160,
                        render: (_: unknown, row: AdminBlogTopic) => (
                            <Space>
                                <Button size="small" onClick={() => openModal(row)}>{t("admin.blog.edit")}</Button>
                                <Popconfirm
                                    title={t("admin.blog.topicDeleteConfirm")}
                                    onConfirm={() => deleteMut.mutate(row.id)}
                                    disabled={row.postCount > 0}
                                >
                                    <Button size="small" danger disabled={row.postCount > 0}>
                                        {t("admin.blog.delete")}
                                    </Button>
                                </Popconfirm>
                            </Space>
                        ),
                    },
                ]}
            />
            <p className="mt-3 text-xs text-stone-400">{t("admin.blog.topicDeleteHint")}</p>
            <Modal
                title={editing ? t("admin.blog.editTopic") : t("admin.blog.newTopic")}
                open={modalOpen}
                onCancel={() => setModalOpen(false)}
                onOk={() => saveMut.mutate()}
                confirmLoading={saveMut.isPending}
                destroyOnHidden
            >
                <Form form={form} layout="vertical">
                    <Form.Item name="name" label={t("admin.blog.topicColumns.name")} rules={[{ required: true }]}>
                        <Input />
                    </Form.Item>
                    <Form.Item name="slug" label="slug" rules={[{ required: true }]}>
                        <Input />
                    </Form.Item>
                    <Form.Item name="enName" label={t("admin.blog.topicColumns.enName")}>
                        <Input />
                    </Form.Item>
                    <Form.Item name="description" label={t("admin.blog.topicColumns.description")}>
                        <Input.TextArea rows={2} />
                    </Form.Item>
                    <Form.Item name="sort" label={t("admin.blog.topicColumns.sort")} initialValue={0}>
                        <InputNumber min={0} className="w-full" />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}

// ===== 评论管理（审核台） =====

function CommentsTab() {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [page, setPage] = useState(1);
    const [status, setStatus] = useState<string>("all");

    const listQuery = useQuery({
        queryKey: ["admin", "blog", "comments", page, status],
        queryFn: ({ signal }) =>
            listAdminBlogComments({ page, size: PAGE_SIZE, status: status === "all" ? undefined : status }, signal),
    });

    const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["admin", "blog"] });
    const wrap = (fn: () => Promise<unknown>, okKey: string) =>
        fn().then(() => { message.success(t(okKey)); invalidate(); })
            .catch((err) => message.error(getApiErrorMessage(err)));

    const statusTag = (s: string) => {
        const map: Record<string, { color: string; key: string }> = {
            visible: { color: "green", key: "visible" },
            hidden: { color: "default", key: "hidden" },
            quarantined: { color: "orange", key: "quarantined" },
        };
        const item = map[s] ?? { color: "default", key: s };
        return <Tag color={item.color}>{t(`admin.blog.commentStatuses.${item.key}`)}</Tag>;
    };

    return (
        <div>
            <div className="mb-4">
                <Segmented
                    value={status}
                    onChange={(v) => { setStatus(v as string); setPage(1); }}
                    options={[
                        { label: t("admin.blog.filter.all"), value: "all" },
                        { label: t("admin.blog.commentStatuses.visible"), value: "visible" },
                        { label: t("admin.blog.commentStatuses.hidden"), value: "hidden" },
                        { label: t("admin.blog.commentStatuses.quarantined"), value: "quarantined" },
                    ]}
                />
            </div>
            <Table
                rowKey="id"
                loading={listQuery.isLoading}
                dataSource={listQuery.data?.comments ?? []}
                pagination={{
                    current: page, pageSize: PAGE_SIZE, total: listQuery.data?.total ?? 0,
                    onChange: setPage, showSizeChanger: false,
                }}
                expandable={{
                    expandedRowRender: (row) => (
                        <div className="blog-preview max-w-2xl" dangerouslySetInnerHTML={{ __html: row.contentHtml }} />
                    ),
                }}
                columns={[
                    {
                        title: t("admin.blog.commentColumns.author"), width: 160,
                        render: (_: unknown, row) => (
                            <Space size={4}>
                                <span className="font-medium">{row.author.name}</span>
                                {row.author.isAdmin ? <Tag color="blue">{t("admin.blog.badgeAdmin")}</Tag> : null}
                            </Space>
                        ),
                    },
                    {
                        title: t("admin.blog.commentColumns.content"), dataIndex: "contentMd",
                        render: (v: string) => <span className="line-clamp-2 max-w-md">{v}</span>,
                    },
                    {
                        title: t("admin.blog.commentColumns.post"), dataIndex: "postTitle", width: 200,
                        render: (v: string) => <span className="line-clamp-1">{v}</span>,
                    },
                    { title: t("admin.blog.commentColumns.status"), dataIndex: "status", width: 110, render: statusTag },
                    {
                        title: t("admin.blog.commentColumns.createdAt"), dataIndex: "createdAt", width: 150,
                        render: (v: string) => new Date(v).toLocaleString(),
                    },
                    {
                        title: t("admin.blog.columns.actions"), key: "actions", width: 230,
                        render: (_: unknown, row) => (
                            <Space>
                                {row.status === "visible" ? (
                                    <Button size="small" onClick={() => wrap(() => hideAdminBlogComment(row.id), "admin.blog.hidden")}>
                                        {t("admin.blog.hide")}
                                    </Button>
                                ) : (
                                    <Button size="small" onClick={() => wrap(() => restoreAdminBlogComment(row.id), "admin.blog.restored")}>
                                        {t("admin.blog.restore")}
                                    </Button>
                                )}
                                <Button size="small" onClick={() => wrap(() => pinAdminBlogComment(row.id, !row.pinned), "admin.blog.pinSaved")}>
                                    {row.pinned ? t("admin.blog.unpin") : t("admin.blog.pin")}
                                </Button>
                                <Popconfirm title={t("admin.blog.deleteConfirm")} onConfirm={() => wrap(() => deleteAdminBlogComment(row.id), "admin.blog.deleted")}>
                                    <Button size="small" danger>{t("admin.blog.delete")}</Button>
                                </Popconfirm>
                            </Space>
                        ),
                    },
                ]}
            />
        </div>
    );
}
