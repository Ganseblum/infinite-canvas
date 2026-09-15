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
    createdAt: string;
    author: CommunityAuthor;
    liked?: boolean;
};

export type CommunityWorkPage = {
    items: CommunityWork[];
    nextCursor: string | null;
};

export function listCommunityWorks(
    params: { cursor?: string; size?: number; q?: string; tag?: string; sort?: "latest" | "hot"; userId?: string },
    signal?: AbortSignal,
) {
    return apiRequest<CommunityWorkPage>("/community/works", {
        query: { cursor: params.cursor, size: params.size, q: params.q, tag: params.tag, sort: params.sort, userId: params.userId },
        signal,
    });
}

export function getCommunityWork(id: string, signal?: AbortSignal) {
    return apiRequest<{ work: CommunityWork }>(`/community/works/${id}`, { signal });
}

export function listMyCommunityWorks(signal?: AbortSignal) {
    return apiRequest<{ items: CommunityWork[] }>("/community/works/mine", { signal });
}

export function publishCommunityWork(input: { assetId: string; title: string; description?: string; tags?: string; sourceWorkId?: string }) {
    return apiRequest<{ work: CommunityWork }>("/community/works", { method: "POST", body: input });
}

export function deleteCommunityWork(id: string) {
    return apiRequest<void>(`/community/works/${id}`, { method: "DELETE" });
}

export function likeCommunityWork(id: string) {
    return apiRequest<{ likeCount: number; liked: boolean }>(`/community/works/${id}/like`, { method: "POST" });
}

export function unlikeCommunityWork(id: string) {
    return apiRequest<{ likeCount: number; liked: boolean }>(`/community/works/${id}/unlike`, { method: "POST" });
}

export function reportCommunityWork(id: string, reason: string) {
    return apiRequest<{ status: string }>(`/community/works/${id}/report`, { method: "POST", body: { reason } });
}

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

export function getPublicSettings(signal?: AbortSignal) {
    return apiRequest<PublicSettings>("/settings/public", { signal });
}
