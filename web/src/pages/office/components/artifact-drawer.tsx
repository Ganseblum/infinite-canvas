import { Code2, FileText, X } from "lucide-react";
import { Streamdown } from "streamdown";

import { useOfficeStore } from "@/stores/office";

const kindIcon = { markdown: FileText, code: Code2, html: Code2, file: FileText } as const;

const streamdownProps = {
    controls: { code: { copy: true, download: false }, table: { copy: true, download: false, fullscreen: false } },
    lineNumbers: false,
} as const;

// 右侧成果抽屉（前端方案 §3）：会话产物列表 + 详情查看；markdown 走净化渲染管线，
// code/html/file 只给等宽源码视图（评审 F7 修复项：不渲染富内容）。
export function ArtifactDrawerBody() {
    const artifacts = useOfficeStore((s) => s.artifacts);
    const activeArtifactId = useOfficeStore((s) => s.activeArtifactId);
    const openArtifact = useOfficeStore((s) => s.openArtifact);
    const toggleDrawer = useOfficeStore((s) => s.toggleDrawer);
    const active = artifacts.find((a) => a.id === activeArtifactId);

    return (
        <>
            <div className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
                <span className="text-sm font-medium">成果</span>
                <span className="rounded-full bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{artifacts.length}</span>
                <button type="button" onClick={() => toggleDrawer(false)} className="ml-auto flex size-7 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground">
                    <X className="size-4" />
                </button>
            </div>
            {artifacts.length === 0 ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-2 px-8 text-center text-xs text-muted-foreground">
                    <FileText className="size-6 text-muted-foreground/50" />
                    运行产出的文档、表格、代码会落在这里
                </div>
            ) : (
                <div className="flex min-h-0 flex-1">
                    <div className="w-44 shrink-0 space-y-0.5 overflow-y-auto border-r border-border p-2">
                        {artifacts.map((artifact) => {
                            const Icon = kindIcon[artifact.kind];
                            const itemActive = artifact.id === activeArtifactId;
                            return (
                                <button
                                    key={artifact.id}
                                    type="button"
                                    onClick={() => openArtifact(artifact.id)}
                                    className={`block w-full rounded-md px-2 py-1.5 text-left transition ${itemActive ? "bg-muted" : "hover:bg-muted/50"}`}
                                >
                                    <span className="flex items-center gap-1.5">
                                        <Icon className="size-3 shrink-0 text-muted-foreground" />
                                        <span className="min-w-0 flex-1 truncate text-xs">{artifact.name}</span>
                                    </span>
                                    <span className="mt-0.5 block pl-[18px] text-[10px] text-muted-foreground/70">{formatSize(artifact.size)} · {artifact.kind}</span>
                                </button>
                            );
                        })}
                    </div>
                    <div className="min-w-0 flex-1 overflow-y-auto p-4">
                        {active ? (
                            active.kind === "markdown" ? (
                                <div className="min-w-0 text-[14px] leading-7 [&_table]:my-3 [&_table]:w-full [&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1">
                                    <Streamdown {...streamdownProps}>{active.text}</Streamdown>
                                </div>
                            ) : (
                                <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground/85">{active.text || "（内容在工作区，M2 接入下载）"}</pre>
                            )
                        ) : (
                            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">选择左侧产物查看</div>
                        )}
                    </div>
                </div>
            )}
        </>
    );
}

function formatSize(size: number) {
    if (size < 1024) return `${size} B`;
    return `${(size / 1024).toFixed(1)} KB`;
}
