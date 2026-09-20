import { useEffect, useState } from "react";
import { CheckCircle2, ChevronDown, ChevronRight, FileText, LoaderCircle, Search, SquareTerminal, XCircle } from "lucide-react";
import { Streamdown } from "streamdown";

import { useOfficeStore } from "@/stores/office";

const streamdownProps = {
    controls: { code: { copy: true, download: false }, table: { copy: true, download: false, fullscreen: false } },
    lineNumbers: false,
} as const;

const toolIcon: Record<string, typeof Search> = { web_search: Search, read_file: FileText, run_command: SquareTerminal };

const errorText: Record<string, string> = {
    cancelled: "已取消",
    stalled: "生成超时已终止，可重试",
    runtime_lost: "执行中断，可重试",
    runtime_unreachable: "执行环境暂不可达，可重试",
    upstream_timeout: "上游超时，建议换模型重试",
    orphan_reclaimed: "执行中断，可重试",
    credits_exhausted: "点数不足，请充值后重试",
};

// 流式 run 卡（前端方案 §3）：状态条 + 工具过程卡 + 流式正文；终态后由快照整体替换（D3）。
export function RunCard() {
    const streamStatus = useOfficeStore((s) => s.streamStatus);
    const startedAt = useOfficeStore((s) => s.startedAt);
    const errorCode = useOfficeStore((s) => s.errorCode);
    const displayText = useOfficeStore((s) => s.displayText);
    const tools = useOfficeStore((s) => s.tools);
    const [, force] = useState(0);

    // running 时每秒重渲染一次，驱动耗时计时。
    useEffect(() => {
        if (streamStatus !== "running") return;
        const timer = window.setInterval(() => force((n) => n + 1), 1000);
        return () => window.clearInterval(timer);
    }, [streamStatus]);

    const runningTool = [...tools].reverse().find((t) => t.status === "running");
    const elapsed = startedAt ? Math.max(1, Math.round((Date.now() - startedAt) / 1000)) : 0;

    return (
        <div className="rounded-xl border border-border bg-card/60 px-4 py-3">
            <div className="flex items-center gap-2 text-[13px]">
                {streamStatus === "queued" && <span className="size-2 rounded-full bg-amber-500" />}
                {streamStatus === "running" && <LoaderCircle className="size-3.5 animate-spin text-foreground" />}
                {(streamStatus === "failed" || streamStatus === "cancelled") && <XCircle className={`size-3.5 ${errorCode === "cancelled" ? "text-muted-foreground" : "text-red-500"}`} />}
                <span className="text-muted-foreground">
                    {streamStatus === "queued" && "排队中…"}
                    {streamStatus === "running" && `执行中 · ${elapsed}s${runningTool ? ` · ${runningTool.name}` : ""}`}
                    {(streamStatus === "failed" || streamStatus === "cancelled") && (errorText[errorCode ?? ""] ?? "执行失败")}
                </span>
                {streamStatus === "running" && <span className="ml-auto size-1.5 animate-pulse rounded-full bg-foreground/60" />}
            </div>

            {tools.length > 0 && (
                <div className="mt-2.5 space-y-1">
                    {tools.map((tool) => (
                        <ToolCallCard key={tool.toolCallId} tool={tool} />
                    ))}
                </div>
            )}

            {displayText && (
                <div className="mt-3 min-w-0 text-[15px] leading-7 [&_table]:my-3 [&_table]:w-full [&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1">
                    <Streamdown {...streamdownProps}>{displayText}</Streamdown>
                </div>
            )}
        </div>
    );
}

function ToolCallCard({ tool }: { tool: { toolCallId: string; name: string; inputPreview?: string; outputPreview?: string; status: "running" | "ok" | "error" } }) {
    const [open, setOpen] = useState(false);
    const Icon = toolIcon[tool.name] ?? SquareTerminal;
    return (
        <div className="overflow-hidden rounded-lg border border-border bg-background">
            <button type="button" onClick={() => setOpen((v) => !v)} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs transition hover:bg-muted/40">
                {tool.status === "running" ? <LoaderCircle className="size-3 shrink-0 animate-spin" /> : tool.status === "ok" ? <CheckCircle2 className="size-3 shrink-0 text-emerald-600" /> : <XCircle className="size-3 shrink-0 text-red-500" />}
                <Icon className="size-3 shrink-0 text-muted-foreground" />
                <span className="font-medium">{tool.name}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{tool.inputPreview}</span>
                {open ? <ChevronDown className="size-3 shrink-0 text-muted-foreground" /> : <ChevronRight className="size-3 shrink-0 text-muted-foreground" />}
            </button>
            {open && (
                <div className="space-y-1.5 border-t border-border px-3 py-2 font-mono text-[11px] leading-relaxed text-muted-foreground">
                    <div>
                        <span className="text-foreground/70">in › </span>
                        {tool.inputPreview}
                    </div>
                    {tool.outputPreview && (
                        <div>
                            <span className="text-foreground/70">out ‹ </span>
                            {tool.outputPreview}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
