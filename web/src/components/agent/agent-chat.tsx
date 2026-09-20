import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { motion, useSpring, useTransform } from "motion/react";
import { useTranslation } from "react-i18next";

import { canvasThemes } from "@/lib/canvas-theme";
import { summarizeCanvasAgentOps } from "@/lib/canvas/canvas-agent-ops";
import { useAgentStore, type AgentChatItem, type AgentPendingApproval, type AgentPendingToolCall, type AgentTokenUsage } from "@/stores/use-agent-store";
import { AgentApprovalCard, AgentChatMessage, AgentCommandGroup, AgentPendingToolCard, AgentToolCard, AgentWorkingMessage } from "./agent-chat-message";
import { agentMessageToChatMessage, currentPlanMessage, isPlanMessage, latestPlanMessage, toolCallDetail, toolName, workingActivity } from "./agent-event-formatters";
import { AgentScrollToBottom } from "./agent-scroll-to-bottom";

// 距底部 48px 内视为「贴底」：贴底时新消息自动跟随滚动，否则显示回到底部按钮。
const SCROLL_BOTTOM_THRESHOLD = 48;
// 历史消息默认不完整渲染（content-visibility 跳过屏外内容），估算高度用于滚动条占位。
const historyMessageStyle = { contentVisibility: "auto", containIntrinsicSize: "0 80px" } as const;

/**
 * 聊天时间线：渲染消息流、待确认工具卡片与运行中状态。
 * 贴底跟随逻辑：用户主动上滚即停止跟随，内容高度变化（流式输出）时仅在贴底时自动滚动。
 */
export function AgentChatTimeline({
    theme,
    pendingTool,
    pendingApprovals,
    sending,
    waiting,
    onRejectTool,
    onApproveTool,
    onApprovalDecision,
}: {
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    pendingTool: AgentPendingToolCall | null;
    pendingApprovals: AgentPendingApproval[];
    sending: boolean;
    waiting: boolean;
    onRejectTool: () => void;
    onApproveTool: () => void;
    onApprovalDecision: (approval: AgentPendingApproval, decision: "accept" | "acceptForSession" | "decline") => void;
}) {
    const { t } = useTranslation();
    const messages = useAgentStore((state) => state.messages);
    const bootstrapStatus = useAgentStore((state) => state.bootstrapStatus);
    const mcpStartupStatuses = useAgentStore((state) => state.mcpStartupStatuses);
    const timeline = useMemo(() => groupTimelineMessages(messages), [messages]);
    const listRef = useRef<HTMLDivElement>(null);
    const contentRef = useRef<HTMLDivElement>(null);
    const followMessagesRef = useRef(true);
    const [showScrollToBottom, setShowScrollToBottom] = useState(false);
    const streaming = messages.some((message) => message.streamId);
    const showBootstrap = Boolean(bootstrapStatus && !messages.some((message) => message.role === "user" || message.role === "assistant"));
    const working = showBootstrap ? bootstrapStatus! : workingActivity(messages.at(-1));
    const updateScrollState = useCallback(() => {
        const list = listRef.current;
        if (!list) return;
        const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight <= SCROLL_BOTTOM_THRESHOLD;
        followMessagesRef.current = atBottom;
        setShowScrollToBottom(!atBottom);
    }, []);
    const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
        const list = listRef.current;
        if (!list) return;
        followMessagesRef.current = true;
        list.scrollTo({ top: list.scrollHeight, behavior });
        setShowScrollToBottom(false);
    }, []);
    useEffect(() => {
        // 每次消息/审批/工具状态变化后：贴底则瞬时滚到底，否则刷新「回到底部」按钮的可见性。
        const frame = requestAnimationFrame(() => (followMessagesRef.current ? scrollToBottom("auto") : updateScrollState()));
        return () => cancelAnimationFrame(frame);
    }, [messages, pendingApprovals, pendingTool, scrollToBottom, updateScrollState, waiting]);
    useEffect(() => {
        // 流式输出会改变内容高度但可能不触发消息列表变化，ResizeObserver 兜底保持跟随。
        const content = contentRef.current;
        if (!content) return;
        let frame = 0;
        const observer = new ResizeObserver(() => {
            cancelAnimationFrame(frame);
            frame = requestAnimationFrame(() => (followMessagesRef.current ? scrollToBottom("auto") : updateScrollState()));
        });
        observer.observe(content);
        return () => {
            observer.disconnect();
            cancelAnimationFrame(frame);
        };
    }, [scrollToBottom, updateScrollState]);
    return (
        <div className="relative min-h-0 flex-1">
            <div ref={listRef} className="thin-scrollbar h-full select-text overflow-y-auto" onScroll={updateScrollState}>
                <div ref={contentRef} className="space-y-4 px-4 pt-4">
                    {timeline.map((entry) => entry.type === "commands"
                        ? <AgentCommandGroupRow key={entry.id} items={entry.items} theme={theme} />
                        : <AgentChatMessageRow key={entry.item.id} item={entry.item} theme={theme} />)}
                    {pendingTool ? (
                        <AgentPendingToolCard
                            summary={summarizeCanvasAgentOps(pendingTool.input?.ops || []) || toolName(pendingTool.name)}
                            detail={toolCallDetail(pendingTool.name, pendingTool.input, "pending")}
                            theme={theme}
                            onReject={onRejectTool}
                            onApprove={onApproveTool}
                        />
                    ) : null}
                    {pendingApprovals.map((approval) => <AgentApprovalCard key={approval.requestId} approval={approval} theme={theme} onDecision={(decision) => onApprovalDecision(approval, decision)} />)}
                    {(sending || waiting || showBootstrap) && !streaming && !pendingTool && !pendingApprovals.length ? <AgentWorkingMessage text={working.text} detail={"detail" in working && typeof working.detail === "string" ? working.detail : undefined} status={showBootstrap ? bootstrapStatus?.status : undefined} mcpStatuses={showBootstrap ? Object.entries(mcpStartupStatuses).map(([name, item]) => ({ name, ...item })) : []} activityKey={working.key} theme={theme} /> : null}
                </div>
            </div>
            {showScrollToBottom ? (
                <AgentScrollToBottom theme={theme} title={t("agent.chat.latestMessages")} onClick={() => scrollToBottom()} />
            ) : null}
        </div>
    );
}

