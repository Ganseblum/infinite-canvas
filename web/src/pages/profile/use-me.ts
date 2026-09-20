import { useQuery } from "@tanstack/react-query";

import { getMe } from "@/services/api/account";
import { useAuthStore } from "@/stores/use-auth-store";

/**
 * 个人中心页面私有 hook：查询当前登录用户信息（react-query 包装）。
 * key 里带上 userId，切换账号后缓存自动隔离；未登录时不发请求。
 */
export function useMeQuery() {
    const status = useAuthStore((state) => state.status);
    const userId = useAuthStore((state) => state.user?.id ?? null);

    return useQuery({
        queryKey: ["me", userId],
        queryFn: ({ signal }) => getMe(signal),
        enabled: status === "authenticated",
    });
}
