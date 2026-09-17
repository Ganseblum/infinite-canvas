import { apiRequest } from "@/services/api/client";
import type { ModelConstraints, ModelCreditCost, ModelCapability } from "@/services/api/catalog";

export type AdminUser = {
    id: string;
    email: string;
    username: string;
    role: string;
    // 后台角色 key（未分配后台角色时为 null）。role 只是旧列的保守投影，判定一律用 roleKey。
    roleKey: string | null;
    status: "active" | "disabled" | "pending_deletion";
    emailVerified: boolean;
    planId: "free" | "paid" | "sunset";
    purchasedMicros: number;
    grantedMicros: number;
    paidUntil: string | null;
    storageBytes: number;
    createdAt: string;
};

export type AdminUserDetail = AdminUser & {
    planName: string;
    storageLimit: number;
    maxFileBytes: number;
    retentionDays: number;
    mediaCount: number;
    readOnly: boolean;
    lastLoginAt: string | null;
};

export type AdminUserListParams = {
    page?: number;
    size?: number;
    q?: string;
    status?: string;
    planId?: string;
    sort?: string;
};

export type AdminStats = {
    userTotal: number;
    userToday: number;
    storageBytes: number;
    generationToday: number;
    orderToday: number;
    revenueMicrosToday: number;
    creditsTotal: number;
};

export type AdminModel = {
    id: string;
    name: string;
    displayName: string;
    capability: ModelCapability;
    provider: string;
    constraints: ModelConstraints;
    creditCost: ModelCreditCost;
    channelIds?: string[];
    freeTrialEligible: boolean;
    enabled: boolean;
    sort: number;
    createdAt: string;
    updatedAt: string;
};

export type AdminModelInput = {
    name: string;
    displayName: string;
    capability: ModelCapability;
    provider: string;
    constraints: ModelConstraints;
    creditCost: ModelCreditCost;
    channelIds?: string[];
    freeTrialEligible?: boolean;
    enabled?: boolean;
    sort?: number;
};

export type AdminPromotion = {
    id: string;
    modelId: string;
    name: string;
    matchParams: Record<string, string>;
    discountBps: number;
    priority: number;
    version: number;
    status: "draft" | "scheduled" | "active" | "ended" | "disabled";
    startsAt: string;
    endsAt: string;
    createdAt: string;
    updatedAt: string;
};

export type AdminPromotionInput = {
    modelId: string;
    name: string;
    matchParams: Record<string, string>;
    discountBps: number;
    priority?: number;
    startsAt: string;
    endsAt: string;
    status?: "disabled";
    reason?: string;
};

export type AdminPackage = {
    id: string;
    name: string;
    priceMicros: number;
    bonusMicros: number;
    entitlementDays: number;
    currency: string;
    enabled: boolean;
    sort: number;
};

export type AdminPackageInput = {
    name: string;
    priceMicros: number;
    bonusMicros?: number;
    entitlementDays?: number;
    enabled?: boolean;
    sort?: number;
};

export type AdminOrder = {
    id: string;
    userId: string;
    provider: string;
    packageId: string;
    priceMicros: number;
    currency: string;
    purchasedMicros: number;
    grantedMicros: number;
    entitlementDays: number;
    status: string;
    paidAt: string | null;
    createdAt: string;
};

export type AdminOrderListParams = {
    page?: number;
    size?: number;
    status?: string;
    provider?: string;
    userId?: string;
    sort?: string;
};

export type AdminCleanupReport = {
    userId: string;
    dryRun: boolean;
    scanned: number;
    reclaimed: number;
    freedBytes: number;
    storageUsed: number;
    storageLimit: number;
    items: Array<{ storageKey: string; bytes: number; reason: string }>;
};

export function getAdminStats(signal?: AbortSignal) {
    return apiRequest<AdminStats>("/admin/stats", { signal });
}

export function listAdminUsers(params: AdminUserListParams, signal?: AbortSignal) {
    return apiRequest<{ items: AdminUser[]; total: number; page: number; size: number }>("/admin/users", {
        query: { page: params.page, size: params.size, q: params.q, status: params.status, planId: params.planId, sort: params.sort },
        signal,
    });
}

export function getAdminUser(id: string, signal?: AbortSignal) {
    return apiRequest<{ user: AdminUserDetail }>(`/admin/users/${id}`, { signal });
}

export function patchAdminUser(id: string, status: "active" | "disabled") {
    return apiRequest<{ id: string; status: string }>(`/admin/users/${id}`, { method: "PATCH", body: { status } });
}

