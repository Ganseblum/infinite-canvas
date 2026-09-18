import { apiRequest } from "@/services/api/client";

export type CheckinStatus = {
    enabled: boolean;
    checkedIn: boolean;
    streakDays: number;
    rewardMicros: number;
    today: string;
};

export type InviteInfo = {
    enabled: boolean;
    code: string;
    invitedCount: number;
    rewardMicros: number;
    rewardPerInviteMicros: number;
};

export function getCheckinStatus(signal?: AbortSignal) {
    return apiRequest<CheckinStatus>("/activity/checkin", { signal });
}

export function checkin() {
    return apiRequest<{ checkedIn: boolean; granted: boolean; rewardMicros: number; streakDays: number }>("/activity/checkin", { method: "POST" });
}

export function getInviteInfo(signal?: AbortSignal) {
    return apiRequest<InviteInfo>("/activity/invite", { signal });
}

export function bindInviteCode(code: string) {
    return apiRequest<{ inviterRewardMicros: number; inviteeRewardMicros: number }>("/activity/invite/bind", {
        method: "POST",
        body: { code },
    });
}
