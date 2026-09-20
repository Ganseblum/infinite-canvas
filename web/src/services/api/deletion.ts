import { apiRequest } from "@/services/api/client";

export type DeletionStatus = "none" | "pending";

export type DeletionState = {
    status: DeletionStatus;
    scheduledAt: string | null;
};

/** 请求注销账号（需密码），进入宽限期后可撤销。POST /api/v1/me/deletion。 */
export function requestAccountDeletion(password: string, signal?: AbortSignal) {
    return apiRequest<{ scheduledAt: string }>("/me/deletion", { method: "POST", body: { password }, signal });
}

/** 撤销注销申请。POST /api/v1/me/deletion/cancel。 */
export function cancelAccountDeletion(signal?: AbortSignal) {
    return apiRequest<{ status?: DeletionStatus }>("/me/deletion/cancel", { method: "POST", signal });
}
