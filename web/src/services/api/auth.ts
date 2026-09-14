import { apiRequest, refreshSession } from "@/services/api/client";

export type AuthUser = {
    id: string;
    email: string;
    username: string;
    displayName: string;
    avatarUrl: string;
    role: string;
    emailVerified: boolean;
};

export type Plan = {
    id: string;
    name: string;
};

export type SessionPayload = {
    user: AuthUser;
    accessToken: string;
    plan: Plan;
};

export type RegisterInput = {
    email: string;
    username: string;
    password: string;
};

export function register(input: RegisterInput) {
    return apiRequest<SessionPayload>("/auth/register", { method: "POST", body: input });
}

export function login(account: string, password: string) {
    return apiRequest<SessionPayload>("/auth/login", { method: "POST", body: { account, password } });
}

export function logout() {
    return apiRequest<void>("/auth/logout", { method: "POST" });
}

// 与 client.ts 的静默刷新共用同一个单飞 Promise，避免并发轮换互相作废。
export function refresh() {
    return refreshSession();
}

export function sendVerifyEmail() {
    return apiRequest<void>("/auth/verify-email/send", { method: "POST" });
}

export function verifyEmail(token: string) {
    return apiRequest<{ user: AuthUser }>("/auth/verify-email", { method: "POST", body: { token } });
}

export function forgotPassword(email: string) {
    return apiRequest<void>("/auth/password/forgot", { method: "POST", body: { email } });
}

export function resetPassword(token: string, password: string) {
    return apiRequest<void>("/auth/password/reset", { method: "POST", body: { token, password } });
}