export function resetAdminUserPassword(id: string, password: string) {
    return apiRequest<void>(`/admin/users/${id}/password`, { method: "POST", body: { password } });
}

export function adjustAdminUserCredits(id: string, input: { bucket: "purchased" | "granted"; amountMicros: number; note: string }) {
    return apiRequest<{ purchasedMicros: number; grantedMicros: number }>(`/admin/users/${id}/credits`, { method: "POST", body: input });
}

export function recalculateAdminUserUsage(id: string) {
    return apiRequest<{ storageBytes: number }>(`/admin/users/${id}/usage/recalculate`, { method: "POST" });
}

export function reclaimAdminUserMedia(id: string, dryRun: boolean) {
    return apiRequest<AdminCleanupReport>(`/admin/users/${id}/media/reclaim`, { query: { dryRun: dryRun ? "true" : undefined }, method: "POST" });
}

export function listAdminModels(signal?: AbortSignal) {
    return apiRequest<{ items: AdminModel[] }>("/admin/models", { signal });
}

export function createAdminModel(input: AdminModelInput) {
    return apiRequest<AdminModel>("/admin/models", { method: "POST", body: input });
}

export function updateAdminModel(id: string, input: Partial<AdminModelInput>) {
    return apiRequest<AdminModel>(`/admin/models/${id}`, { method: "PATCH", body: input });
}

export function deleteAdminModel(id: string) {
    return apiRequest<void>(`/admin/models/${id}`, { method: "DELETE" });
}

export function listAdminPromotions(params: { modelId?: string; status?: string } = {}, signal?: AbortSignal) {
    return apiRequest<{ items: AdminPromotion[] }>("/admin/model-promotions", { query: { modelId: params.modelId, status: params.status }, signal });
}

export function createAdminPromotion(input: AdminPromotionInput) {
    return apiRequest<AdminPromotion>("/admin/model-promotions", { method: "POST", body: input });
}

export function updateAdminPromotion(id: string, input: Partial<AdminPromotionInput>) {
    return apiRequest<AdminPromotion>(`/admin/model-promotions/${id}`, { method: "PATCH", body: input });
}

export function listAdminPackages(signal?: AbortSignal) {
    return apiRequest<{ items: AdminPackage[] }>("/admin/credit-packages", { signal });
}

export function createAdminPackage(input: AdminPackageInput & { id: string }) {
    return apiRequest<AdminPackage>("/admin/credit-packages", { method: "POST", body: input });
}

export function updateAdminPackage(id: string, input: Partial<AdminPackageInput>) {
    return apiRequest<AdminPackage>(`/admin/credit-packages/${id}`, { method: "PATCH", body: input });
}

export function listAdminOrders(params: AdminOrderListParams, signal?: AbortSignal) {
    return apiRequest<{ items: AdminOrder[]; total: number; page: number; size: number }>("/admin/orders", {
        query: { page: params.page, size: params.size, status: params.status, provider: params.provider, userId: params.userId, sort: params.sort },
        signal,
    });
}

export type AdminChannel = {
    id: string;
    name: string;
    baseUrl: string;
    apiFormat: "openai" | "gemini" | "ark";
    priority: number;
    enabled: boolean;
    hasKey: boolean;
    createdAt: string;
    updatedAt: string;
};

export type AdminChannelInput = {
    name: string;
    baseUrl: string;
    apiFormat: AdminChannel["apiFormat"];
    apiKey?: string;
    priority?: number;
    enabled?: boolean;
};

export function listAdminChannels(signal?: AbortSignal) {
    return apiRequest<{ items: AdminChannel[] }>("/admin/channels", { signal });
}

export function createAdminChannel(input: AdminChannelInput & { apiKey: string }) {
    return apiRequest<AdminChannel>("/admin/channels", { method: "POST", body: input });
}

export function updateAdminChannel(id: string, input: Partial<AdminChannelInput>) {
    return apiRequest<AdminChannel>(`/admin/channels/${id}`, { method: "PATCH", body: input });
}

export function deleteAdminChannel(id: string) {
    return apiRequest<void>(`/admin/channels/${id}`, { method: "DELETE" });
}

