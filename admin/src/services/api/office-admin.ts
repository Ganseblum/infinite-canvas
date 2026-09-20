import { apiRequest } from "@/services/api/client";

// ===== 办公助理（运营面）=====
// 契约：/api/admin/office/*，路径以 /admin 开头，共享 client 据此前缀分流到 /api/admin（client.ts buildUrl）。

// 会话条目：列表与详情共用同一形状（列表可能省略可选字段，渲染时按缺省处理）。
export type AdminOfficeSession = {
    id: string;
    title: string;
    status: string;
    userId: string;
    userEmail: string;
    activeRunId?: string | null;
    messagesCount: number;
    createdAt: string;
    updatedAt: string;
    lastRunStatus?: string | null;
};

// 消息快照：content 按存储 JSON 返回（{schemaVersion:1,text}），渲染只取 text 字段。
export type AdminOfficeMessage = {
    id: string;
    role: "user" | "assistant";
    content: { schemaVersion: number; text: string } | null;
    threadId: string;
    turnId: string;
    createdAt: string;
};

export type AdminOfficeSessionListParams = {
    // 服务端按用户 email 或 ID 过滤（与运营界面输入一致）。
    userId?: string;
    status?: string;
    cursor?: string;
    limit?: number;
};

export type AdminOfficeStats = {
    runsTotal: number;
    runsByStatus: { succeeded: number; failed: number; cancelled: number; running: number; queued: number };
    runsByModel: { model: string; count: number }[];
    messagesTotal: number;
    sessionsTotal: number;
    activeUsers: number;
    topUsers: { userId: string; email: string; runs: number; messages: number }[];
    toolCalls: { name: string; count: number }[];
};

// 会话列表是 updated_at 倒序的游标分页（nextCursor 为空即末页），不是 page/total。
export function listAdminOfficeSessions(params: AdminOfficeSessionListParams, signal?: AbortSignal) {
    return apiRequest<{ items: AdminOfficeSession[]; nextCursor?: string }>("/admin/office/sessions", {
        query: { userId: params.userId, status: params.status, cursor: params.cursor, limit: params.limit },
        signal,
    });
}

export function getAdminOfficeSession(id: string, signal?: AbortSignal) {
    return apiRequest<{ session: AdminOfficeSession; messages: AdminOfficeMessage[] }>(`/admin/office/sessions/${id}`, { signal });
}

export function getAdminOfficeStats(days: 7 | 30, signal?: AbortSignal) {
    return apiRequest<AdminOfficeStats>("/admin/office/stats", { query: { days }, signal });
}
