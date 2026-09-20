import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button, Segmented, Tooltip } from "antd";
import copyToClipboard from "copy-to-clipboard";
import { CheckCircle2, ChevronDown, CircleAlert, CircleDot, Copy, Trash2, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

import i18n from "@/i18n";
import { canvasThemes } from "@/lib/canvas-theme";
import type { AgentEventLog } from "@/stores/use-agent-store";
import { formatLogJson, formatLogText, type AgentLogContext } from "./agent-event-formatters";
import { AgentScrollToBottom } from "./agent-scroll-to-bottom";

/** 日志筛选档位：all 为全部，其余按级别过滤。 */
type LogFilter = "all" | "error" | "warning" | "info";
/** 列表渲染用的日志条目：在原始日志上补充了条数合并计数、级别与展示文案等派生字段。 */
type DisplayLog = AgentEventLog & { count: number; detail: string; displayText: string; level: Exclude<LogFilter, "all">; signature: string; success: boolean };
// 距底部 48px 内视为「贴底」：贴底时新日志自动跟随滚动，否则累计未读条数。
const SCROLL_BOTTOM_THRESHOLD = 48;

/**
 * 事件日志视图：顶部连接诊断信息，正文分「诊断文本」与「原始 JSON」两种模式。
 * 文本模式支持按级别筛选、同类日志合并计数、贴底跟随与未读条数提示；
 * 复制失败时兜底聚焦隐藏 textarea 让用户手动选择。
 */
export function AgentLogView({
    logs,
    theme,
    context,
    onClear,
    onCopied,
    onCopyBlocked,
}: {
    logs: AgentEventLog[];
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    context: AgentLogContext;
    onClear: () => void;
    onCopied: (text: string) => void;
    onCopyBlocked: (text: string) => void;
}) {
    const { t } = useTranslation();
    const [mode, setMode] = useState<"text" | "json">("text");
    const [filter, setFilter] = useState<LogFilter>("all");
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const listRef = useRef<HTMLDivElement>(null);
    const followLogsRef = useRef(true);
    const [showScrollToBottom, setShowScrollToBottom] = useState(false);
    const [newLogCount, setNewLogCount] = useState(0);
    const content = mode === "text" ? formatLogText(logs, context) : formatLogJson(logs, context);
    const displayLogs = useMemo(() => prepareLogs(logs), [logs]);
    const counts = useMemo(
        () => ({
            all: displayLogs.reduce((sum, item) => sum + item.count, 0),
            error: displayLogs.filter((item) => item.level === "error").reduce((sum, item) => sum + item.count, 0),
            warning: displayLogs.filter((item) => item.level === "warning").reduce((sum, item) => sum + item.count, 0),
            info: displayLogs.filter((item) => item.level === "info").reduce((sum, item) => sum + item.count, 0),
        }),
        [displayLogs],
    );
    const visibleLogs = filter === "all" ? displayLogs : displayLogs.filter((item) => item.level === filter);
    const visibleLogCount = visibleLogs.reduce((sum, item) => sum + item.count, 0);
    const previousVisibleCountRef = useRef(visibleLogCount);
    const lastError = [...logs].reverse().find((item) => logLevel(item) === "error");
    // 与聊天时间线相同的贴底跟随逻辑：用户上滚即停止跟随并显示「回到底部」。
    const updateScrollState = useCallback(() => {
        const list = listRef.current;
        if (!list) return;
        const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight <= SCROLL_BOTTOM_THRESHOLD;
        followLogsRef.current = atBottom;
        setShowScrollToBottom(!atBottom);
        if (atBottom) setNewLogCount(0);
    }, []);
    const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
        const list = listRef.current;
        if (!list) return;
        followLogsRef.current = true;
        list.scrollTo({ top: list.scrollHeight, behavior });
        setShowScrollToBottom(false);
        setNewLogCount(0);
    }, []);
    // 最后一条日志是 <details>，展开/收起会改变高度：收起后重判贴底；展开且在跟随时等两帧（高度生效后）再滚底。
    const handleLastLogToggle = useCallback((open: boolean) => {
        if (!open) {
            requestAnimationFrame(updateScrollState);
            return;
        }
        if (!followLogsRef.current) return;
        requestAnimationFrame(() => requestAnimationFrame(() => scrollToBottom("auto")));
    }, [scrollToBottom, updateScrollState]);
    useEffect(() => {
        if (mode !== "text") return;
        const frame = requestAnimationFrame(() => scrollToBottom("auto"));
        return () => cancelAnimationFrame(frame);
    }, [filter, mode, scrollToBottom]);
    useEffect(() => {
        const previousCount = previousVisibleCountRef.current;
        const addedCount = Math.max(0, visibleLogCount - previousCount);
        previousVisibleCountRef.current = visibleLogCount;
        if (mode !== "text") return;
        // 条数减少说明发生了清空或筛选变化，未读计数直接归零。
        if (visibleLogCount < previousCount) setNewLogCount(0);
        const frame = requestAnimationFrame(() => {
            // 贴底则跟随滚到最新；未贴底则累计新日志条数用于「回到底部」按钮上的未读提示。
            if (followLogsRef.current) scrollToBottom("auto");
            else {
                if (addedCount) setNewLogCount((count) => count + addedCount);
                updateScrollState();
            }
        });
        return () => cancelAnimationFrame(frame);
    }, [mode, scrollToBottom, updateScrollState, visibleLogCount]);
    // Clipboard API 不可用（如非安全上下文）时退回 textarea 全选，让用户 Ctrl+C 手动复制。
    const copy = async (value = content, tip = t("agent.logs.copied")) => {
        if (await copyToClipboard(value)) {
            onCopied(tip);
            return;
        }
        textareaRef.current?.focus();
        textareaRef.current?.select();
        onCopyBlocked(t(mode === "json" ? "agent.logs.selectedManual" : "agent.logs.copyFailed"));
    };
    const connectionLabel = t(context.connected ? "agent.events.online" : context.enabled ? "agent.events.connecting" : "agent.events.disabled");
    return (
        <div className="min-h-0 flex-1 overflow-hidden px-4 py-3">
            <div className="flex h-full min-h-0 flex-col gap-3">
                <div className="flex items-center justify-between gap-3">
                    <div className="text-base font-semibold leading-6">{t("agent.logs.title")}</div>
                    <Segmented
                        size="small"
                        value={mode}
                        onChange={(value) => setMode(value as "text" | "json")}
                        options={[
                            { label: t("agent.logs.diagnostics"), value: "text" },
                            { label: t("agent.logs.rawJson"), value: "json" },
                        ]}
                    />
                </div>

                <div className="border-y py-2.5" style={{ borderColor: theme.node.stroke }}>
                    <div className="flex items-start gap-2.5">
                        <span className={`mt-1.5 size-2 shrink-0 rounded-full ${context.connected ? "bg-emerald-500" : context.enabled ? "bg-amber-500" : "bg-current opacity-30"}`} />
                        <div className="min-w-0 flex-1">
                            <div className="flex min-w-0 items-center gap-2 text-sm">
                                <span className="font-medium">{connectionLabel}</span>
                                <span className="truncate" style={{ color: theme.node.muted }}>
                                    {context.activity}
                                </span>
                            </div>
                            <div className="mt-0.5 truncate font-mono text-[11px] leading-4" style={{ color: theme.node.faint }} title={context.endpoint}>
                                {context.endpoint}
                            </div>
                        </div>
                        <div className="shrink-0 text-right text-[11px] leading-4" style={{ color: theme.node.muted }}>
                            <div>{t("agent.logs.messages", { count: context.messages })}</div>
                            <div>{context.pendingTool ? t("agent.logs.tool", { tool: context.pendingTool }) : t("agent.logs.noPendingTool")}</div>
                        </div>
                    </div>
                </div>

                {mode === "text" ? (
                    <>
                        <div className="flex items-center justify-between gap-2">
                            <Segmented
                                size="small"
                                value={filter}
                                onChange={(value) => setFilter(value as LogFilter)}
                                options={[
                                    { label: t("agent.logs.all", { count: counts.all }), value: "all" },
                                    { label: t("agent.logs.errors", { count: counts.error }), value: "error" },
                                    { label: t("agent.logs.warnings", { count: counts.warning }), value: "warning" },
                                    { label: t("agent.logs.info", { count: counts.info }), value: "info" },
                                ]}
                            />
                            <LogActions logs={logs} lastError={lastError} onClear={onClear} onCopy={(value, tip) => void copy(value, tip)} context={context} />
                        </div>
                        <div className="relative min-h-0 flex-1">
                            <div ref={listRef} tabIndex={0} aria-label={t("agent.logs.list")} className="thin-scrollbar h-full overflow-y-auto border-y focus-visible:outline-none" style={{ borderColor: theme.node.stroke }} onScroll={updateScrollState}>
                                {visibleLogs.map((item, index) => (
                                    <LogRow key={item.id} item={item} theme={theme} onToggle={index === visibleLogs.length - 1 ? handleLastLogToggle : undefined} />
                                ))}
                                {!visibleLogs.length ? (
                                    <div className="px-3 py-10 text-center text-sm" style={{ color: theme.node.muted }}>
                                        {t(logs.length ? "agent.logs.noFiltered" : "agent.logs.empty")}
                                    </div>
                                ) : null}
                            </div>
                            {showScrollToBottom ? (
                                <AgentScrollToBottom
                                    theme={theme}
                                    title={newLogCount ? t("agent.logs.new", { count: newLogCount }) : t("agent.logs.latest")}
                                    ariaLabel={newLogCount ? t("agent.logs.newLabel", { count: newLogCount }) : t("agent.logs.latest")}
                                    className="!bottom-52"
                                    onClick={() => scrollToBottom()}
                                />
                            ) : null}
                        </div>
                    </>
                ) : (
                    <>
                        <div className="flex items-center justify-between gap-2">
                            <span className="text-xs" style={{ color: theme.node.muted }}>
                                {t("agent.logs.fullData", { count: logs.length })}
                            </span>
                            <LogActions logs={logs} lastError={lastError} onClear={onClear} onCopy={(value, tip) => void copy(value, tip)} context={context} />
                        </div>
                        <textarea
                            ref={textareaRef}
                            readOnly
                            value={content}
                            className="thin-scrollbar min-h-0 flex-1 resize-none rounded-md border bg-transparent p-3 font-mono text-xs leading-5 outline-none"
                            style={{ borderColor: theme.node.stroke, color: theme.node.text }}
                            onFocus={(event) => event.currentTarget.select()}
                        />
                    </>
                )}
            </div>
        </div>
    );
}

