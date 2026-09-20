import { apiRequest } from "@/services/api/client";

// 用户面反馈工单：提交、我的工单、详情对话与关闭；处理端在 admin 后台。
export type FeedbackTicket = {
    id: string;
    category: "quality" | "suggestion" | "payment" | "account" | "other" | string;
    status: "open" | "resolved" | "closed" | string;
    content: string;
    replyCount: number;
    createdAt: string;
    updatedAt: string;
    resolvedAt?: string | null;
};

export type FeedbackReply = {
    id: string;
    isStaff: boolean;
    content: string;
    createdAt: string;
};

export const FEEDBACK_CATEGORIES = ["quality", "suggestion", "payment", "account", "other"] as const;

/** 提交反馈工单。POST /api/v1/feedback。 */
export function createFeedbackTicket(input: { category: string; content: string }) {
    return apiRequest<{ ticket: FeedbackTicket }>("/feedback", { method: "POST", body: input });
}

/** 分页查询我的工单，可按状态过滤。GET /api/v1/feedback。 */
export function listMyFeedbackTickets(params: { page?: number; size?: number; status?: string }, signal?: AbortSignal) {
    return apiRequest<{ items: FeedbackTicket[]; total: number; page: number; size: number }>("/feedback", { query: params, signal });
}

/** 读取工单详情与对话记录。GET /api/v1/feedback/{id}。 */
export function getFeedbackTicket(id: string, signal?: AbortSignal) {
    return apiRequest<{ ticket: FeedbackTicket; replies: FeedbackReply[] }>(`/feedback/${id}`, { signal });
}

/** 在工单下追加回复（用户与客服共用对话流）。POST /api/v1/feedback/{id}/replies。 */
export function replyFeedbackTicket(id: string, content: string) {
    return apiRequest<{ reply: FeedbackReply }>(`/feedback/${id}/replies`, { method: "POST", body: { content } });
}

/** 用户关闭工单（已解决或不再跟进）。POST /api/v1/feedback/{id}/close。 */
export function closeFeedbackTicket(id: string) {
    return apiRequest<{ id: string; status: string }>(`/feedback/${id}/close`, { method: "POST" });
}

// 生成结果点赞点踩：同一生成记录一条反馈，重复提交覆盖，撤销走 DELETE。
export type GenerationFeedback = {
    generationId: string;
    rating: 1 | -1;
    labels: string[];
    note: string;
    updatedAt: string;
};

/** 提交或覆盖对某条生成记录的点赞/点踩。PUT /api/v1/generations/{id}/feedback。 */
export function setGenerationFeedback(id: string, input: { rating: 1 | -1; labels?: string; note?: string }) {
    return apiRequest<GenerationFeedback>(`/generations/${id}/feedback`, { method: "PUT", body: input });
}

/** 查询已提交的生成反馈，未提交时 feedback 为 null。 */
export function getGenerationFeedback(id: string, signal?: AbortSignal) {
    return apiRequest<{ feedback: GenerationFeedback | null }>(`/generations/${id}/feedback`, { signal });
}

/** 撤销生成反馈。DELETE /api/v1/generations/{id}/feedback。 */
export function deleteGenerationFeedback(id: string) {
    return apiRequest<void>(`/generations/${id}/feedback`, { method: "DELETE" });
}
