import { useQuery } from "@tanstack/react-query";

import { getMe } from "@/services/api/account";
import { useAuthStore } from "@/stores/use-auth-store";

export function useMeQuery() {
    const status = useAuthStore((state) => state.status);
    const userId = useAuthStore((state) => state.user?.id ?? null);

    return useQuery({
        queryKey: ["me", userId],
        queryFn: ({ signal }) => getMe(signal),
        enabled: status === "authenticated",
    });
}