export type ModerationRecord = {
    id: string;
    userId: string;
    stage: "prompt" | "reference" | "artifact" | "upload";
    contentType: string;
    contentHash: string;
    provider: string;
    providerRequestId?: string;
    policyVersion: string;
    decision: "pending" | "passed" | "rejected" | "error";
    riskLabels: string[];
    reviewStatus: "not_required" | "pending" | "approved" | "rejected";
    reviewRevision: number;
    reviewNote?: string;
    reviewedBy?: string;
    reviewedAt?: string;
    compensatedMicros: number;
    generationId?: string;
    quarantineExpiresAt?: string;
    createdAt: string;
};

export type ModerationRecordDetail = ModerationRecord & {
    providerResult?: Record<string, unknown>;
    quarantine?: { available: boolean; previewUrl?: string; previewPath?: string; expiresAt?: string; reason?: string };
};

export type ModerationStats = {
    total: number;
    passed: number;
    rejected: number;
    errors: number;
    pendingReview: number;
    rejectedRate: number;
    errorRate: number;
    labelCounts: Record<string, number>;
    reviewRate: number;
    quarantineCount: number;
};

export function listModerationRecords(
    params: { page?: number; size?: number; stage?: string; decision?: string; reviewStatus?: string; userId?: string; label?: string; sort?: string },
    signal?: AbortSignal,
) {
    return apiRequest<{ items: ModerationRecord[]; total: number; page: number; size: number }>("/admin/moderation/records", {
        query: {
            page: params.page,
            size: params.size,
            stage: params.stage,
            decision: params.decision,
            reviewStatus: params.reviewStatus,
            userId: params.userId,
            label: params.label,
            sort: params.sort,
        },
        signal,
    });
}

export function getModerationRecord(id: string, signal?: AbortSignal) {
    return apiRequest<{ record: ModerationRecordDetail }>(`/admin/moderation/records/${id}`, { signal });
}

export function reviewModerationRecord(id: string, input: { decision: "approved" | "rejected"; note: string; revision: number }) {
    return apiRequest<{ released: boolean; releaseError?: string }>(`/admin/moderation/records/${id}`, { method: "PATCH", body: input });
}

export function compensateModerationRecord(id: string, input: { amountMicros: number; note: string }) {
    return apiRequest<{ grantedMicros: number }>(`/admin/moderation/records/${id}/compensate`, { method: "POST", body: input });
}

export function getModerationStats(signal?: AbortSignal) {
    return apiRequest<ModerationStats>("/admin/moderation/stats", { signal });
}

export type SiteSettings = {
    announcement: string;
    registrationEnabled: boolean;
    maintenanceMode: boolean;
    maintenanceNotice: string;
    communityEnabled: boolean;
    checkinEnabled: boolean;
    checkinRewardMicros: number;
    inviteEnabled: boolean;
    inviteRewardMicros: number;
    inviteeRewardMicros: number;
    maxUploadBytes: number;
    generationConcurrency: number;
};

export type AdminSummary = {
    id: string;
    email: string;
    username: string;
    displayName: string;
    status: string;
    createdAt: string;
    lastLoginAt: string | null;
};

export type AuditLog = {
    id: string;
    actorUserId: string;
    action: string;
    targetType: string;
    targetId: string;
    requestId?: string;
    reason?: string;
    beforeSummary?: unknown;
    afterSummary?: unknown;
    createdAt: string;
};

export type CommunityAdminWork = {
    id: string;
    userId: string;
    title: string;
    description: string;
    tags: string;
    coverKey: string;
    status: "published" | "hidden" | "removed";
    likeCount: number;
    remixCount: number;
    reportCount: number;
    createdAt: string;
};

export type CommunityAdminReport = {
    id: string;
    workId: string;
    workTitle: string;
    reason: string;
    status: "pending" | "handled" | "dismissed";
    createdAt: string;
};

export type RevenueStats = {
    revenueMicrosToday: number;
    ordersToday: number;
    revenueMicrosWeek: number;
    ordersWeek: number;
    revenueMicrosTotal: number;
    ordersTotal: number;
    paidUsers: number;
    userTotal: number;
    conversionRate: number;
};

export function getSettings(signal?: AbortSignal) {
    return apiRequest<SiteSettings>("/admin/settings", { signal });
}

export function updateSettings(input: Partial<SiteSettings>) {
    return apiRequest<SiteSettings>("/admin/settings", { method: "PATCH", body: input });
}

export function listAdmins(signal?: AbortSignal) {
    return apiRequest<{ items: AdminSummary[] }>("/admin/admins", { signal });
}

export function addAdmin(email: string) {
    return apiRequest<{ id: string; role: string }>("/admin/admins", { method: "POST", body: { email } });
}

