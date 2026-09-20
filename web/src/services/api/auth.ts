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

// 认证接口客户端（/api/v1/auth/*）：登录成功后 access token 存内存，refresh token 只在 cookie。
// 统一返回 SessionPayload（用户 + 套餐 + access token）。

/** 注册成功即建立会话。POST /api/v1/auth/register。 */
export function register(input: RegisterInput) {
    return apiRequest<SessionPayload>("/auth/register", { method: "POST", body: input });
}

/** 账号（邮箱或用户名）+ 密码登录。POST /api/v1/auth/login。 */
export function login(account: string, password: string) {
    return apiRequest<SessionPayload>("/auth/login", { method: "POST", body: { account, password } });
}

/** 登出：服务端吊销 refresh cookie；调用方仍需清空本地登录态。 */
export function logout() {
    return apiRequest<void>("/auth/logout", { method: "POST" });
}

// 与 client.ts 的静默刷新共用同一个单飞 Promise，避免并发轮换互相作废。
export function refresh() {
    return refreshSession();
}

/** 发送邮箱验证邮件（登录态下）。 */
export function sendVerifyEmail() {
    return apiRequest<void>("/auth/verify-email/send", { method: "POST" });
}

/** 凭邮件里的 token 完成邮箱验证，返回更新后的用户信息。 */
export function verifyEmail(token: string) {
    return apiRequest<{ user: AuthUser }>("/auth/verify-email", { method: "POST", body: { token } });
}

/** 忘记密码：向邮箱发送重置链接。 */
export function forgotPassword(email: string) {
    return apiRequest<void>("/auth/password/forgot", { method: "POST", body: { email } });
}

/** 凭邮件 token 重置密码。 */
export function resetPassword(token: string, password: string) {
    return apiRequest<void>("/auth/password/reset", { method: "POST", body: { token, password } });
}
