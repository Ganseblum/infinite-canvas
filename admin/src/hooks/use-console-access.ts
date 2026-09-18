import { useQuery } from "@tanstack/react-query";

import { ApiError } from "@/lib/api-error";
import type { AuthUser } from "@/services/api/auth";
import { useAuthStore } from "@/stores/use-auth-store";
import { getAdminMe } from "@admin/services/api/admin";

// 后台访问判定的唯一入口：路由守卫、登录页、无权限说明页都只从这里取结论。
// 以前 require-admin.tsx 与 sign-in-required.tsx 各判一次登录态，两处会漂移。
//
// 能否进后台由 GET /api/admin/me 决定（服务端按 role_key + 权限点判定），不再看 users.role 这个
// 保守投影列：自定义角色在投影列上是 user，但确实拥有后台权限。
//
// status === "booting" 必须单独成态：bootstrap 的静默刷新还没回来时身份未定，
// 调用方只显示 loading，既不放行也不跳转，否则已登录用户刷新页面会先闪一下登录页。
// /admin/me 在途时同样返回 booting：身份已定但后台权限未定，结论只有一个字「等」。
export type ConsoleAccess =
    | { phase: "booting"; user: null }
    | { phase: "signed-out"; user: null }
    // 强制改密是独立于权限的闸门：must_change_password 置位时 /admin/me 一律 403
    // PASSWORD_CHANGE_REQUIRED（服务端该拦截挂在 admin 权限判定之前，与有没有后台角色无关），
    // 必须与「无后台权限」分开成态，守卫把用户送去改密页而不是「无权限」说明页。
    | { phase: "must-change-password"; user: AuthUser }
    | { phase: "no-console-access"; user: AuthUser }
    | { phase: "admin"; user: AuthUser; role: { key: string; name: string; isSystem: boolean }; permissions: string[] };

// web 的 AuthUser 类型没声明 mustChangePassword，但登录/刷新响应的 user 对象里实际带着它
//（server 的 userPayload 一直下发），admin 在本地补上这个可选字段读会话状态，不改白名单里的 web 文件。
export type AuthUserWithGate = AuthUser & { mustChangePassword?: boolean };

export function useConsoleAccess(): ConsoleAccess {
    const status = useAuthStore((state) => state.status);
    const user = useAuthStore((state) => state.user);
    // 强制改密闸门先看会话状态：置位时服务端只放行登出/刷新/改密，/admin/me 注定 403，
    // 守卫直接送改密页，一个多余的请求都不发。
    const mustChangePassword = (user as AuthUserWithGate | null)?.mustChangePassword === true;
    const signedIn = status === "authenticated" && !!user;

    const meQuery = useQuery({
        queryKey: ["admin", "me"],
        queryFn: async ({ signal }) => {
            try {
                return await getAdminMe(signal);
            } catch (error) {
                // 401 说明会话已失效（client 的刷新重放也没救回来）：清掉本地登录态，
                // 守卫会落到 /login，而不是拿着一个已经无效的 user 反复请求。
                if (error instanceof ApiError && error.status === 401) useAuthStore.getState().clearSession();
                throw error;
            }
        },
        // 闸门放下时 /admin/me 必然 403，不发这个注定失败的请求；改密成功清除标记后 enabled 翻回
        // true，这里会自动重新拉取。
        enabled: signedIn && !mustChangePassword,
        // 权限每请求由服务端现算，前端不长期缓存结论：重新聚焦窗口时重新确认一次，
        // 权限被调整后菜单不会一直停在旧状态。
        staleTime: 0,
        refetchOnWindowFocus: true,
        // 403 / 401 是结论不是故障：没有后台角色或权限不匹配，重试多少次都一样。
        // 网络抖动这类非结论性错误才重试两次，避免一次抖动就把管理员送进「无权限」页。
        retry: (failureCount, error) => {
            const status = error instanceof ApiError ? error.status : 0;
            if (status === 403 || status === 401) return false;
            return failureCount < 2;
        },
    });

    if (status === "booting") return { phase: "booting", user: null };
    // 已登录却没有 user 属于异常快照，按未登录处理，避免后面把 user 当非空用。
    if (status === "unauthenticated" || !user) return { phase: "signed-out", user: null };
    if (meQuery.error instanceof ApiError && meQuery.error.status === 401) return { phase: "signed-out", user: null };
    // 会话状态里的强制改密标记：守卫据此直接送改密页。
    if (mustChangePassword) return { phase: "must-change-password", user };
    // 兜底：会话标记缺席但服务端闸门已放下（比如管理员在会话进行中才给这个账号置位），403 PASSWORD_CHANGE_REQUIRED
    // 同样是「闸门放下」不是「无权限」，改密是唯一出路。
    if (meQuery.error instanceof ApiError && meQuery.error.code === "PASSWORD_CHANGE_REQUIRED") return { phase: "must-change-password", user };
    if (meQuery.isError) return { phase: "no-console-access", user };
    if (!meQuery.data) return { phase: "booting", user: null };
    return { phase: "admin", user, role: meQuery.data.role, permissions: meQuery.data.permissions };
}
