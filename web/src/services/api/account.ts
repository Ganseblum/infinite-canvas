import type { AuthUser, Plan } from "@/services/api/auth";
import { apiRequest } from "@/services/api/client";
import type { DeletionState } from "@/services/api/deletion";

export type { DeletionState } from "@/services/api/deletion";

export type MeUser = AuthUser & {
    createdAt?: string;
};

export type PlanDetail = Plan & {
    storageBytes: number;
    maxFileBytes: number;
    retentionDays: number;
};

export type CreditsSummary = {
    purchasedMicros: number;
    grantedMicros: number;
    totalMicros: number;
    paidUntil: string | null;
};

export type UsageSummary = {
    storageBytes: number;
    freeImageTrialsUsed: number;
    freeVideoTrialsUsed: number;
};

export type MediaExpirySummary = {
    nearestAt: string | null;
    expiringCount: number;
};

// POST /me/free-grant/claim 的返回：幂等，已领取时服务端原样返回既有结论。
export type FreeGrantClaim = {
    campaignId: string;
    status: string;
    imageTrials: number;
    videoTrials: number;
    grantedMicros: number;
};

export type MeResponse = {
    user: MeUser;
    plan: PlanDetail;
    credits?: CreditsSummary;
    usage?: UsageSummary;
    mediaExpiry?: MediaExpirySummary;
    deletion?: DeletionState;
    readOnly?: boolean;
    graceEndsAt?: string | null;
};

export type UpdateProfileInput = {
    displayName?: string;
    avatarUrl?: string;
};

export function getMe(signal?: AbortSignal) {
    return apiRequest<MeResponse>("/me", { signal });
}

export async function updateProfile(input: UpdateProfileInput) {
    const result = await apiRequest<{ user: AuthUser }>("/me", { method: "PATCH", body: input });
    return result.user;
}

export function changePassword(input: { oldPassword: string; newPassword: string }) {
    return apiRequest<void>("/me/password", { method: "POST", body: input });
}

export function claimFreeGrant() {
    return apiRequest<FreeGrantClaim>("/me/free-grant/claim", { method: "POST" });
}

// GET /me/export：个人数据导出（差异清单 #123）。取回 JSON 后触发浏览器下载，
// 文件名与服务端 Content-Disposition 保持一致。
export async function exportMyData() {
    const payload = await apiRequest<unknown>("/me/export");
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "youc-export.json";
    anchor.click();
    URL.revokeObjectURL(url);
}
