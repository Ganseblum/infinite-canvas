import { create } from "zustand";

import { login as loginRequest, logout as logoutRequest, register as registerRequest, type AuthUser, type Plan, type RegisterInput, type SessionPayload } from "@/services/api/auth";
import { refreshSession } from "@/services/api/client";

/** 登录态三阶段：booting 表示正在用 refresh cookie 恢复会话，路由守卫据此等待而非闪跳登录页。 */
export type AuthStatus = "booting" | "authenticated" | "unauthenticated";

type AuthStore = {
    status: AuthStatus;
    user: AuthUser | null;
    plan: Plan | null;
    accessToken: string | null; // 只存内存，不持久化
    bootstrap: () => Promise<void>;
    login: (account: string, password: string) => Promise<void>;
    register: (input: RegisterInput) => Promise<void>;
    logout: () => Promise<void>;
    setSession: (session: SessionPayload) => void;
    clearSession: () => void;
};

// 模块级单飞：开发模式 StrictMode 下 effect 重复执行也只会刷新一次。
let bootstrapPromise: Promise<void> | null = null;

/**
 * 全局登录态 store：管用户信息、套餐、access token 与三阶段登录状态，
 * 路由守卫（RequireAuth）、请求层（client.ts 注入 Bearer）与各页面读取。
 * 不持久化：token 只存内存，刷新后靠 refresh cookie 走 bootstrap 恢复会话。
 */
export const useAuthStore = create<AuthStore>()((set) => ({
    status: "booting",
    user: null,
    plan: null,
    accessToken: null,
    /** 应用挂载后恢复会话：调用 refresh 接口，成功则重建登录态，失败则落到未登录。 */
    bootstrap: () => {
        if (!bootstrapPromise) {
            bootstrapPromise = refreshSession()
                .then(() => undefined)
                .catch(() => {
                    useAuthStore.getState().clearSession();
                });
        }
        return bootstrapPromise;
    },
    login: async (account, password) => {
        const session = await loginRequest(account, password);
        set({ status: "authenticated", user: session.user, plan: session.plan, accessToken: session.accessToken });
    },
    register: async (input) => {
        const session = await registerRequest(input);
        set({ status: "authenticated", user: session.user, plan: session.plan, accessToken: session.accessToken });
    },
    logout: async () => {
        try {
            await logoutRequest();
        } catch {
            // 登出接口失败也要清空本地登录态，避免用户卡在已登录界面。
        } finally {
            set({ status: "unauthenticated", user: null, plan: null, accessToken: null });
        }
    },
    setSession: (session) => set({ status: "authenticated", user: session.user, plan: session.plan, accessToken: session.accessToken }),
    clearSession: () => set({ status: "unauthenticated", user: null, plan: null, accessToken: null }),
}));