/** 任务进度条：任务进行中显示当前轮计划，空闲时显示最后一轮的完成态。 */
export function AgentTaskProgress({ theme, busy }: { theme: (typeof canvasThemes)[keyof typeof canvasThemes]; busy: boolean }) {
    const { t } = useTranslation();
    const plan = useAgentStore((state) => busy ? currentPlanMessage(state.messages) : latestPlanMessage(state.messages));
    if (!plan) return null;
    return (
        <div className="shrink-0 px-4 pt-2">
            <AgentToolCard key={plan.id} title={plan.title || t("agent.events.progress")} text={plan.text} detail={plan.detail} theme={theme} />
        </div>
    );
}

const AgentChatMessageRow = memo(function AgentChatMessageRow({ item, theme }: { item: AgentChatItem; theme: (typeof canvasThemes)[keyof typeof canvasThemes] }) {
    const endpoint = useAgentStore((state) => state.url);
    const token = useAgentStore((state) => state.token);
    return (
        <div style={item.streamId ? undefined : historyMessageStyle}>
            <AgentChatMessage item={agentMessageToChatMessage(item, endpoint, token)} theme={theme} />
        </div>
    );
});

const AgentCommandGroupRow = memo(function AgentCommandGroupRow({ items, theme }: { items: AgentChatItem[]; theme: (typeof canvasThemes)[keyof typeof canvasThemes] }) {
    return (
        <div style={items.some((item) => item.streamId) ? undefined : historyMessageStyle}>
            <AgentCommandGroup items={items} theme={theme} />
        </div>
    );
});

// 时间线条目：普通消息，或同 thread/turn 内连续命令消息合并成的命令组。
type AgentTimelineEntry = { type: "message"; item: AgentChatItem } | { type: "commands"; id: string; items: AgentChatItem[] };

/** 把消息流按 thread/turn 归属把连续的命令执行消息折叠成命令组；计划消息单独由进度条展示，跳过。 */
function groupTimelineMessages(messages: AgentChatItem[]) {
    const timeline: AgentTimelineEntry[] = [];
    let commands: AgentChatItem[] = [];
    let commandScope = "";
    const flushCommands = () => {
        if (!commands.length) return;
        timeline.push({ type: "commands", id: `commands:${commands[0].id}`, items: commands });
        commands = [];
        commandScope = "";
    };
    messages.forEach((item) => {
        if (isPlanMessage(item)) return;
        if (isCommandMessage(item)) {
            const scope = item.threadId && item.turnId ? `${item.threadId}\0${item.turnId}` : item.id;
            if (commands.length && scope !== commandScope) flushCommands();
            commands.push(item);
            commandScope = scope;
            return;
        }
        flushCommands();
        timeline.push({ type: "message", item });
    });
    flushCommands();
    return timeline;
}

function isCommandMessage(item: AgentChatItem) {
    return item.role === "tool" && item.detail && typeof item.detail === "object" && (item.detail as { kind?: unknown }).kind === "command";
}

/** 最近一次 token 用量条。 */
export function AgentUsageBar({ usage, theme }: { usage: AgentTokenUsage; theme: (typeof canvasThemes)[keyof typeof canvasThemes] }) {
    const { t } = useTranslation();
    return (
        <div className="flex items-center justify-center gap-4 px-4 pt-1 text-[11px] tabular-nums" style={{ color: theme.node.muted }}>
            <span className="opacity-70">{t("agent.chat.latestCall")}</span>
            <UsageNumber label={t("agent.chat.input")} value={usage.input} color={theme.node.text} />
            <UsageNumber label={t("agent.chat.cached")} value={usage.cached} color={theme.node.text} />
            <UsageNumber label={t("agent.chat.output")} value={usage.output} color={theme.node.text} />
        </div>
    );
}

/** 用量数字：用弹簧动画平滑过渡到新值，避免流式更新时数字跳变。 */
function UsageNumber({ label, value, color }: { label: string; value: number; color: string }) {
    const spring = useSpring(value, { stiffness: 110, damping: 24, mass: 0.7 });
    const text = useTransform(spring, (current) => Math.round(current).toLocaleString());
    useEffect(() => spring.set(value), [spring, value]);
    return (
        <span className="inline-flex items-baseline gap-1" aria-label={`${label} ${value.toLocaleString()}`}>
            <span>{label}</span>
            <motion.span aria-hidden className="font-medium" style={{ color }}>
                {text}
            </motion.span>
        </span>
    );
}
