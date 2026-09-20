import { useMemo, useState } from "react";
import { App, Button, Drawer, Empty, Input, Modal, Pagination, Segmented, Select, Skeleton, Tag } from "antd";
import { MessageSquarePlus } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { FEEDBACK_CATEGORIES, closeFeedbackTicket, getFeedbackTicket, listMyFeedbackTickets, replyFeedbackTicket, createFeedbackTicket, type FeedbackTicket } from "@/services/api/feedback";

/** 工单详情抽屉的数据结构：工单本体 + 时间正序的回复列表（isStaff 区分客服/用户）。 */
type TicketDetail = {
    ticket: FeedbackTicket;
    replies: { id: string; isStaff: boolean; content: string; createdAt: string }[];
};

/** 反馈工单页入口：我的工单列表（状态筛选 + 分页）、新建工单弹窗、详情抽屉内追回复与结单。 */
export default function FeedbackPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [statusFilter, setStatusFilter] = useState<string | undefined>();
    const [page, setPage] = useState(1);
    const [size, setSize] = useState(10);
    const [createOpen, setCreateOpen] = useState(false);
    const [createCategory, setCreateCategory] = useState<string>("quality");
    const [createContent, setCreateContent] = useState("");
    const [detailId, setDetailId] = useState<string | null>(null);
    const [replyContent, setReplyContent] = useState("");

    const ticketsQuery = useQuery({
        queryKey: ["feedback", "mine", page, size, statusFilter],
        queryFn: ({ signal }) => listMyFeedbackTickets({ page, size, status: statusFilter }, signal),
    });

    // 详情查询仅在抽屉打开（detailId 存在）时发起。
    const detailQuery = useQuery({
        queryKey: ["feedback", "detail", detailId],
        queryFn: ({ signal }) => getFeedbackTicket(detailId!, signal),
        enabled: !!detailId,
    });

    // 新建/回复/结单都只需让整个 feedback 缓存失效，由各查询自行重拉。
    const invalidate = () => queryClient.invalidateQueries({ queryKey: ["feedback"] });

    const createMutation = useMutation({
        mutationFn: () => createFeedbackTicket({ category: createCategory, content: createContent }),
        onSuccess: () => {
            message.success(t("feedback.created"));
            setCreateOpen(false);
            setCreateContent("");
            setCreateCategory("quality");
            void invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const replyMutation = useMutation({
        mutationFn: () => replyFeedbackTicket(detailId!, replyContent),
        onSuccess: () => {
            setReplyContent("");
            void invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const closeMutation = useMutation({
        mutationFn: () => closeFeedbackTicket(detailId!),
        onSuccess: () => {
            message.success(t("feedback.closed"));
            void invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    // 工单状态到 Tag 颜色/文案的映射；依赖 t，语言切换时重建。
    const statusTag: Record<string, { color: string; label: string }> = useMemo(
        () => ({
            open: { color: "gold", label: t("feedback.status.open") },
            resolved: { color: "green", label: t("feedback.status.resolved") },
            closed: { color: "default", label: t("feedback.status.closed") },
        }),
        [t],
    );

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
                <div className="flex items-end justify-between gap-3">
                    <div>
                        <h1 className="text-3xl font-semibold">{t("feedback.title")}</h1>
                        <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("feedback.description")}</p>
                    </div>
                    <Button type="primary" icon={<MessageSquarePlus className="size-4" />} onClick={() => setCreateOpen(true)}>
                        {t("feedback.create")}
                    </Button>
                </div>

                <div className="mt-5">
                    <Segmented
                        value={statusFilter ?? "all"}
                        onChange={(value) => {
                            setStatusFilter(value === "all" ? undefined : String(value));
                            setPage(1);
                        }}
                        options={[
                            { value: "all", label: t("feedback.filter.all") },
                            { value: "open", label: t("feedback.status.open") },
                            { value: "resolved", label: t("feedback.status.resolved") },
                            { value: "closed", label: t("feedback.status.closed") },
                        ]}
                    />
                </div>

                {ticketsQuery.isPending ? (
                    <div className="mt-6 space-y-3">
                        {Array.from({ length: 3 }).map((_, index) => (
                            <Skeleton key={index} active title paragraph={{ rows: 1 }} />
                        ))}
                    </div>
                ) : ticketsQuery.isError ? (
                    <p className="mt-8 text-sm text-rose-500">{getApiErrorMessage(ticketsQuery.error)}</p>
                ) : !ticketsQuery.data.items.length ? (
                    <Empty className="mt-16" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("feedback.empty")} />
                ) : (
                    <div className="mt-5 space-y-3">
                        {ticketsQuery.data.items.map((ticket) => (
                            <button
                                key={ticket.id}
                                type="button"
                                className="block w-full rounded-xl border border-stone-200 p-4 text-left transition hover:border-stone-300 dark:border-stone-800 dark:hover:border-stone-700"
                                onClick={() => setDetailId(ticket.id)}
                            >
                                <div className="flex items-center gap-2">
                                    <Tag className="m-0">{t(`feedback.categories.${ticket.category}`)}</Tag>
                                    <Tag color={statusTag[ticket.status]?.color} className="m-0">
                                        {statusTag[ticket.status]?.label}
                                    </Tag>
                                    <span className="ml-auto text-xs text-stone-400">{ticket.updatedAt.slice(0, 16).replace("T", " ")}</span>
                                </div>
                                <p className="mt-2 line-clamp-2 text-sm">{ticket.content}</p>
                                <p className="mt-1 text-xs text-stone-400">{t("feedback.replyCount", { count: ticket.replyCount })}</p>
                            </button>
                        ))}
                    </div>
                )}

                {ticketsQuery.data && ticketsQuery.data.total > size ? (
                    <div className="mt-6 flex justify-center">
                        <Pagination current={page} pageSize={size} total={ticketsQuery.data.total} showSizeChanger pageSizeOptions={[10, 20, 50]} onChange={(nextPage, nextSize) => { setPage(nextPage); setSize(nextSize); }} />
                    </div>
                ) : null}
            </div>

            <Modal
                title={t("feedback.create")}
                open={createOpen}
                okText={t("feedback.submit")}
                cancelText={t("common.cancel")}
                confirmLoading={createMutation.isPending}
                onCancel={() => setCreateOpen(false)}
                onOk={() => createMutation.mutate()}
            >
                <div className="space-y-4 pt-2">
                    <Select
                        className="w-full"
                        value={createCategory}
                        onChange={(value) => setCreateCategory(String(value))}
                        options={FEEDBACK_CATEGORIES.map((category) => ({ value: category, label: t(`feedback.categories.${category}`) }))}
                    />
                    <Input.TextArea
                        rows={5}
                        maxLength={1000}
                        showCount
                        value={createContent}
                        placeholder={t("feedback.contentPlaceholder")}
                        onChange={(event) => setCreateContent(event.target.value)}
                    />
                </div>
            </Modal>

            <Drawer title={t("feedback.detailTitle")} open={!!detailId} size="large" onClose={() => setDetailId(null)}>
                {detailQuery.isPending || !detailQuery.data ? (
                    <Skeleton active paragraph={{ rows: 6 }} />
                ) : (
                    <FeedbackConversation
                        detail={detailQuery.data as TicketDetail}
                        replyContent={replyContent}
                        onReplyChange={setReplyContent}
                        replying={replyMutation.isPending}
                        onReply={() => replyMutation.mutate()}
                        closing={closeMutation.isPending}
                        onCloseTicket={() => closeMutation.mutate()}
                        statusTag={statusTag}
                    />
                )}
            </Drawer>
        </main>
    );
}

/** 抽屉内的会话视图：客服消息靠左、用户回复靠右的气泡布局，底部为回复输入与结单操作。
 * 回复输入框内容由父级托管，避免抽屉开关时丢失草稿。
 * @param detail 工单详情（工单本体 + 回复列表）
 * @param replyContent 回复输入内容（受控于父级）
 * @param onReplyChange 更新回复内容
 * @param replying 回复请求进行中
 * @param onReply 发送回复
 * @param closing 结单请求进行中
 * @param onCloseTicket 关闭（结单）工单
 * @param statusTag 状态到 Tag 颜色/文案的映射
 */
function FeedbackConversation({
    detail,
    replyContent,
    onReplyChange,
    replying,
    onReply,
    closing,
    onCloseTicket,
    statusTag,
}: {
    detail: TicketDetail;
    replyContent: string;
    onReplyChange: (value: string) => void;
    replying: boolean;
    onReply: () => void;
    closing: boolean;
    onCloseTicket: () => void;
    statusTag: Record<string, { color: string; label: string }>;
}) {
    const { t } = useTranslation();
    const { ticket, replies } = detail;
    return (
        <div className="space-y-4">
            <div className="rounded-xl border border-stone-200 p-4 dark:border-stone-800">
                <div className="flex items-center gap-2">
                    <Tag className="m-0">{t(`feedback.categories.${ticket.category}`)}</Tag>
                    <Tag color={statusTag[ticket.status]?.color} className="m-0">
                        {statusTag[ticket.status]?.label}
                    </Tag>
                    <span className="ml-auto text-xs text-stone-400">{ticket.createdAt.slice(0, 16).replace("T", " ")}</span>
                </div>
                <p className="mt-3 whitespace-pre-wrap text-sm">{ticket.content}</p>
            </div>
            <div className="space-y-3">
                {replies.map((reply) => (
                    <div key={reply.id} className={reply.isStaff ? "" : "flex justify-end"}>
                        <div className={`max-w-[85%] rounded-xl border p-3 text-sm ${reply.isStaff ? "border-stone-200 dark:border-stone-800" : "border-blue-200 bg-blue-50 dark:border-blue-900 dark:bg-blue-950"}`}>
                            <p className="text-xs text-stone-400">{reply.isStaff ? t("feedback.staffLabel") : t("feedback.selfLabel")} · {reply.createdAt.slice(0, 16).replace("T", " ")}</p>
                            <p className="mt-1 whitespace-pre-wrap">{reply.content}</p>
                        </div>
                    </div>
                ))}
                {!replies.length ? <p className="text-sm text-stone-400">{t("feedback.noReplies")}</p> : null}
            </div>
            <div className="space-y-2 border-t border-stone-200 pt-4 dark:border-stone-800">
                <Input.TextArea rows={3} maxLength={1000} value={replyContent} placeholder={t("feedback.replyPlaceholder")} onChange={(event) => onReplyChange(event.target.value)} />
                <div className="flex justify-end gap-2">
                    {ticket.status !== "closed" ? (
                        <Button danger loading={closing} onClick={onCloseTicket}>
                            {t("feedback.closeTicket")}
                        </Button>
                    ) : null}
                    <Button type="primary" disabled={!replyContent.trim()} loading={replying} onClick={onReply}>
                        {t("feedback.sendReply")}
                    </Button>
                </div>
            </div>
        </div>
    );
}