/** 日志操作按钮组：复制全部、复制最后一条错误、清空，文本/JSON 两种模式共用。 */
function LogActions({ logs, lastError, context, onClear, onCopy }: { logs: AgentEventLog[]; lastError?: AgentEventLog; context: AgentLogContext; onClear: () => void; onCopy: (value?: string, tip?: string) => void }) {
    const { t } = useTranslation();
    return (
        <div className="flex shrink-0 items-center gap-0.5">
            <Tooltip title={t("agent.logs.copyAll")}>
                <Button type="text" size="small" shape="circle" aria-label={t("agent.logs.copyAll")} icon={<Copy className="size-3.5" />} onClick={() => onCopy()} />
            </Tooltip>
            <Tooltip title={t("agent.logs.copyLastError")}>
                <Button type="text" size="small" shape="circle" aria-label={t("agent.logs.copyLastError")} disabled={!lastError} icon={<CircleAlert className="size-3.5" />} onClick={() => lastError && onCopy(formatLogText([lastError], context), t("agent.logs.lastErrorCopied"))} />
            </Tooltip>
            <Tooltip title={t("agent.logs.clear")}>
                <Button danger type="text" size="small" shape="circle" aria-label={t("agent.logs.clear")} disabled={!logs.length} icon={<Trash2 className="size-3.5" />} onClick={onClear} />
            </Tooltip>
        </div>
    );
}

