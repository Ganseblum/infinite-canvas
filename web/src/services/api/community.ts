import { apiRequest } from "@/services/api/client";

export type CommunityAuthor = {
    id: string;
    username: string;
    displayName: string;
    avatarUrl: string;
};

export type CommunityWork = {
    id: string;
    title: string;
    description: string;
    tags: string[];
    kind: "image" | "video" | "text" | string;
    storageKey: string;
    coverKey: string;
    likeCount: number;
    remixCount: number;
    reportCount: number;
    sourceWorkId?: string;
    isAIGC?: boolean;
    createdAt: string;
    author: CommunityAuthor;
    liked?: boolean;
};

export type CommunityWorkPage = {
    items: CommunityWork[];
    nextCursor: string | null;
};

// 社区接口客户端（/api/v1/community/*）：作品浏览、发布、点赞与举报。

/** 游标分页浏览作品，支持关键词/标签/排序（latest|hot）/按作者过滤。GET /api/v1/community/works。 */
export function listCommunityWorks(
    params: { cursor?: string; size?: number; q?: string; tag?: string; sort?: "latest" | "hot"; userId?: string },
    signal?: AbortSignal,
) {
    return apiRequest<CommunityWorkPage>("/community/works", {
        query: { cursor: params.cursor, size: params.size, q: params.q, tag: params.tag, sort: params.sort, userId: params.userId },
        signal,
    });
}

/** 读取单个作品详情。GET /api/v1/community/works/{id}。 */
export function getCommunityWork(id: string, signal?: AbortSignal) {
    return apiRequest<{ work: CommunityWork }>(`/community/works/${id}`, { signal });
}

/** 我发布的全部作品（不分页）。GET /api/v1/community/works/mine。 */
export function listMyCommunityWorks(signal?: AbortSignal) {
    return apiRequest<{ items: CommunityWork[] }>("/community/works/mine", { signal });
}

/** 从「我的素材」发布作品：引用素材 id，remix 时带 sourceWorkId 溯源。POST /api/v1/community/works。 */
export function publishCommunityWork(input: { assetId: string; title: string; description?: string; tags?: string; sourceWorkId?: string }) {
    return apiRequest<{ work: CommunityWork }>("/community/works", { method: "POST", body: input });
}

/** 删除我发布的作品。DELETE /api/v1/community/works/{id}。 */
export function deleteCommunityWork(id: string) {
    return apiRequest<void>(`/community/works/${id}`, { method: "DELETE" });
}

/** 点赞（幂等），返回最新计数与当前点赞状态。POST /api/v1/community/works/{id}/like。 */
export function likeCommunityWork(id: string) {
    return apiRequest<{ likeCount: number; liked: boolean }>(`/community/works/${id}/like`, { method: "POST" });
}

/** 取消点赞。POST /api/v1/community/works/{id}/unlike。 */
export function unlikeCommunityWork(id: string) {
    return apiRequest<{ likeCount: number; liked: boolean }>(`/community/works/${id}/unlike`, { method: "POST" });
}

/** 举报作品（理由必填）。POST /api/v1/community/works/{id}/report。 */
export function reportCommunityWork(id: string, reason: string) {
    return apiRequest<{ status: string }>(`/community/works/${id}/report`, { method: "POST", body: { reason } });
}

/** 社区用户主页：作者信息、统计与其作品列表。GET /api/v1/community/users/{id}。 */
export function getCommunityUser(id: string, signal?: AbortSignal) {
    return apiRequest<{ user: CommunityAuthor & { createdAt: string }; stats: { works: number; likes: number }; items: CommunityWork[] }>(
        `/community/users/${id}`,
        { signal },
    );
}

export type PublicSettings = {
    announcement: string;
    maintenanceMode: boolean;
    maintenanceNotice: string;
    communityEnabled: boolean;
    checkinEnabled: boolean;
    inviteEnabled: boolean;
};

/** 公开站点设置：公告、维护模式与各功能开关，登录前后都可读。GET /api/v1/settings/public。 */
export function getPublicSettings(signal?: AbortSignal) {
    return apiRequest<PublicSettings>("/settings/public", { signal });
}
