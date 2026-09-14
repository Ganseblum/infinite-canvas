import { apiRequest } from "@/services/api/client";

export type DeletionStatus = "none" | "pending";

export type DeletionState = {
    status: DeletionStatus;
    scheduledAt: string | null;
};

export function requestAccountDeletion(password: string, signal?: AbortSignal) {
    return apiRequest<{ scheduledAt: string }>("/me/deletion", { method: "POST", body: { password }, signal });
}

export function cancelAccountDeletion(signal?: AbortSignal) {
    return apiRequest<{ status?: DeletionStatus }>("/me/deletion/cancel", { method: "POST", signal });
}
