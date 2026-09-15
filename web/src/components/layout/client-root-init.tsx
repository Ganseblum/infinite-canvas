import type { ReactNode } from "react";
import { useEffect } from "react";

import { usePromptSourceScheduler } from "@/hooks/use-prompt-source-scheduler";
import { useAuthStore } from "@/stores/use-auth-store";
import { useModelCatalogStore } from "@/stores/use-model-catalog-store";

// 登录后预加载平台模型目录，生成入口的就绪条件就是「已登录且目录已加载」。
export function ClientRootInit({ children }: { children: ReactNode }) {
    usePromptSourceScheduler();
    const isAuthenticated = useAuthStore((state) => state.status === "authenticated");
    const load = useModelCatalogStore((state) => state.load);

    useEffect(() => {
        if (isAuthenticated) void load();
    }, [isAuthenticated, load]);

    return <>{children}</>;
}
