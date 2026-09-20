import { useRef } from "react";
import { ArrowUp, Paperclip, Square } from "lucide-react";

import { useOfficeStore } from "@/stores/office";

// 输入区（前端方案 §8）：Enter 发送 / Shift+Enter 换行；运行中变停止钮；附件为 M2 占位。
export function Composer() {
    const draft = useOfficeStore((s) => s.draft);
    const setDraft = useOfficeStore((s) => s.setDraft);
    const send = useOfficeStore((s) => s.send);
    const cancelRun = useOfficeStore((s) => s.cancelRun);
    const streamStatus = useOfficeStore((s) => s.streamStatus);
    const currentSessionId = useOfficeStore((s) => s.currentSessionId);
    const textareaRef = useRef<HTMLTextAreaElement>(null);

    const busy = streamStatus === "queued" || streamStatus === "running";

    const submit = () => {
        const content = draft.trim();
        if (!content || busy || !currentSessionId) return;
        send(content);
        textareaRef.current?.focus();
    };

    return (
        <div className="shrink-0 border-t border-border bg-background/80 px-6 py-3.5 backdrop-blur">
            <div className="mx-auto max-w-3xl">
                <div className="flex items-end gap-2 rounded-xl border border-border bg-card px-3 py-2 transition focus-within:border-foreground/30">
                    <button
                        type="button"
                        disabled
                        title="附件上传将在 M2 开通"
                        className="mb-0.5 flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground/50"
                    >
                        <Paperclip className="size-4" />
                    </button>
                    <textarea
                        ref={textareaRef}
                        value={draft}
                        onChange={(e) => setDraft(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                                e.preventDefault();
                                submit();
                            }
                        }}
                        rows={1}
                        placeholder={currentSessionId ? "把活儿交给我，Enter 发送，Shift+Enter 换行" : "先新建或选择一个会话"}
                        className="max-h-44 min-h-6 flex-1 resize-none bg-transparent py-1 text-[14px] leading-6 outline-none placeholder:text-muted-foreground/60"
                    />
                    {busy ? (
                        <button
                            type="button"
                            onClick={cancelRun}
                            title="停止"
                            className="mb-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-foreground text-background transition hover:opacity-85"
                        >
                            <Square className="size-3 fill-current" />
                        </button>
                    ) : (
                        <button
                            type="button"
                            onClick={submit}
                            disabled={!draft.trim() || !currentSessionId}
                            title="发送"
                            className="mb-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground transition hover:opacity-85 disabled:opacity-30"
                        >
                            <ArrowUp className="size-4" />
                        </button>
                    )}
                </div>
                <div className="mt-1.5 text-center text-[11px] text-muted-foreground/70">内容由 AI 生成，请注意核对</div>
            </div>
        </div>
    );
}
