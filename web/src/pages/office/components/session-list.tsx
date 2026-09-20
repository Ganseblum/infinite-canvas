import { useNavigate } from "react-router";
import { LoaderCircle } from "lucide-react";

import { useOfficeStore } from "@/stores/office";

// 左栏会话列表（前端方案 §3）：分页略（原型数据量小），点击走 URL 驱动选中。
export function SessionList() {
    const navigate = useNavigate();
    const sessions = useOfficeStore((s) => s.sessions);
    const sessionsLoading = useOfficeStore((s) => s.sessionsLoading);
    const currentSessionId = useOfficeStore((s) => s.currentSessionId);

    if (sessionsLoading) {
        return (
            <div className="flex flex-1 items-center justify-center text-muted-foreground">
                <LoaderCircle className="size-4 animate-spin" />
            </div>
        );
    }

    return (
        <nav className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 py-1">
            {sessions.map((session) => {
                const active = session.id === currentSessionId;
                return (
                    <button
                        key={session.id}
                        type="button"
                        onClick={() => navigate(`/office/s/${session.id}`)}
                        className={`block w-full rounded-md px-2.5 py-2 text-left transition max-lg:px-0 ${active ? "bg-background shadow-sm ring-1 ring-border" : "hover:bg-background/60"}`}
                    >
                        <div className={`truncate text-[13px] leading-tight max-lg:hidden ${active ? "font-medium text-foreground" : "text-foreground/85"}`}>{session.title}</div>
                        <div className="mt-0.5 truncate text-[11px] leading-tight text-muted-foreground max-lg:hidden">{relativeTime(session.updatedAt)}</div>
                        <div className={`mx-auto size-1.5 rounded-full max-lg:block lg:hidden ${active ? "bg-foreground" : "bg-muted-foreground/30"}`} title={session.title} />
                    </button>
                );
            })}
        </nav>
    );
}

function relativeTime(ts: number) {
    const diff = Date.now() - ts;
    if (diff < 60_000) return "刚刚";
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
    return `${Math.floor(diff / 86_400_000)} 天前`;
}