/** 单条日志行：按级别着色，可展开查看结构化详情（JSON），重复日志显示合并计数。 */
function LogRow({ item, theme, onToggle }: { item: DisplayLog; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; onToggle?: (open: boolean) => void }) {
    const { t } = useTranslation();
    const tone = item.level === "error" ? "text-red-600 dark:text-red-400" : item.level === "warning" ? "text-amber-600 dark:text-amber-400" : item.success ? "text-emerald-600 dark:text-emerald-400" : "";
    const Icon = item.level === "error" ? CircleAlert : item.level === "warning" ? TriangleAlert : item.success ? CheckCircle2 : CircleDot;
    return (
        <details className="group border-b last:border-b-0" style={{ borderColor: theme.node.stroke }} onToggle={(event) => onToggle?.(event.currentTarget.open)}>
            <summary className="cursor-pointer list-none px-1 py-2.5 transition hover:bg-black/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-current/20 dark:hover:bg-white/10 [&::-webkit-details-marker]:hidden">
                <div className="flex items-start gap-2.5">
                    <Icon className={`mt-0.5 size-4 shrink-0 ${tone}`} style={tone ? undefined : { color: theme.node.muted }} />
                    <div className="min-w-0 flex-1">
                        <div className="flex min-w-0 items-center gap-2">
                            <span className="shrink-0 font-mono text-[10px] leading-5" style={{ color: theme.node.faint }}>
                                {item.time}
                            </span>
                            <span className="truncate text-sm font-medium leading-5">{item.title}</span>
                            {item.count > 1 ? (
                                <span className="shrink-0 text-[10px] leading-4" style={{ color: theme.node.muted }}>
                                    {t("agent.logs.repeated", { count: item.count })}
                                </span>
                            ) : null}
                        </div>
                        {item.displayText !== item.title ? (
                            <div className="line-clamp-2 whitespace-pre-wrap break-words text-xs leading-5" style={{ color: theme.node.muted }}>
                                {item.displayText}
                            </div>
                        ) : null}
                    </div>
                    <ChevronDown className="mt-1 size-3.5 shrink-0 transition-transform group-open:rotate-180" style={{ color: theme.node.faint }} />
                </div>
            </summary>
            <div className="pb-3 pl-[34px] pr-2">
                <div className="mb-1 text-[10px] font-medium" style={{ color: theme.node.faint }}>
                    {t("agent.logs.details")}
                </div>
                <pre className="thin-scrollbar max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md border p-2.5 font-mono text-[11px] leading-5" style={{ borderColor: theme.node.stroke, background: theme.node.panel, color: theme.node.text }}>
                    {item.detail}
                </pre>
            </div>
        </details>
    );
}

