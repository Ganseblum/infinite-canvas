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
