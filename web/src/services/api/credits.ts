import { apiRequest } from "@/services/api/client";

export type CreditBucket = "purchased" | "granted";
export type CreditTransactionType = "purchase" | "consume" | "refund" | "grant" | "expire";

export type CreditTransaction = {
    id: string;
    bucket: CreditBucket;
    type: CreditTransactionType;
    amountMicros: number;
    balanceAfterMicros: number;
    refType?: string;
    refId?: string;
    note?: string;
    createdAt: string;
};

export type CreditBalance = {
    purchasedMicros: number;
    grantedMicros: number;
    totalMicros: number;
    paidUntil: string | null;
    recent: CreditTransaction[];
};

export type CreditPlan = {
    id: string;
    name: string;
    storageBytes: number;
    maxFileBytes: number;
    retentionDays: number;
    freeImageTrials?: number;
    freeVideoTrials?: number;
};

export type CreditPackage = {
    id: string;
    name: string;
    priceMicros: number;
    purchasedMicros: number;
    bonusMicros: number;
    entitlementDays: number;
    currency: string;
};

export type CreditTransactionPage = {
    items: CreditTransaction[];
    nextCursor: string | null;
};

export function getCredits(signal?: AbortSignal) {
    return apiRequest<CreditBalance>("/credits", { signal });
}

export function listCreditTransactions(params: { cursor?: string; size?: number; type?: CreditTransactionType }, signal?: AbortSignal) {
    return apiRequest<CreditTransactionPage>("/credits/transactions", {
        query: { cursor: params.cursor, size: params.size, type: params.type },
        signal,
    });
}

export function getPlans(signal?: AbortSignal) {
    return apiRequest<{ items: CreditPlan[] }>("/plans", { signal });
}

export function getCreditPackages(signal?: AbortSignal) {
    return apiRequest<{ items: CreditPackage[] }>("/credit-packages", { signal });
}
