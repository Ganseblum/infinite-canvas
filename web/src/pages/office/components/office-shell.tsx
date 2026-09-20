import { SquarePen } from "lucide-react";

import { useOfficeStore } from "@/stores/office";

import { ArtifactDrawerBody } from "./artifact-drawer";
import { ChatPanel } from "./chat-panel";
import { SessionList } from "./session-list";

// 三栏骨架（前端方案 §2）：左会话栏 / 中对话区 / 右产物抽屉；配色全部平台 token（D4）。
export function OfficeShell() {
    const drawerOpen = useOfficeStore((s) => s.drawerOpen);
    const newSession = useOfficeStore((s) => s.newSession);

    return (
        <div className="flex h-full min-h-0">
            <aside className="flex w-64 shrink-0 flex-col border-r border-border bg-muted/30 max-lg:w-16">
                <div className="flex items-center gap-2.5 px-4 pb-3 pt-4 max-lg:justify-center max-lg:px-0">
                    <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-foreground font-serif text-sm text-background">办</div>
                    <div className="min-w-0 max-lg:hidden">
                        <div className="truncate text-sm font-semibold leading-tight">AI 办公</div>
                        <div className="truncate text-[11px] leading-tight text-muted-foreground">深夜排字房 · 内测</div>
                    </div>
                </div>
                <div className="px-3 pb-2 max-lg:flex max-lg:justify-center max-lg:px-0">
                    <button
                        type="button"
                        onClick={() => void newSession()}
                        className="inline-flex w-full items-center justify-center gap-1.5 rounded-md bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground transition hover:opacity-85 max-lg:w-8 max-lg:px-0"
                    >
                        <SquarePen className="size-3.5" />
                        <span className="max-lg:hidden">新会话</span>
                    </button>
                </div>
                <SessionList />
                <div className="border-t border-border px-4 py-2.5 text-[11px] leading-relaxed text-muted-foreground max-lg:hidden">会话与成果保存在服务端工作区</div>
            </aside>

            <main className="flex min-w-0 flex-1 flex-col">
                <ChatPanel />
            </main>

            {drawerOpen && (
                <div className="fixed inset-y-0 right-0 z-40 flex w-[min(92vw,440px)] flex-col border-l border-border bg-background shadow-2xl lg:static lg:z-auto lg:w-[420px] lg:shadow-none">
                    <ArtifactDrawerBody />
                </div>
            )}
        </div>
    );
}