/**
 * 把原始日志整理为展示列表：先展开（一条日志可能内含多个 JSON 事件），
 * 再把相邻且签名相同的条目合并计数，减少刷屏。
 */
function prepareLogs(logs: AgentEventLog[]) {
    return logs.flatMap(expandLog).reduce<DisplayLog[]>((result, item) => {
        const previous = result.at(-1);
        if (previous && previous.signature === item.signature) {
            previous.count += 1;
            previous.time = item.time;
            previous.detail = item.detail;
            return result;
        }
        result.push(item);
        return result;
    }, []);
}

/**
 * 单条原始日志 → 展示条目。raw/text 里若解析出 JSON 事件，会展开成多条（id 加序号），
 * 并从事件里提取标题、时间与摘要；签名用于相邻去重（路径/ID 归一化后比较）。
 */
function expandLog(item: AgentEventLog): DisplayLog[] {
    const entries = parseJsonEntries(item.raw ?? item.text);
    return (entries.length ? entries : [undefined]).map((entry, index) => {
        const displayText = compactLogText(entry === undefined ? item.text : logSummary(entry)) || item.title;
        const level = logLevel(item, entry);
        const title = entry === undefined ? item.title : logTitle(item.title, entry);
        return {
            ...item,
            id: entries.length > 1 ? `${item.id}-${index}` : item.id,
            time: logTime(item.time, entry),
            title,
            text: displayText,
            raw: entry ?? item.raw,
            count: 1,
            detail: entry === undefined ? stripAnsi(safeString(item.raw ?? item.text)) || item.text : safeJson(entry),
            displayText,
            level,
            signature: `${level}\n${title}\n${logSignature(displayText)}`,
            success: level === "info" && /完成|成功|已连接|已就绪|收到回复/.test(`${title}\n${displayText}`),
        };
    });
}

