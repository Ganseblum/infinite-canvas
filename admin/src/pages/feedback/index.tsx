import { useMemo, useState, type ReactNode } from "react";
import { App, Button, Drawer, Input, Pagination, Segmented, Select, Skeleton, Table, Tabs, Tag } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import {
    getAdminFeedbackTicket,
    listAdminFeedbackTickets,
    listAdminGenerationFeedbacks,
    replyAdminFeedbackTicket,
    updateAdminFeedbackTicketStatus,
    type AdminFeedbackTicket,
} from "@admin/services/api/admin";

const PAGE_SIZE = 10;

export default function AdminFeedbackPage() {
    const { t } = useTranslation();
    return (
        <main className="min-h-full bg-background">
            <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6">
                <h1 className="text-2xl font-semibold">{t("admin.feedback.title")}</h1>
                <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">{t("admin.feedback.description")}</p>
                <Tabs
                    className="mt-4"
                    items={[
                        { key: "tickets", label: t("admin.feedback.ticketsTab"), children: <TicketsTab /> },
                        { key: "generations", label: t("admin.feedback.generationsTab"), children: <GenerationFeedbackTab /> },
                    ]}
                />
            </div>
        </main>
    );
}

function TicketsTab() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [page, setPage] = useState(1);
    const [status, setStatus] = useState<string | undefined>("open");
    const [category, setCategory] = useState<string | undefined>();
    const [detailId, setDetailId] = useState<string | null>(null);
    const [replyContent, setReplyContent] = useState("");

    const listQuery = useQuery({
        queryKey: ["admin", "feedback", "tickets", page, status, category],
        queryFn: ({ signal }) => listAdminFeedbackTickets({ page, size: PAGE_SIZE, status, category }, signal),
    });

    const detailQuery = useQuery({
        queryKey: ["admin", "feedback", "ticket", detailId],
        queryFn: ({ signal }) => getAdminFeedbackTicket(detailId!, signal),
        enabled: !!detailId,
    });

    const invalidate = () => {
        void queryClient.invalidateQueries({ queryKey: ["admin", "feedback"] });
    };

    const replyMutation = useMutation({
        mutationFn: () => replyAdminFeedbackTicket(detailId!, replyContent),
        onSuccess: () => {
            setReplyContent("");
            invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const statusMutation = useMutation({
        mutationFn: (next: string) => updateAdminFeedbackTicketStatus(detailId!, next),
        onSuccess: invalidate,
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const statusTag = (status: string) =>
        status === "open" ? <Tag color="gold">{t("admin.feedback.statuses.open")}</Tag> : status === "resolved" ? <Tag color="green">{t("admin.feedback.statuses.resolved")}</Tag> : <Tag>{t("admin.feedback.statuses.closed")}</Tag>;

    const columns = useMemo(
        () => [
            { title: t("admin.feedback.columns.user"), key: "user", render: (_: unknown, row: AdminFeedbackTicket) => row.user?.email || row.user?.username || "—" },
            { title: t("admin.feedback.columns.category"), key: "category", width: 100, render: (_: unknown, row: AdminFeedbackTicket) => t(`admin.feedback.categories.${row.category}`) },
            { title: t("admin.feedback.columns.content"), key: "content", render: (_: unknown, row: AdminFeedbackTicket) => <span className="line-clamp-2 max-w-md text-sm">{row.content}</span> },
            { title: t("admin.feedback.columns.status"), key: "status", width: 90, render: (_: unknown, row: AdminFeedbackTicket) => statusTag(row.status) },
            { title: t("admin.feedback.columns.replies"), key: "replies", width: 70, dataIndex: "replyCount" },
            { title: t("admin.feedback.columns.updatedAt"), key: "updatedAt", width: 110, render: (_: unknown, row: AdminFeedbackTicket) => (row.updatedAt || "").slice(0, 16).replace("T", " ") },
            {
                title: t("admin.membership.columns.actions"),
                key: "actions",
                width: 80,
                render: (_: unknown, row: AdminFeedbackTicket) => (
                    <Button size="small" type="link" onClick={() => setDetailId(row.id)}>
                        {t("common.view")}
                    </Button>
                ),
            },
        ],
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [t],
    );

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3">
                <Segmented
                    value={status ?? "all"}
                    onChange={(value) => {
                        setStatus(value === "all" ? undefined : String(value));
                        setPage(1);
                    }}
                    options={[
                        { value: "all", label: t("admin.feedback.filter.all") },
                        { value: "open", label: t("admin.feedback.statuses.open") },
                        { value: "resolved", label: t("admin.feedback.statuses.resolved") },
                        { value: "closed", label: t("admin.feedback.statuses.closed") },
                    ]}
                />
                <Select
                    className="w-40"
                    allowClear
                    placeholder={t("admin.feedback.filter.category")}
                    value={category}
                    onChange={(value) => {
                        setCategory(value || undefined);
                        setPage(1);
                    }}
                    options={["quality", "suggestion", "payment", "account", "other"].map((key) => ({ value: key, label: t(`admin.feedback.categories.${key}`) }))}
                />
            </div>
            {listQuery.isPending ? (
                <Skeleton active paragraph={{ rows: 6 }} />
            ) : (
                <Table
                    rowKey="id"
                    size="small"
                    columns={columns}
                    dataSource={listQuery.data?.items ?? []}
                    loading={listQuery.isFetching}
                    pagination={{ current: page, pageSize: PAGE_SIZE, total: listQuery.data?.total ?? 0, onChange: setPage, showSizeChanger: false }}
                />
            )}

            <Drawer title={t("admin.feedback.detailTitle")} open={!!detailId} size="large" onClose={() => setDetailId(null)}>
                {detailQuery.isPending || !detailQuery.data ? (
                    <Skeleton active paragraph={{ rows: 8 }} />
                ) : (
                    <FeedbackDetail
                        ticket={detailQuery.data.ticket}
                        replies={detailQuery.data.replies}
                        replyContent={replyContent}
                        onReplyChange={setReplyContent}
                        replying={replyMutation.isPending}
                        onReply={() => replyMutation.mutate()}
                        onStatus={(next) => statusMutation.mutate(next)}
                        statusPending={statusMutation.isPending}
                        statusTag={statusTag}
                    />
                )}
            </Drawer>
        </div>
    );
}

function FeedbackDetail({
    ticket,
    replies,
    replyContent,
    onReplyChange,
    replying,
    onReply,
    onStatus,
    statusPending,
    statusTag,
}: {
    ticket: AdminFeedbackTicket;
    replies: { id: string; isStaff: boolean; content: string; createdAt: string }[];
    replyContent: string;
    onReplyChange: (value: string) => void;
    replying: boolean;
    onReply: () => void;
    onStatus: (next: string) => void;
    statusPending: boolean;
    statusTag: (status: string) => ReactNode;
}) {
    const { t } = useTranslation();
    return (
        <div className="space-y-4">
            <div className="rounded-xl border border-stone-200 p-4 dark:border-stone-800">
                <div className="flex flex-wrap items-center gap-2">
                    <Tag className="m-0">{t(`admin.feedback.categories.${ticket.category}`)}</Tag>
                    {statusTag(ticket.status)}
                    <span className="text-xs text-stone-400">{ticket.user?.email || ticket.user?.username}</span>
                    <span className="ml-auto text-xs text-stone-400">{(ticket.createdAt || "").slice(0, 16).replace("T", " ")}</span>
                </div>
                <p className="mt-3 whitespace-pre-wrap text-sm">{ticket.content}</p>
                <div className="mt-3 flex flex-wrap gap-2">
                    <Button size="small" loading={statusPending && ticket.status !== "resolved"} onClick={() => onStatus("resolved")}>
                        {t("admin.feedback.markResolved")}
                    </Button>
                    <Button size="small" loading={statusPending && ticket.status !== "closed"} onClick={() => onStatus("closed")}>
                        {t("admin.feedback.markClosed")}
                    </Button>
                    {ticket.status !== "open" ? (
                        <Button size="small" loading={statusPending && ticket.status !== "open"} onClick={() => onStatus("open")}>
                            {t("admin.feedback.reopen")}
                        </Button>
                    ) : null}
                </div>
            </div>
            <div className="space-y-3">
                {replies.map((reply) => (
                    <div key={reply.id} className={reply.isStaff ? "flex justify-end" : ""}>
                        <div className={`max-w-[85%] rounded-xl border p-3 text-sm ${reply.isStaff ? "border-blue-200 bg-blue-50 dark:border-blue-900 dark:bg-blue-950" : "border-stone-200 dark:border-stone-800"}`}>
                            <p className="text-xs text-stone-400">{reply.isStaff ? t("admin.feedback.staffLabel") : ticket.user?.username || t("admin.feedback.userLabel")} · {reply.createdAt.slice(0, 16).replace("T", " ")}</p>
                            <p className="mt-1 whitespace-pre-wrap">{reply.content}</p>
                        </div>
                    </div>
                ))}
            </div>
            <div className="space-y-2 border-t border-stone-200 pt-4 dark:border-stone-800">
                <Input.TextArea rows={3} maxLength={1000} value={replyContent} placeholder={t("admin.feedback.replyPlaceholder")} onChange={(event) => onReplyChange(event.target.value)} />
                <div className="flex justify-end">
                    <Button type="primary" disabled={!replyContent.trim()} loading={replying} onClick={onReply}>
                        {t("admin.feedback.sendReply")}
                    </Button>
                </div>
            </div>
        </div>
    );
}

function GenerationFeedbackTab() {
    const { t } = useTranslation();
    const [page, setPage] = useState(1);
    const [rating, setRating] = useState<string | undefined>();
    const listQuery = useQuery({
        queryKey: ["admin", "feedback", "generations", page, rating],
        queryFn: ({ signal }) => listAdminGenerationFeedbacks({ page, size: PAGE_SIZE, rating }, signal),
    });
    const columns = useMemo(
        () => [
            {
                title: t("admin.feedback.columns.rating"),
                key: "rating",
                width: 90,
                render: (_: unknown, row: { rating: number }) => (row.rating === -1 ? <Tag color="rose">{t("admin.feedback.dislike")}</Tag> : <Tag color="green">{t("admin.feedback.like")}</Tag>),
            },
            { title: t("admin.feedback.columns.labels"), key: "labels", render: (_: unknown, row: { labels: string[] }) => (row.labels.filter(Boolean).length ? row.labels.filter(Boolean).join("、") : "—") },
            { title: t("admin.feedback.columns.note"), key: "note", render: (_: unknown, row: { note: string }) => <span className="line-clamp-2 max-w-sm text-sm">{row.note || "—"}</span> },
            { title: t("admin.feedback.columns.user"), key: "user", render: (_: unknown, row: { user?: { email?: string } }) => row.user?.email || "—" },
            { title: t("admin.feedback.columns.generation"), key: "generation", width: 150, render: (_: unknown, row: { generation?: { kind?: string; model?: string } }) => `${row.generation?.kind || "—"} · ${row.generation?.model || "—"}` },
            { title: t("admin.feedback.columns.updatedAt"), key: "updatedAt", width: 110, render: (_: unknown, row: { updatedAt: string }) => (row.updatedAt || "").slice(0, 16).replace("T", " ") },
        ],
        [t],
    );
    return (
        <div className="space-y-4">
            <Segmented
                value={rating ?? "all"}
                onChange={(value) => {
                    setRating(value === "all" ? undefined : String(value));
                    setPage(1);
                }}
                options={[
                    { value: "all", label: t("admin.feedback.filter.all") },
                    { value: "-1", label: t("admin.feedback.dislike") },
                    { value: "1", label: t("admin.feedback.like") },
                ]}
            />
            {listQuery.isPending ? (
                <Skeleton active paragraph={{ rows: 6 }} />
            ) : (
                <Table
                    rowKey="generationId"
                    size="small"
                    columns={columns}
                    dataSource={listQuery.data?.items ?? []}
                    loading={listQuery.isFetching}
                    pagination={{ current: page, pageSize: PAGE_SIZE, total: listQuery.data?.total ?? 0, onChange: setPage, showSizeChanger: false }}
                />
            )}
        </div>
    );
}
