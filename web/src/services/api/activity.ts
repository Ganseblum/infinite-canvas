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

// 活动运营接口客户端（/api/v1/activity/*）：每日签到与邀请码。

/** 签到状态：是否开启、今日是否已签、连续天数与奖励点数。GET /api/v1/activity/checkin。 */
export function getCheckinStatus(signal?: AbortSignal) {
    return apiRequest<CheckinStatus>("/activity/checkin", { signal });
}

/** 执行签到；granted 表示本次是否真正发放奖励（重复签到幂等）。POST /api/v1/activity/checkin。 */
export function checkin() {
    return apiRequest<{ checkedIn: boolean; granted: boolean; rewardMicros: number; streakDays: number }>("/activity/checkin", { method: "POST" });
}

/** 我的邀请码与累计奖励。GET /api/v1/activity/invite。 */
export function getInviteInfo(signal?: AbortSignal) {
    return apiRequest<InviteInfo>("/activity/invite", { signal });
}

/** 绑定他人邀请码，返回双方各得的奖励点数。POST /api/v1/activity/invite/bind。 */
export function bindInviteCode(code: string) {
    return apiRequest<{ inviterRewardMicros: number; inviteeRewardMicros: number }>("/activity/invite/bind", {
        method: "POST",
        body: { code },
    });
}