/** 推断日志级别：优先取结构化字段（mcp.startup 状态 / level 字段），否则按中英文关键词兜底。 */
function logLevel(item: AgentEventLog, entry?: unknown): DisplayLog["level"] {
    const entries = entry === undefined ? parseJsonEntries(item.raw ?? item.text) : [entry];
    const structured = entries.find((value) => value && typeof value === "object" && !Array.isArray(value)) as Record<string, unknown> | undefined;
    if (structured?.type === "mcp.startup") {
        if (structured.status === "failed") return "error";
        if (structured.status === "cancelled") return "warning";
        return "info";
    }
    const declared = entries.map(declaredLogLevel).filter(Boolean);
    if (declared.includes("error")) return "error";
    if (declared.includes("warning")) return "warning";
    if (declared.includes("info")) return "info";
    const text = `${item.title}\n${item.text}\n${safeString(item.raw)}`;
    if (/错误|失败|异常|中断|断开|拒绝|\berror\b|\bfailed\b|\bfatal\b|exception/i.test(text)) return "error";
    if (/警告|重试|未找到|不可用|\bwarn(?:ing)?\b|deprecated|missing|unavailable/i.test(text)) return "warning";
    return "info";
}

/** 读取日志对象自带 level 字段（error/warn/info 等多种写法），无有效级别返回空串。 */
function declaredLogLevel(value: unknown): DisplayLog["level"] | "" {
    if (!value || typeof value !== "object" || Array.isArray(value)) return "";
    const level = String((value as Record<string, unknown>).level || "").toLowerCase();
    if (["error", "fatal"].includes(level)) return "error";
    if (["warn", "warning"].includes(level)) return "warning";
    if (["info", "debug", "trace"].includes(level)) return "info";
    return "";
}

/** 通用「日志」标题按事件 target 细化（技能/插件/MCP/终端等），其余标题原样保留。 */
function logTitle(fallback: string, value: unknown) {
    if (fallback !== "日志" && fallback !== "Log" || !value || typeof value !== "object" || Array.isArray(value)) return fallback;
    const target = String((value as Record<string, unknown>).target || "").toLowerCase();
    if (target.includes("skill")) return i18n.t("agent.logs.skillLoading");
    if (target.includes("plugin")) return i18n.t("agent.logs.plugin");
    if (target.includes("mcp") || target.includes("rmcp")) return "MCP";
    if (target.includes("shell")) return i18n.t("agent.logs.terminal");
    if (target.includes("state_db")) return i18n.t("agent.logs.conversationStorage");
    return "Codex";
}

/** 优先用日志事件自带的 ISO timestamp 显示时间，缺失或非法时回退原始时间字符串。 */
function logTime(fallback: string, value: unknown) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return fallback;
    const timestamp = (value as Record<string, unknown>).timestamp;
    if (typeof timestamp !== "string") return fallback;
    const date = new Date(timestamp);
    return Number.isNaN(date.getTime()) ? fallback : date.toLocaleTimeString();
}

/**
 * 从日志 raw/text 中提取 JSON 事件条目：整体是对象直接返回，字符串先尝试整体解析，
 * 失败则扫描其中嵌套的 `{...}` / `[...]` 片段逐个解析（stderr 常混有普通文本输出）。
 */
