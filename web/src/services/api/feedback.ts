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

export function createFeedbackTicket(input: { category: string; content: string }) {
    return apiRequest<{ ticket: FeedbackTicket }>("/feedback", { method: "POST", body: input });
}

export function listMyFeedbackTickets(params: { page?: number; size?: number; status?: string }, signal?: AbortSignal) {
    return apiRequest<{ items: FeedbackTicket[]; total: number; page: number; size: number }>("/feedback", { query: params, signal });
}

export function getFeedbackTicket(id: string, signal?: AbortSignal) {
    return apiRequest<{ ticket: FeedbackTicket; replies: FeedbackReply[] }>(`/feedback/${id}`, { signal });
}

export function replyFeedbackTicket(id: string, content: string) {
    return apiRequest<{ reply: FeedbackReply }>(`/feedback/${id}/replies`, { method: "POST", body: { content } });
}

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

export function setGenerationFeedback(id: string, input: { rating: 1 | -1; labels?: string; note?: string }) {
    return apiRequest<GenerationFeedback>(`/generations/${id}/feedback`, { method: "PUT", body: input });
}

export function getGenerationFeedback(id: string, signal?: AbortSignal) {
    return apiRequest<{ feedback: GenerationFeedback | null }>(`/generations/${id}/feedback`, { signal });
}

export function deleteGenerationFeedback(id: string) {
    return apiRequest<void>(`/generations/${id}/feedback`, { method: "DELETE" });
}
