import type { ReactNode } from "react";

import { AgentPanel } from "@/components/agent/agent-panel";
import { AppTopNav } from "@/components/layout/app-top-nav";
import { SiteNotice } from "@/components/layout/site-notice";

// 登录后主站的整体布局：顶部导航 + 公告条 + 页面内容，右侧挂全局 Agent 面板。
export default function UserLayout({ children }: { children: ReactNode }) {
    return (
        <div className="flex h-dvh overflow-hidden bg-background text-foreground">
            <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
                <AppTopNav />
                <SiteNotice />
                <div className="min-h-0 flex-1 overflow-hidden">{children}</div>
            </div>
            <AgentPanel />
        </div>
    );
}