function parseJsonEntries(value: unknown): unknown[] {
    if (value && typeof value === "object") return [value];
    if (typeof value !== "string") return [];
    const text = stripAnsi(value).trim();
    if (!text) return [];
    try {
        return [JSON.parse(text)];
    } catch {
        // 手写扫描器状态机：start 记录片段起点，depth 配对括号，quoted/escaped 处理字符串字面量。
        const entries: unknown[] = [];
        let start = -1;
        let depth = 0;
        let quoted = false;
        let escaped = false;
        for (let index = 0; index < text.length; index += 1) {
            const character = text[index];
            if (start < 0) {
                if (character !== "{" && character !== "[") continue;
                start = index;
                depth = 1;
                continue;
            }
            if (quoted) {
                if (escaped) escaped = false;
                else if (character === "\\") escaped = true;
                else if (character === '"') quoted = false;
                continue;
            }
            if (character === '"') quoted = true;
            else if (character === "{" || character === "[") depth += 1;
            else if (character === "}" || character === "]") depth -= 1;
            if (depth !== 0) continue;
            try {
                entries.push(JSON.parse(text.slice(start, index + 1)));
            } catch {
                // Ignore non-JSON fragments and continue scanning the stderr chunk.
            }
            start = -1;
        }
        return entries;
    }
}

/**
 * 递归提取一条日志的人类可读摘要：字符串原样、数组取前三段拼接，
 * 对象优先取 message/msg/reason 等常见字段，全无时回退 JSON 全文。
 */
function logSummary(value: unknown): string {
    if (typeof value === "string") return value;
    if (Array.isArray(value)) return value.map(logSummary).filter(Boolean).slice(0, 3).join(" · ");
    if (!value || typeof value !== "object") return String(value ?? "");
    const record = value as Record<string, unknown>;
    const fields = record.fields && typeof record.fields === "object" && !Array.isArray(record.fields) ? (record.fields as Record<string, unknown>) : null;
    if (fields) {
        for (const key of ["message", "msg", "reason", "summary", "text", "error"]) {
            if (fields[key] === undefined) continue;
            const summary = logSummary(fields[key]);
            if (summary) return summary;
        }
    }
    for (const key of ["message", "msg", "reason", "summary", "text", "error"]) {
        if (record[key] === undefined) continue;
        const summary = logSummary(record[key]);
        if (summary) return summary;
    }
    return [record.method, record.type, record.tool, record.path, record.url].filter((item) => typeof item === "string" && item).join(" · ") || safeJson(value);
}

/** 压缩空白并去掉 ANSI 转义序列，得到单行摘要文本。 */
function compactLogText(value: string) {
    return stripAnsi(value).replace(/\s+/g, " ").trim();
}

/** 去重签名用的文本归一化：把 skill.md 路径、file:// URL 与 UUID 替换为占位符，避免同内容不同参数被判定为新日志。 */
function logSignature(value: string) {
    return value
        .replace(/[a-z]:\\[^\r\n]*?skill\.md/gi, "<SKILL.md>")
        .replace(/file:\/\/\/[^\s"']+/gi, "<路径>")
        .replace(/[0-9a-f]{8}-[0-9a-f-]{27,}/gi, "<ID>");
}

/** 去掉终端 ANSI 颜色控制序列（\x1B[...m 等）。 */
function stripAnsi(value: string) {
    return value.replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, "");
}

/** 安全转字符串：字符串原样，null/undefined 返回空串，其余 JSON 序列化。 */
function safeString(value: unknown) {
    if (typeof value === "string") return value;
    if (value === undefined || value === null) return "";
    return safeJson(value);
}

/** JSON.stringify 的防抛错封装：循环引用等异常时退回 String()。 */
function safeJson(value: unknown) {
    try {
        return JSON.stringify(value, null, 2);
    } catch {
        return String(value);
    }
}
