import { afterEach, describe, expect, it, vi } from "vitest";

const user = {
    id: "user-1",
    email: "alpha@example.com",
    username: "alpha",
    displayName: "Alpha",
    avatarUrl: "",
    role: "user",
    emailVerified: true,
};

const session = { user, accessToken: "new-token", plan: { id: "free", name: "免费档" } };

function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// 让 429 之外的并发 401 都排到同一个刷新 Promise 上，再返回刷新结果。
function holdRefresh() {
    return new Promise((resolve) => setTimeout(resolve, 10));
}

afterEach(() => {
    vi.unstubAllGlobals();
});

describe("API 客户端刷新单飞", () => {
    it("并发 TOKEN_EXPIRED 只刷新一次，且全部请求重放成功", async () => {
        vi.resetModules();
        const { apiRequest } = await import("@/services/api/client");
        const { useAuthStore } = await import("@/stores/use-auth-store");

        useAuthStore.setState({ status: "authenticated", user, plan: null, accessToken: "old-token" });

        let refreshCalls = 0;
        let apiCalls = 0;
        const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
            const url = String(input);
            if (url.endsWith("/api/auth/refresh")) {
                refreshCalls += 1;
                await holdRefresh();
                return jsonResponse(session);
            }
            apiCalls += 1;
            if (apiCalls <= 3) return jsonResponse({ error: { code: "TOKEN_EXPIRED", message: "expired" } }, 401);
            return jsonResponse({ ok: url });
        });
        vi.stubGlobal("fetch", fetchMock);

        const results = await Promise.all([apiRequest<{ ok: string }>("/alpha"), apiRequest<{ ok: string }>("/beta"), apiRequest<{ ok: string }>("/gamma")]);

        expect(refreshCalls).toBe(1);
        expect(apiCalls).toBe(6);
        expect(results.map((result) => result.ok)).toEqual([expect.stringContaining("/api/alpha"), expect.stringContaining("/api/beta"), expect.stringContaining("/api/gamma")]);
        expect(useAuthStore.getState().status).toBe("authenticated");
        expect(useAuthStore.getState().accessToken).toBe("new-token");
    });

    it("刷新失败时全部请求以同一错误 reject，且登录态被清空", async () => {
        vi.resetModules();
        const { apiRequest } = await import("@/services/api/client");
        const { useAuthStore } = await import("@/stores/use-auth-store");
        const { ApiError } = await import("@/lib/api-error");

        useAuthStore.setState({ status: "authenticated", user, plan: null, accessToken: "old-token" });

        let refreshCalls = 0;
        const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
            const url = String(input);
            if (url.endsWith("/api/auth/refresh")) {
                refreshCalls += 1;
                await holdRefresh();
                return jsonResponse({ error: { code: "UNAUTHORIZED", message: "refresh rejected" } }, 401);
            }
            return jsonResponse({ error: { code: "TOKEN_EXPIRED", message: "expired" } }, 401);
        });
        vi.stubGlobal("fetch", fetchMock);

        const results = await Promise.allSettled([apiRequest("/alpha"), apiRequest("/beta")]);
        const rejected = results.filter((result): result is PromiseRejectedResult => result.status === "rejected");

        expect(rejected).toHaveLength(2);
        expect(refreshCalls).toBe(1);
        expect(new Set(rejected.map((result) => `${result.reason.code}:${result.reason.status}`)).size).toBe(1);
        for (const result of rejected) {
            expect(result.reason).toBeInstanceOf(ApiError);
            expect(result.reason.code).toBe("TOKEN_EXPIRED");
            expect(result.reason.status).toBe(401);
        }
        expect(useAuthStore.getState().status).toBe("unauthenticated");
        expect(useAuthStore.getState().user).toBeNull();
        expect(useAuthStore.getState().accessToken).toBeNull();
    });
});
