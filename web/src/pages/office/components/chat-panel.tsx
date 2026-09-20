import { useEffect, useRef } from "react";
import { Code2, FilePenLine, Search, Table2 } from "lucide-react";
import { Streamdown } from "streamdown";

import { useOfficeStore } from "@/stores/office";
import type { MessageItem } from "@/services/api/office";

import { Composer } from "./composer";
import { RunCard } from "./run-card";

const streamdownProps = {
    controls: { code: { copy: true, download: false }, table: { copy: true, download: false, fullscreen: false } },
    lineNumbers: false,
} as const;

// 中区对话面板（前端方案 §3）：欢迎空态 / 消息流 / 流式 run 卡 / 输入区。
export function ChatPanel() {
    const messages = useOfficeStore((s) => s.messages);
    const messagesLoading = useOfficeStore((s) => s.messagesLoading);
    const streamStatus = useOfficeStore((s) => s.streamStatus);
    const streamRunId = useOfficeStore((s) => s.streamRunId);
    const lastUsage = useOfficeStore((s) => s.lastUsage);
    const connected = useOfficeStore((s) => s.connected);
    const sendError = useOfficeStore((s) => s.sendError);
    const scrollRef = useRef<HTMLDivElement>(null);
    const streaming = streamStatus === "queued" || streamStatus === "running";

    // 新消息/流式增长时贴底。
    const stickToBottom = useRef(true);
    useEffect(() => {
        const el = scrollRef.current;
        if (el && stickToBottom.current) el.scrollTop = el.scrollHeight;
    });

    return (
        <div className="flex min-h-0 flex-1 flex-col">
            {sendError && <div className="shrink-0 bg-red-500/10 px-6 py-1.5 text-center text-[11px] text-red-600 dark:text-red-400">{sendError}</div>}
            {!connected && <div className="shrink-0 bg-muted px-6 py-1.5 text-center text-[11px] text-muted-foreground">连接中断，正在重连…</div>}
            <div
                ref={scrollRef}
                className="min-h-0 flex-1 overflow-y-auto"
                onScroll={(e) => {
                    const el = e.currentTarget;
                    stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
                }}
            >
                {messagesLoading ? (
                    <div className="mx-auto max-w-3xl space-y-3 px-6 pt-10">
                        {[0, 1, 2].map((i) => (
                            <div key={i} className="h-4 animate-pulse rounded bg-muted" style={{ width: `${72 - i * 14}%` }} />
                        ))}
                    </div>
                ) : messages.length === 0 && !streamRunId ? (
                    <Welcome />
                ) : (
                    <div className="mx-auto max-w-3xl space-y-5 px-6 py-8">
                        {messages.map((message, index) => (
                            <MessageItem key={message.id} message={message} usage={index === messages.length - 1 && message.role === "assistant" ? lastUsage : null} />
                        ))}
                        {streamRunId && streamStatus !== "succeeded" && <RunCard />}
                    </div>
                )}
            </div>
            <Composer />
        </div>
    );
}

function Welcome() {
    const setDraft = useOfficeStore((s) => s.setDraft);
    const scenes = [
        { icon: FilePenLine, title: "周报整理", prompt: "把本周零散笔记整理成一份周报，按项目分组、标注风险。" },
        { icon: Table2, title: "表格汇总", prompt: "上传了九月费用明细，帮我按类别汇总，给占比和要点。" },
        { icon: Search, title: "竞品调研", prompt: "帮我调研一下国内外 AI 办公助手的产品形态，输出一份简报，要有对比表格和结论。" },
        { icon: Code2, title: "代码修复", prompt: "工作区里的脚本跑不通，帮我修复并解释原因。" },
    ];
    return (
        <div className="mx-auto flex h-full max-w-2xl flex-col items-center justify-center px-6 pb-16">
            <div className="flex size-11 items-center justify-center rounded-lg bg-foreground font-serif text-lg text-background">办</div>
            <h1 className="mt-5 text-2xl font-medium tracking-tight" style={{ fontFamily: '"Noto Serif SC", "Songti SC", serif' }}>
                灯已开，把活儿交给我。
            </h1>
            <p className="mt-2 text-sm text-muted-foreground">会话、过程与成果都留在你的工作区里，随时回来接着排。</p>
            <div className="mt-8 grid w-full grid-cols-1 gap-2.5 sm:grid-cols-2">
                {scenes.map((scene) => (
                    <button
                        key={scene.title}
                        type="button"
                        onClick={() => setDraft(scene.prompt)}
                        className="group flex items-start gap-3 rounded-lg border border-border bg-card px-3.5 py-3 text-left transition hover:border-foreground/25 hover:bg-muted/40"
                    >
                        <scene.icon className="mt-0.5 size-4 shrink-0 text-muted-foreground transition group-hover:text-foreground" />
                        <span className="min-w-0">
                            <span className="block text-[13px] font-medium">{scene.title}</span>
                            <span className="mt-0.5 block truncate text-xs text-muted-foreground">{scene.prompt}</span>
                        </span>
                    </button>
                ))}
            </div>
        </div>
    );
}

function MessageItem({ message, usage }: { message: MessageItem; usage: { inputTokens: number; outputTokens: number; credits: number } | null }) {
    if (message.role === "user") {
        return (
            <div className="flex justify-end">
                <div className="max-w-[75%] rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-[14px] leading-relaxed text-primary-foreground">{message.text}</div>
            </div>
        );
    }
    return (
        <div className="min-w-0">
            <div className="text-[15px] leading-7 [&_table]:my-3 [&_table]:w-full [&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1">
                <Streamdown {...streamdownProps}>{message.text}</Streamdown>
            </div>
            {usage && (
                <div className="mt-1.5 text-[11px] text-muted-foreground">
                    ↑ {usage.inputTokens.toLocaleString()} · ↓ {usage.outputTokens.toLocaleString()} tokens · {usage.credits.toFixed(1)} 点
                </div>
            )}
        </div>
    );
}