export function removeAdmin(id: string) {
    return apiRequest<void>(`/admin/admins/${id}`, { method: "DELETE" });
}

export function listAuditLogs(params: { page?: number; size?: number; action?: string; targetType?: string }, signal?: AbortSignal) {
    return apiRequest<{ items: AuditLog[]; total: number; page: number; size: number }>("/admin/audit-logs", {
        query: { page: params.page, size: params.size, action: params.action, targetType: params.targetType },
        signal,
    });
}

export function getRevenueStats(signal?: AbortSignal) {
    return apiRequest<RevenueStats>("/admin/stats/revenue", { signal });
}

export function listAdminCommunityWorks(
    params: { page?: number; size?: number; status?: string; q?: string; userId?: string },
    signal?: AbortSignal,
) {
    return apiRequest<{ items: CommunityAdminWork[]; total: number; page: number; size: number }>("/admin/community/works", {
        query: { page: params.page, size: params.size, status: params.status, q: params.q, userId: params.userId },
        signal,
    });
}

export function patchAdminCommunityWork(id: string, status: "published" | "hidden" | "removed") {
    return apiRequest<{ id: string; status: string }>(`/admin/community/works/${id}`, { method: "PATCH", body: { status } });
}

export function listAdminCommunityReports(params: { page?: number; size?: number; status?: string }, signal?: AbortSignal) {
    return apiRequest<{ items: CommunityAdminReport[]; total: number; page: number; size: number }>("/admin/community/reports", {
        query: { page: params.page, size: params.size, status: params.status },
        signal,
    });
}

export function patchAdminCommunityReport(id: string, input: { status: "handled" | "dismissed"; removeWork?: boolean }) {
    return apiRequest<{ id: string; status: string }>(`/admin/community/reports/${id}`, { method: "PATCH", body: input });
}

// ===== 后台角色与权限（RBAC）=====

export type AdminMe = {
    role: { key: string; name: string; isSystem: boolean };
    permissions: string[];
};

export type AdminRole = {
    key: string;
    name: string;
    description: string;
    isSystem: boolean;
    memberCount: number;
    permissions: string[];
};

export type AdminPermission = {
    key: string;
    name: string;
    description: string;
    sort: number;
};

export type AdminPermissionGroup = {
    module: string;
    moduleName: string;
    permissions: AdminPermission[];
};

// 唯一不要求权限点的管理接口：有后台角色即可调用，前端据此渲染菜单。
export function getAdminMe(signal?: AbortSignal) {
    return apiRequest<AdminMe>("/admin/me", { signal });
}

// 当前账号改密（/me/password）：must_change_password 置位期间服务端只放行登出/刷新/改密，
// 改密同时清掉强制改密标记并撤销全部 refresh token、下发新会话，前端不需要重新登录。
// web 的 services/api/account.ts 不在 admin 复用白名单里，这里按同一契约自己封装。
export function changeMyPassword(input: { oldPassword: string; newPassword: string }) {
    return apiRequest<void>("/me/password", { method: "POST", body: input });
}

export function listAdminRoles(signal?: AbortSignal) {
    return apiRequest<{ items: AdminRole[] }>("/admin/roles", { signal });
}

export function createAdminRole(input: { key: string; name: string; description: string }) {
    return apiRequest<AdminRole>("/admin/roles", { method: "POST", body: input });
}

// permissions 是全量替换：不传该字段表示不动权限，传空数组表示清空。
export function updateAdminRole(key: string, input: { name?: string; description?: string; permissions?: string[] }) {
    return apiRequest<AdminRole>(`/admin/roles/${encodeURIComponent(key)}`, { method: "PATCH", body: input });
}

export function deleteAdminRole(key: string) {
    return apiRequest<void>(`/admin/roles/${encodeURIComponent(key)}`, { method: "DELETE" });
}

// 权限目录只读：界面上只能把权限分配给角色，不能增删权限点本身。
export function listAdminPermissions(signal?: AbortSignal) {
    return apiRequest<{ items: AdminPermissionGroup[] }>("/admin/permissions", { signal });
}

// roleKey 传 null 表示取消该用户的后台角色。改自己、让系统角色失去最后一个 active 用户会被服务端拒绝。
export function assignAdminUserRole(id: string, roleKey: string | null) {
    return apiRequest<{ id: string; roleKey: string | null }>(`/admin/users/${id}/role`, { method: "PATCH", body: { roleKey } });
}
