import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { App, Button, Tooltip } from "antd";
import dayjs from "dayjs";
import { Bot, History, MessageSquare, PanelRightClose, PlugZap, Plus, Sparkles, Terminal } from "lucide-react";
import { useTranslation } from "react-i18next";

import i18n from "@/i18n";
import { readAgentUrlBootstrap } from "@/lib/agent/agent-url-bootstrap";
import { canvasThemes } from "@/lib/canvas-theme";
import { upscaleDataUrl } from "@/lib/canvas/canvas-image-data";
import { imageMetadata } from "@/lib/canvas/canvas-node-factory";
import { fitNodeSize } from "@/lib/canvas/canvas-node-size";
import { resolveCanvasReferenceImages } from "@/lib/canvas/canvas-resource-references";
import { readImageMeta } from "@/lib/image-utils";
import { randomId } from "@/lib/utils";
import { uploadImage } from "@/services/media-ingest";
import { useThemeStore } from "@/stores/use-theme-store";
import { useAgentSkillStore } from "@/stores/use-agent-skill-store";
import { useShallow } from "zustand/react/shallow";
import { useAgentStore, type AgentAttachment, type AgentBootstrapStatus, type AgentCanvasContext, type AgentCanvasReference, type AgentChatItem, type AgentConversationState, type AgentModel, type AgentPendingApproval, type AgentPendingToolCall, type AgentPermissionMode, type AgentReasoningEffort, type AgentThreadSummary } from "@/stores/use-agent-store";
import { type CanvasAgentOp, type CanvasAgentSnapshot } from "@/lib/canvas/canvas-agent-ops";
import { isSiteTool, runSiteTool } from "@/lib/agent/agent-site-tools";
import { acknowledgeCodexHistory, activateAgentClient, AgentApiError, discoverAgentConfig, fetchAgentJson, interruptCodexTurn, postCodexApproval, postState, postToolResult } from "@/services/api/canvas-agent";
import { AgentChatTimeline, AgentTaskProgress, AgentUsageBar } from "./agent-chat";
import { AgentChatComposer } from "./agent-chat-composer";
import { AgentConnectView } from "./agent-connect-view";
import {
    activityDeltaFallback,
    activityDetail,
    activityKind,
    activityPlaceholder,
    agentAttachmentToChatAttachment,
    agentErrorView,
    attachmentPayloadBytes,
    compactText,
    eventUsage,
    formatAgentActivity,
    formatAgentEvent,
    formatAgentEventLog,
    formatAgentPlan,
    formatBytes,
    bindPendingTurnMessages,
    isCanvasWriteTool,
    isConnectionErrorMessage,
    isCurrentThreadEvent,
    isReasoningSummary,
    mergeAgentMessages,
    mergeStreamText,
    normalizeHistoryMessages,
    normalizeText,
    parseEventData,
    promptWithAttachments,
    promptWithCanvasReferences,
    reasoningActivityText,
    registerLiveAgentTurn,
    scopeChatItem,
    stringText,
    toolName,
    turnPlanStatus,
    upsertAgentMessage,
    type AgentEventItem,
    type AgentEventPayload,
} from "./agent-event-formatters";
import { AgentHistoryView } from "./agent-history-view";
import { AgentLogView } from "./agent-log-view";
import { AgentPanelTabs } from "./agent-panel-tabs";
import { AgentSkillsView } from "./agent-skills-view";

// 附件上限与总负载上限：附件以 dataUrl 整包随 turn 请求提交，超限会被本地直接拒绝。
const MAX_ATTACHMENTS = 6;
const MAX_ATTACHMENT_PAYLOAD_BYTES = 28 * 1024 * 1024;
// 消息附件预览图的最长边 / dataUrl 长度上限：超过即降采样，控制落库体积。
const MESSAGE_PREVIEW_LONG_EDGE = 192;
const MESSAGE_PREVIEW_MAX_LENGTH = 500_000;
const DEFAULT_AGENT_URL = "http://127.0.0.1:17371";
// 与本地 Agent 服务的 SSE 协议版本，不一致时直接断开并提示升级，防止新旧前后端互发无法理解的事件。
const AGENT_PROTOCOL_VERSION = 6;
// 历史快照拉取的递增重试间隔（毫秒）：覆盖服务端刚切线程、快照尚未就绪的窗口。
const HISTORY_RETRY_DELAYS_MS = [0, 150, 350, 700, 1200];
// 支持的推理力度白名单：过滤模型返回的 effort 列表，避免展示后端不认识的值。
const AGENT_REASONING_EFFORTS = new Set<AgentReasoningEffort>(["minimal", "low", "medium", "high", "xhigh", "max", "ultra"]);
const rt = (key: string, options?: Record<string, unknown>) => i18n.t(`agent.runtime.${key}`, options);

// 本地 Agent 服务各接口的响应结构（均允许缺字段，读取方需判空）。
type AgentWorkspace = { workspacePath: string; activeThreadId?: string };
type AgentThreadsResponse = { ok?: boolean; workspace?: AgentWorkspace; conversation?: AgentConversationState; data?: AgentThreadSummary[] };
type AgentThreadResponse = { ok?: boolean; workspace?: AgentWorkspace; conversation?: AgentConversationState; thread?: AgentThreadSummary; messages?: AgentChatItem[]; settledTurnIds?: string[]; historyReady?: boolean };
type AgentWorkspaceResponse = { ok?: boolean; workspace?: AgentWorkspace; conversation?: AgentConversationState };
type AgentTurnResponse = { ok?: boolean; threadId?: string };
type AgentModelsResponse = { ok?: boolean; data?: AgentModel[] };
// codex_state 事件负载：busy 表示 turn 正在执行，threadId/turnId 指明归属。
type AgentCodexState = { busy?: boolean; threadId?: string; turnId?: string };
// hello 事件负载：连接建立后的首个握手消息，携带协议版本、会话与待审批列表。
type AgentHelloEvent = { ok?: boolean; protocolVersion?: number; clientId?: string; workspace?: { activeThreadId?: string }; conversation?: AgentConversationState; codex?: AgentCodexState; pendingApprovals?: AgentPendingApproval[] };
type AgentWorkspaceEvent = { activeThreadId?: string; threadId?: string; sourceClientId?: string; emptyThread?: boolean; draftThread?: boolean; conversation?: AgentConversationState };
type AgentChatEvent = { threadId?: string; turnId?: string; sourceClientId?: string; replayed?: boolean; message?: AgentChatItem };
// agent_bootstrap 事件负载：会话初始化与 MCP 服务启动进度的分阶段通知。
type AgentBootstrapEvent = { type?: "codex.preparing" | "codex.prepare_failed" | "mcp.startup" | "mcp.complete"; phase?: "preheat" | "runtime"; threadId?: string; name?: string; status?: "starting" | "ready" | "failed" | "cancelled"; error?: string | null; failureReason?: string | null };
// 在 globalThis 上缓存 clientId 的单例 Promise，避免多组件实例重复竞速锁。
type AgentClientGlobal = typeof globalThis & { __infiniteCanvasAgentClientIdPromise?: Promise<string> };

/** 把 settledTurnIds 编成 `threadId\0turnId` 键集合：作为权威历史标记，用于丢弃重放事件。 */
function authoritativeHistoryTurnKeys(threadId: string, settledTurnIds: string[]) {
    return new Set(settledTurnIds.map((turnId) => `${threadId}\0${turnId}`));
}

/** 从 AgentApiError 响应体中提取服务端回传的会话状态快照，用于失败后回滚到一致状态。 */
function agentErrorState(error: unknown) {
    return error instanceof AgentApiError ? (error.response as { state?: AgentConversationState }).state : undefined;
}

/** 把会话初始化状态（idle/preparing/warning/failed/ready + 各 MCP 服务状态）折叠成面板顶部的引导状态展示。 */
function conversationBootstrapView(conversation: AgentConversationState) {
    const mcpStartupStatuses: Record<string, AgentBootstrapStatus> = Object.fromEntries(Object.entries(conversation.mcpStatuses).map(([name, item]) => {
        const view: AgentBootstrapStatus = item.status === "starting"
            ? { key: `mcp:${name}:starting`, text: rt("mcpStarting", { name }), detail: rt("mcpConnecting"), status: "running" }
            : item.status === "ready"
                ? { key: `mcp:${name}:ready`, text: rt("mcpReadyNamed", { name }), detail: rt("toolsReady"), status: "ready" }
                : { key: `mcp:${name}:${item.status}`, text: rt(item.status === "failed" ? "mcpFailedNamed" : "mcpCanceledNamed", { name }), detail: item.error || rt("toolInitFailed"), status: "error" };
        return [name, view];
    }));
    const services = Object.values(mcpStartupStatuses);
    const pending = services.filter((item) => item.status === "running").length;
    const bootstrapStatus: AgentBootstrapStatus | null = conversation.status === "idle" || conversation.status === "preparing"
        ? services.length
            ? { key: "mcp:starting", text: rt("mcpServicesStarting"), detail: pending ? rt("toolServicesPending", { count: pending }) : rt("checkingToolServices"), status: "running" }
            : { key: "codex:preparing", text: rt("conversationInitializing"), detail: rt("conversationCreating"), status: "running" }
        : conversation.status === "warning"
            ? { key: "mcp:warning", text: rt("someMcpFailed"), detail: rt("remainingToolsReady"), status: "error" }
            : conversation.status === "failed"
                ? { key: "codex:prepare_failed", text: rt("conversationInitFailed"), detail: conversation.error || rt("conversationCreateFailed"), status: "error" }
                : conversation.status === "ready"
                    ? { key: "mcp:ready", text: rt("mcpServicesReady", { count: services.length }), detail: rt("toolsReady"), status: "ready" }
                    : null;
    return { bootstrapStatus, mcpStartupStatuses };
}

/**
 * AI 助手面板的主容器：负责与本地 Agent 服务建立 SSE 连接、维护消息/线程/技能状态，
 * 并把画布上下文快照同步给 Agent。消息严格按 threadId/turnId/itemId 归属去重；
 * 事件按连接排队串行处理，历史快照到达后成为权威并合并实时消息。
 * @param embedded 内嵌模式：直接渲染内容（不套面板外壳，由 AgentPanel 提供容器）
 * @param headless 无头模式：只跑连接逻辑不渲染任何 UI（弹窗也静默）
 * @param autoConnect 挂载后自动静默连接一次
 */
export function LocalAgentPanel({ embedded, headless, autoConnect }: { embedded?: boolean; headless?: boolean; autoConnect?: boolean }) {
    const { t } = useTranslation();
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const { message, modal } = App.useApp();
    const { hash } = useLocation();
    const [searchParams] = useSearchParams();
    const navigate = useNavigate();
    // Field-level selectors with useShallow rerender only when these fields change.
    // canvasContext is intentionally excluded because project updates it every frame during dragging and resizing.
    // The panel uses it only for ref synchronization and debounced postState calls, never during rendering.
    // Subscribing here would rerender the panel every frame and amplify the #185 crash, so it is observed imperatively below.
    const { width, url, token, connected, enabled, prompt, attachments, sending, waiting, tokenUsage, eventLogs, threads, activeThreadId, workspacePath, loadingThreads, activeTab, confirmTools, permissionMode, models, model, reasoningEffort, activity, conversation, connectError, pendingTool, pendingApprovals } = useAgentStore(
        useShallow((state) => ({
            width: state.width,
            url: state.url,
            token: state.token,
            connected: state.connected,
            enabled: state.enabled,
            prompt: state.prompt,
            attachments: state.attachments,
            sending: state.sending,
            waiting: state.waiting,
            tokenUsage: state.tokenUsage,
            eventLogs: state.eventLogs,
            threads: state.threads,
            activeThreadId: state.activeThreadId,
            workspacePath: state.workspacePath,
            loadingThreads: state.loadingThreads,
            activeTab: state.activeTab,
            confirmTools: state.confirmTools,
            permissionMode: state.permissionMode,
            models: state.models,
            model: state.model,
            reasoningEffort: state.reasoningEffort,
            activity: state.activity,
            conversation: state.conversation,
            connectError: state.connectError,
            pendingTool: state.pendingTool,
            pendingApprovals: state.pendingApprovals,
        })),
    );
    const setAgentState = useAgentStore((state) => state.setAgentState);
    const conversationReady = conversation.status === "ready" || conversation.status === "warning";
    const conversationBusy = conversation.status === "preparing" || conversation.status === "running";
    const closePanel = useAgentStore((state) => state.closePanel);
    const pushMessage = useAgentStore((state) => state.addMessage);
    const pushEventLog = useAgentStore((state) => state.addEventLog);
    const clearEventLogs = useAgentStore((state) => state.clearEventLogs);
    const loadSkills = useAgentSkillStore((state) => state.loadSkills);
    const clearSkillSelection = useAgentSkillStore((state) => state.clearSelection);
    const skillCount = useAgentSkillStore((state) => state.skills.length);
    const messageCount = useAgentStore((state) => state.messages.length);
    const canvasContextRef = useRef<AgentCanvasContext | null>(useAgentStore.getState().canvasContext);
    const confirmToolsRef = useRef(confirmTools);
    const pendingToolRef = useRef<AgentPendingToolCall | null>(null);
    const autoConnectRef = useRef(false);
    const connectedRef = useRef(false);
    const errorLoggedRef = useRef(false);
    const attachmentUrlsRef = useRef(new Set<string>());
    const clientIdRef = useRef("");
    const [clientReady, setClientReady] = useState(false);
    const loadThreadsSequenceRef = useRef(0);
    const threadMessagesRef = useRef(new Map<string, AgentChatItem[]>());
    const authoritativeHistoryTurnsRef = useRef(new Set<string>());
    const liveTurnKeysRef = useRef(new Set<string>());
    const threadOperationRef = useRef(0);
    const threadOperationSequenceRef = useRef(0);
    const endpoint = useMemo(() => url.trim().replace(/\/$/, ""), [url]);
    const urlAgentAutoConnect = searchParams.has("agentUrl") && searchParams.has("agentToken");
    useEffect(() => {
        let disposed = false;
        void acquireAgentClientId().then((clientId) => {
            if (!disposed) {
                clientIdRef.current = clientId;
                setClientReady(true);
            }
        });
        return () => { disposed = true; };
    }, []);
    /**
     * 拉取指定线程的历史快照，按递增间隔重试直至拿到包含期望 turn 的就绪数据。
     * 每次成功后把快照标记为权威（settled turns 清出 live 集合），再与本地实时消息合并写回 store；
     * 序列号或激活线程已变化时放弃本轮结果，防止旧请求覆盖新线程。
     * @returns true 表示拿到了就绪快照（含期望 turn），false 表示重试耗尽或中途失效
     */
    const loadThreadSnapshot = useCallback(async (threadId: string, sequence: number, response?: AgentThreadResponse, expectedTurnId = "") => {
        let thread = response;
        let lastError: unknown;
        for (const delayMs of HISTORY_RETRY_DELAYS_MS) {
            if (delayMs) await delay(delayMs);
            if (sequence !== loadThreadsSequenceRef.current || useAgentStore.getState().activeThreadId !== threadId) return false;
            try {
                thread ||= await fetchAgentJson<AgentThreadResponse>(endpoint, token, `/agent/codex/threads/${encodeURIComponent(threadId)}`);
                lastError = undefined;
            } catch (error) {
                lastError = error;
                thread = undefined;
                continue;
            }
            const history = normalizeHistoryMessages(thread.messages || []);
            const latest = useAgentStore.getState();
            if (sequence !== loadThreadsSequenceRef.current || latest.activeThreadId !== threadId) return false;
            const historyTurns = authoritativeHistoryTurnKeys(threadId, thread.settledTurnIds || []);
            const hasExpectedTurn = !expectedTurnId || historyTurns.has(`${threadId}\0${expectedTurnId}`);
            historyTurns.forEach((key) => liveTurnKeysRef.current.delete(key));
            if (latest.activeTurnId) liveTurnKeysRef.current.add(`${threadId}\0${latest.activeTurnId}`);
            authoritativeHistoryTurnsRef.current = historyTurns;
            const messages = mergeAgentMessages(history, latest.messages, threadId, liveTurnKeysRef.current);
            threadMessagesRef.current.set(threadId, messages);
            setAgentState({ messages, connectError: "" });
            const coveredTurnIds = [...historyTurns].map((key) => key.slice(threadId.length + 1));
            if (coveredTurnIds.length) void acknowledgeCodexHistory(endpoint, token, threadId, coveredTurnIds).catch(() => undefined);
            if (hasExpectedTurn && (thread.historyReady !== false || Boolean(expectedTurnId))) return true;
            thread = undefined;
        }
        if (lastError) throw lastError;
        return false;
    }, [endpoint, setAgentState, token]);
    /**
     * 应用工作区/线程切换事件：缓存旧线程消息、清空待发草稿的归属，
     * 并 bump 序列号使在途的历史请求失效。emptyThread（新建草稿线程）时清空消息，
     * 但保留「正在发送且尚未拿到 turnId」的用户消息，避免刚发出的内容凭空消失。
     * @returns 本次的最新序列号，供调用方校验请求是否仍然有效
     */
    const applyWorkspaceChange = useCallback((data: AgentWorkspaceEvent) => {
        const nextThreadId = data.activeThreadId ?? data.threadId ?? "";
        const current = useAgentStore.getState();
        const threadChanged = current.activeThreadId !== nextThreadId;
        const emptyThread = Boolean(data.emptyThread || data.draftThread);
        const pendingMessage = [...current.messages].reverse().find((item) => item.role === "user" && !item.turnId);
        const keepPendingMessage = Boolean(
            data.emptyThread
            && pendingMessage
            && (current.sending || current.waiting)
            && (!data.sourceClientId || data.sourceClientId === clientIdRef.current),
        );
        if (threadChanged && current.activeThreadId) {
            const messages = keepPendingMessage ? current.messages.filter((item) => item.id !== pendingMessage!.id) : current.messages;
            threadMessagesRef.current.set(current.activeThreadId, messages);
        }
        if (emptyThread && nextThreadId) threadMessagesRef.current.delete(nextThreadId);
        if (threadChanged || emptyThread) {
            loadThreadsSequenceRef.current += 1;
            authoritativeHistoryTurnsRef.current.clear();
            liveTurnKeysRef.current.clear();
        }
        const messages = keepPendingMessage
            ? [scopeChatItem(pendingMessage!, nextThreadId, "")]
            : emptyThread ? []
                : threadChanged ? threadMessagesRef.current.get(nextThreadId) || []
                    : current.messages;
        pendingToolRef.current = null;
        setAgentState({
            activeThreadId: nextThreadId,
            activeTurnId: threadChanged || emptyThread ? "" : current.activeTurnId,
            messages,
            tokenUsage: threadChanged || emptyThread ? null : current.tokenUsage,
            pendingTool: null,
            pendingApprovals: threadChanged || emptyThread ? [] : current.pendingApprovals,
        });
        return loadThreadsSequenceRef.current;
    }, [setAgentState]);
    /**
     * 按版本号（revision）单调应用会话状态：旧于当前的更新直接忽略。
     * 会话或线程变化时联动 applyWorkspaceChange 切换消息视图。
     * @param force 跳过版本比较强制应用（用于 hello 握手后的全量对齐）
     */
    const applyConversationState = useCallback((next: AgentConversationState, force = false) => {
        const current = useAgentStore.getState();
        if (!next?.revision || !force && next.revision <= current.conversation.revision) return false;
        const conversationChanged = next.conversationId !== current.conversation.conversationId;
        if (conversationChanged || next.threadId !== current.activeThreadId) {
            applyWorkspaceChange({
                activeThreadId: next.threadId,
                emptyThread: conversationChanged || !current.activeThreadId,
                draftThread: next.status === "preparing",
                sourceClientId: next.sourceClientId,
            });
        }
        setAgentState({ conversation: next, ...conversationBootstrapView(next) });
        return true;
    }, [applyWorkspaceChange, setAgentState]);
    /** 拉取线程列表并对当前线程做快照合并；skipHistory 用于新建草稿线程时跳过历史拉取。 */
    const loadThreads = useCallback(async (skipHistory = false, expectedTurnId = "") => {
        if (!connectedRef.current && !useAgentStore.getState().connected) return;
        let sequence = ++loadThreadsSequenceRef.current;
        setAgentState({ loadingThreads: true });
        try {
            const data = await fetchAgentJson<AgentThreadsResponse>(endpoint, token, `/agent/codex/threads`);
            if (sequence !== loadThreadsSequenceRef.current) return;
            if (data.conversation) {
                applyConversationState(data.conversation);
                sequence = loadThreadsSequenceRef.current;
            }
            const current = useAgentStore.getState();
            const currentThreadId = current.activeThreadId || data.workspace?.activeThreadId || "";
            if (!data.conversation && currentThreadId !== current.activeThreadId) sequence = applyWorkspaceChange({ activeThreadId: currentThreadId });
            if (sequence !== loadThreadsSequenceRef.current || useAgentStore.getState().activeThreadId !== currentThreadId) return;
            setAgentState({ threads: data.data || [], workspacePath: data.workspace?.workspacePath || "" });
            if (currentThreadId && !skipHistory) {
                await loadThreadSnapshot(currentThreadId, sequence, undefined, expectedTurnId);
            } else {
                authoritativeHistoryTurnsRef.current.clear();
                liveTurnKeysRef.current.clear();
            }
        } catch (error) {
            addEventLog(rt("historyReadFailed"), error);
        } finally {
            if (sequence === loadThreadsSequenceRef.current && !threadOperationRef.current) setAgentState({ loadingThreads: false });
        }
    }, [applyConversationState, applyWorkspaceChange, endpoint, loadThreadSnapshot, setAgentState, token]);
    // Imperatively subscribe to canvasContext to keep the ref current and debounce snapshot reports without rerendering the panel.
    useEffect(() => {
        let timer: ReturnType<typeof setTimeout> | null = null;
        const unsubscribe = useAgentStore.subscribe((state) => {
            if (state.canvasContext === canvasContextRef.current) return;
            canvasContextRef.current = state.canvasContext;
            if (!useAgentStore.getState().connected) return;
            if (timer) clearTimeout(timer);
            timer = setTimeout(() => void postState(endpoint, token, clientIdRef.current, canvasContextRef.current?.snapshot || null), 300);
        });
        return () => {
            unsubscribe();
            if (timer) clearTimeout(timer);
        };
    }, [endpoint, token]);
    useEffect(() => {
        confirmToolsRef.current = confirmTools;
    }, [confirmTools]);
    useEffect(() => {
        pendingToolRef.current = pendingTool;
    }, [pendingTool]);
    useEffect(() => () => attachmentUrlsRef.current.forEach((url) => URL.revokeObjectURL(url)), []);

    useEffect(() => {
        if (!clientReady || !enabled || !token.trim()) return;
        localStorage.setItem("canvas-agent-url", endpoint);
        localStorage.setItem("canvas-agent-token", token);
        const clientId = clientIdRef.current;
        let disposed = false;
        let protocolRejected = false;
        // SSE 事件的处理器全部串行入队：避免上一事件触发的 setState 还是旧状态时下一事件抢先读取。
        let eventQueue = Promise.resolve();
        const isCurrentConnection = () => !disposed && clientIdRef.current === clientId;
        const enqueueEvent = (task: () => void | Promise<void>) => {
            eventQueue = eventQueue.then(async () => {
                if (isCurrentConnection()) await task();
            }).catch((error) => {
                if (isCurrentConnection()) addEventLog(rt("conversationSyncFailed"), error);
            });
        };
        const source = new EventSource(`${endpoint}/events?token=${encodeURIComponent(token)}&clientId=${encodeURIComponent(clientId)}`);
        source.addEventListener("hello", (event) => {
            if (!isCurrentConnection()) return;
            const hello = parseEventData<AgentHelloEvent>(event);
            // 协议版本不一致：服务端可能是旧版插件/新版前端，直接断开并要求升级，防止双方互发错乱事件。
            if (hello?.protocolVersion !== AGENT_PROTOCOL_VERSION) {
                const text = rt("agentOutdated");
                protocolRejected = true;
                source.close();
                connectedRef.current = false;
                setAgentState({ enabled: false, connected: false, waiting: false, sending: false, activity: rt("restartRequired"), connectError: text, silentConnect: false, fragmentBootstrap: false, pendingTool: null, pendingApprovals: [] });
                useAgentSkillStore.getState().reset();
                addEventLog(rt("versionMismatch"), text, hello);
                if (!headless) message.error(text);
                return;
            }
            const codex = hello?.codex;
            const busy = Boolean(codex?.busy);
            // hello 时对齐全量状态：当前 turn、待审批列表，并清除上次断连留下的本地错误消息。
            const nextThreadId = hello?.conversation?.threadId ?? hello?.workspace?.activeThreadId ?? useAgentStore.getState().activeThreadId;
            if (hello?.conversation) applyConversationState(hello.conversation, true);
            else applyWorkspaceChange({ activeThreadId: nextThreadId });
            const current = useAgentStore.getState();
            const nextTurnId = codex?.threadId === nextThreadId ? codex.turnId ?? "" : "";
            if (nextTurnId) liveTurnKeysRef.current.add(`${nextThreadId}\0${nextTurnId}`);
            const activeTurnId = busy ? nextTurnId : "";
            const pendingApprovals = busy ? (hello?.pendingApprovals || []).filter((item) => !item.threadId || item.threadId === nextThreadId) : [];
            const messages = activeTurnId
                ? bindPendingTurnMessages(current.messages.filter((item) => !isConnectionErrorMessage(item)), nextThreadId, activeTurnId)
                : current.messages.filter((item) => !isConnectionErrorMessage(item));
            errorLoggedRef.current = false;
            connectedRef.current = true;
            setAgentState({
                connected: true,
                activity: pendingApprovals.length ? rt("awaitingApproval") : busy ? rt("codexRunning") : rt("connected"),
                waiting: busy,
                sending: false,
                connectError: "",
                silentConnect: false,
                fragmentBootstrap: false,
                activeThreadId: nextThreadId,
                activeTurnId,
                messages,
                pendingApprovals,
            });
            if (!headless) message.success(rt("localAgentConnected"));
            void postState(endpoint, token, clientId, canvasContextRef.current?.snapshot || null);
            if (document.visibilityState === "visible" && document.hasFocus()) void activateAgentClient(endpoint, token, clientId);
            if (!busy && !nextThreadId && (!hello?.conversation || hello.conversation.status === "idle")) {
                // 首次连接且没有任何线程：主动重置出一个新会话，避免面板停在「未初始化」状态。
                void fetchAgentJson<AgentWorkspaceResponse>(endpoint, token, "/agent/codex/threads/reset", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ clientId, permissionMode }) })
                    .then((result) => result.conversation && applyConversationState(result.conversation))
                    .catch((error) => {
                        const state = agentErrorState(error);
                        if (state) applyConversationState(state);
                        addEventLog(rt("conversationInitFailed"), error);
                    });
            }
        });
        source.addEventListener("codex_state", (event) => {
            const data = parseEventData<AgentCodexState>(event);
            if (!data) return;
            enqueueEvent(async () => {
                const busy = Boolean(data.busy);
                const current = useAgentStore.getState();
                // 只处理属于当前线程的状态，其它线程的 busy 变化由线程切换逻辑兜住。
                const appliesToCurrentThread = !data.threadId || data.threadId === current.activeThreadId;
                if (!appliesToCurrentThread) return;
                const turnId = data.turnId || current.activeTurnId;
                if (turnId) liveTurnKeysRef.current.add(`${current.activeThreadId}\0${turnId}`);
                const activeTurnId = busy ? turnId : "";
                const messages = activeTurnId ? bindPendingTurnMessages(current.messages, current.activeThreadId, activeTurnId) : current.messages;
                setAgentState({
                    activity: busy ? rt("codexRunning") : current.activity === rt("processingFailed") ? rt("processingFailed") : rt("completed"),
                    waiting: busy,
                    sending: false,
                    activeTurnId,
                    messages,
                });
                if (!busy && current.waiting) void loadThreads(false, turnId);
            });
        });
        // 画布写工具到达时按确认开关拦截：待确认期间再来的写工具直接回错误，保证一次只确认一个。
        source.addEventListener("tool_call", (event) => {
            if (!isCurrentConnection()) return;
            const data = parseEventData<AgentPendingToolCall>(event);
            if (data) void handleToolCall(endpoint, token, data);
        });
        // Codex 运行时审批（命令/文件/网络权限）请求：维护 pendingApprovals 列表并提示等待审批。
        source.addEventListener("codex_approval", (event) => {
            if (!isCurrentConnection()) return;
            const data = parseEventData<AgentPendingApproval>(event);
            if (!data || !isCurrentThreadEvent(data)) return;
            setAgentState({ pendingApprovals: [...useAgentStore.getState().pendingApprovals.filter((item) => item.requestId !== data.requestId), data], activity: rt("awaitingApproval") });
            addEventLog(rt("awaitingApproval"), data.reason || data.method, data);
        });
        source.addEventListener("codex_approval_resolved", (event) => {
            if (!isCurrentConnection()) return;
            const data = parseEventData<{ requestId?: string; decision?: "accept" | "acceptForSession" | "decline" | "cancel" }>(event);
            if (!data?.requestId) return;
            const current = useAgentStore.getState();
            const approval = current.pendingApprovals.find((item) => item.requestId === data.requestId);
            const pendingApprovals = current.pendingApprovals.filter((item) => item.requestId !== data.requestId);
            setAgentState({ pendingApprovals, activity: approvalActivity(pendingApprovals, current.waiting, current.activity) });
            const decision = data.decision || approval?.deciding;
            if (approval && decision) addEventLog(rt(decision === "accept" || decision === "acceptForSession" ? "approvalGranted" : "approvalCanceled"), approval.reason || approval.method, approval);
        });
        // 核心 Agent 事件流：先做线程归属与重放去重校验，usage 事件除外（它只更新用量条）。
        source.addEventListener("agent_event", (event) => {
            const data = parseEventData<AgentEventPayload>(event);
            if (data) enqueueEvent(() => {
                if (!isCurrentThreadEvent(data)) return;
                const shouldProcess = registerLiveAgentTurn(data, authoritativeHistoryTurnsRef.current, liveTurnKeysRef.current);
                if (data.type !== "usage.updated" && !shouldProcess) return;
                return handleAgentEvent(data);
            });
        });
        source.addEventListener("agent_bootstrap", (event) => {
            const data = parseEventData<AgentBootstrapEvent>(event);
            if (!data?.type) return;
            if (data.type === "codex.preparing") {
                addEventLog(rt("conversationInitializing"), rt("conversationCreating"), data);
                return;
            }
            if (data.type === "codex.prepare_failed") {
                addEventLog(rt("conversationInitFailed"), data.error, data);
                return;
            }
            if (data.type === "mcp.complete") {
                addEventLog(rt("mcpStatusComplete"), rt("mcpListRead"), data);
                return;
            }
            if (!data.name || !data.status) return;
            const label = data.name;
            const status = data.status === "starting"
                ? { text: rt("mcpStarting", { name: label }), detail: rt("mcpConnecting"), status: "running" as const }
                : data.status === "ready"
                    ? { text: rt("mcpReadyNamed", { name: label }), detail: rt("toolsReady"), status: "ready" as const }
                    : data.status === "failed"
                        ? { text: rt("mcpFailedNamed", { name: label }), detail: data.error || rt("toolInitFailed"), status: "error" as const }
                        : { text: rt("mcpCanceledNamed", { name: label }), detail: rt("toolInitCanceled"), status: "error" as const };
            addEventLog(status.text, status.detail, data);
        });
        source.addEventListener("conversation_changed", (event) => {
            const data = parseEventData<AgentConversationState>(event);
            if (data) enqueueEvent(() => { applyConversationState(data); });
        });
        source.addEventListener("workspace_changed", (event) => {
            const data = parseEventData<AgentWorkspaceEvent>(event);
            if (!data) return;
            enqueueEvent(() => {
                if (data.conversation) applyConversationState(data.conversation);
                else applyWorkspaceChange(data);
                if (!data.draftThread) void loadThreads(Boolean(data.emptyThread));
            });
        });
        source.addEventListener("chat_message", (event) => {
            const data = parseEventData<AgentChatEvent>(event);
            if (!data?.message) return;
            enqueueEvent(() => {
                if (!isCurrentThreadEvent(data)) return;
                if (!registerLiveAgentTurn(data, authoritativeHistoryTurnsRef.current, liveTurnKeysRef.current)) return;
                const current = useAgentStore.getState();
                const threadId = data.threadId || data.message!.threadId || current.activeThreadId;
                const turnId = data.turnId ?? data.message!.turnId ?? "";
                const clientMessageId = data.message!.clientMessageId || data.message!.itemId || data.message!.id;
                if (current.activeThreadId !== threadId) return;
                const next = scopeChatItem(data.message!, threadId, turnId);
                // 服务端回显的用户消息会替换本地乐观插入的那条（按 clientMessageId 匹配），避免重复。
                const currentMessages = data.message!.role === "user" && clientMessageId
                    ? current.messages.filter((item) => item.role !== "user" || item.clientMessageId !== clientMessageId || item.id === next.id)
                    : current.messages;
                const messages = upsertAgentMessage(currentMessages, next);
                setAgentState({ messages });
            });
        });
        source.addEventListener("agent_log", (event) => {
            if (!isCurrentConnection()) return;
            const text = parseEventData<{ text?: unknown }>(event)?.text;
            addEventLog(rt("log"), text, text);
        });
        source.addEventListener("skills_changed", (event) => {
            if (!isCurrentConnection()) return;
            const data = parseEventData<{ forceReload?: boolean }>(event);
            void loadSkills(endpoint, token, Boolean(data?.forceReload));
        });
        source.addEventListener("agent_error", (event) => {
            const data = parseEventData<AgentEventPayload>(event);
            if (!data) return;
            enqueueEvent(() => {
                if (!isCurrentThreadEvent(data)) return;
                if (!registerLiveAgentTurn(data, authoritativeHistoryTurnsRef.current, liveTurnKeysRef.current)) return;
                showAgentError(data.message, data, !data.replayed);
            });
        });
        // SSE 断连：已连接过提示「连接丢失」，从未连上则关闭连接并禁用，避免无限自动重连风暴。
        source.onerror = () => {
            if (disposed || protocolRejected) return;
            const wasConnected = connectedRef.current;
            const silent = useAgentStore.getState().silentConnect && !wasConnected;
            const text = rt(wasConnected ? "connectionLostDescription" : "connectionFailedDescription");
            if (!errorLoggedRef.current || wasConnected) {
                addEventLog(rt(wasConnected ? "connectionLost" : "connectionFailed"), text);
                if (!headless && !silent) message.error(text);
            }
            errorLoggedRef.current = true;
            connectedRef.current = false;
            pendingToolRef.current = null;
            setAgentState({
                activity: rt(wasConnected ? "connectionLost" : "connectionFailed"),
                connected: false,
                waiting: false,
                sending: false,
                connectError: silent ? "" : text,
                silentConnect: false,
                fragmentBootstrap: false,
                pendingTool: null,
                pendingApprovals: [],
            });
            useAgentSkillStore.getState().reset();
            if (!wasConnected) {
                source.close();
                setAgentState({ enabled: false });
            }
        };
        return () => {
            disposed = true;
            source.close();
            connectedRef.current = false;
            loadThreadsSequenceRef.current += 1;
            useAgentSkillStore.getState().reset();
        };
    }, [applyConversationState, applyWorkspaceChange, clientReady, enabled, endpoint, loadSkills, loadThreads, message, setAgentState, token]);

    useEffect(() => {
        if (connected) void loadThreads();
    }, [connected, loadThreads]);

    useEffect(() => {
        if (connected) void loadSkills(endpoint, token);
    }, [connected, endpoint, loadSkills, token]);

    useEffect(() => {
        if (!connected) return;
        // 拉取模型列表并归一化：过滤评审专用模型与重复项，回退用户上次选择或默认模型/力度。
        void fetchAgentJson<AgentModelsResponse>(endpoint, token, "/agent/codex/models").then(({ data = [] }) => {
            const names = new Set<string>();
            const models = data.flatMap((item) => {
                const name = item.displayName || item.model;
                const efforts = item.supportedReasoningEfforts.filter(({ reasoningEffort }) => AGENT_REASONING_EFFORTS.has(reasoningEffort));
                if (item.model === "codex-auto-review" || names.has(name) || !efforts.length) return [];
                names.add(name);
                const defaultReasoningEffort = efforts.some((effort) => effort.reasoningEffort === item.defaultReasoningEffort) ? item.defaultReasoningEffort : efforts[0].reasoningEffort;
                return [{ ...item, supportedReasoningEfforts: efforts, defaultReasoningEffort }];
            });
            if (!models.length) return;
            const savedModel = useAgentStore.getState().model;
            const current = models.find((item) => item.model === savedModel) || models.find((item) => item.isDefault) || models[0];
            const savedEffort = useAgentStore.getState().reasoningEffort;
            const efforts = current.supportedReasoningEfforts.map((item) => item.reasoningEffort);
            const nextEffort = efforts.includes(savedEffort as AgentReasoningEffort) ? savedEffort as AgentReasoningEffort : current.defaultReasoningEffort || efforts[0];
            localStorage.setItem("canvas-agent-model", current.model);
            localStorage.setItem("canvas-agent-reasoning-effort", nextEffort);
            setAgentState({ models, model: current.model, reasoningEffort: nextEffort });
        }).catch((error) => addEventLog(rt("modelListFailed"), error));
    }, [connected, endpoint, setAgentState, token]);

    useEffect(() => {
        if (!connected) return;
        // 窗口重新获得焦点/可见时激活本客户端：多标签页场景下让服务端把实时事件路由到当前标签。
        const activate = () => void activateAgentClient(endpoint, token, clientIdRef.current);
        const activateVisible = () => {
            if (document.visibilityState === "visible") activate();
        };
        window.addEventListener("focus", activate);
        document.addEventListener("visibilitychange", activateVisible);
        return () => {
            window.removeEventListener("focus", activate);
            document.removeEventListener("visibilitychange", activateVisible);
        };
    }, [connected, endpoint, token]);
    /** 发送用户消息：合并附件与画布引用、乐观插入本地用户消息，再提交 turn 请求并处理失败回滚。 */
    const sendPrompt = async () => {
        const text = prompt.trim();
        const files = attachments;
        const skillState = useAgentSkillStore.getState();
        const selectedSkill = skillState.selectedSkill;
        const selectedSkillRevision = skillState.selectionRevision;
        const currentState = useAgentStore.getState();
        const canvasNodeIds = new Set(currentState.canvasContext?.snapshot.nodes.map((node) => node.id) || []);
        // 画布引用先按当前快照过滤：节点已被删除的引用直接剔除并提示。
        const canvasReferences = currentState.canvasReferences.filter((item) => canvasNodeIds.has(item.nodeId));
        if (canvasReferences.length !== currentState.canvasReferences.length) {
            setAgentState({ canvasReferences });
            message.warning(rt(canvasReferences.length ? "someCanvasReferencesMissing" : "canvasReferencesMissing"));
        }
        const requestPrompt = promptWithCanvasReferences(promptWithAttachments(text, files), canvasReferences);
        if (!currentState.connected || !requestPrompt || currentState.sending || currentState.waiting || currentState.loadingThreads || !["ready", "warning"].includes(currentState.conversation.status)) return;
        let referenceImages: AgentAttachment[] = [];
        if (canvasReferences.some((item) => item.kind === "image")) {
            setAgentState({ sending: true, activity: rt("readingCanvasImages") });
            try {
                referenceImages = await resolveCanvasReferenceImages(canvasReferences, currentState.canvasContext?.snapshot.nodes || []);
            } catch (error) {
                setAgentState({ sending: false, activity: rt("canvasImageReadFailed") });
                addMessage({ role: "error", title: rt("canvasImageReadFailed"), text: error instanceof Error ? error.message : rt("canvasImageReadFailed") });
                return;
            }
        }
        const requestFiles = [...files, ...referenceImages.filter((reference) => !files.some((file) => file.dataUrl === reference.dataUrl))];
        if (requestFiles.length > MAX_ATTACHMENTS) {
            setAgentState({ sending: false, activity: rt("tooManyImages") });
            addMessage({ role: "error", title: rt("tooManyImages"), text: rt("imageCountLimit", { count: MAX_ATTACHMENTS }) });
            return;
        }
        if (attachmentPayloadBytes(requestFiles) > MAX_ATTACHMENT_PAYLOAD_BYTES) {
            setAgentState({ sending: false, activity: rt("imageTooLarge") });
            addMessage({ role: "error", title: rt("imageTooLarge"), text: rt("imagePayloadTooLarge") });
            return;
        }
        const messageId = createId();
        // 纯图片/纯引用发送时生成占位文案；引用的预览图换成降采样后的消息附件地址。
        const userText = text || rt(files.length ? "imagesSent" : "canvasReferencesSent", { count: files.length || canvasReferences.length });
        const messageReferences: AgentCanvasReference[] = await Promise.all(canvasReferences.map(async ({ nodeId, label, title, kind, previewUrl, text }) => {
            const image = referenceImages.find((item) => item.id === `canvas:${nodeId}`);
            return { nodeId, label, title, kind, previewUrl: image ? (await createMessageAttachmentMetadata(image)).url : previewUrl, text };
        }));
        const messageSkill = selectedSkill ? { name: selectedSkill.name, path: selectedSkill.path, displayName: selectedSkill.interface?.displayName || undefined } : undefined;
        loadThreadsSequenceRef.current += 1;
        const currentBeforeSend = useAgentStore.getState();
        const requestThreadId = currentBeforeSend.activeThreadId;
        // 乐观清空输入并插入本地用户消息（itemId 固定为 synthetic:user，turnId 留空待 turn.started 绑定）。
        setAgentState({ prompt: "", attachments: [], canvasReferences: [], activity: rt("sending"), sending: true, loadingThreads: false, activeTurnId: "", messages: currentBeforeSend.messages });
        addMessage({ id: messageId, itemId: "synthetic:user", clientMessageId: messageId, threadId: requestThreadId, turnId: "", role: "user", text: userText, attachments: files, canvasReferences: messageReferences, skill: messageSkill });
        let threadId = requestThreadId;
        try {
            const messageAttachments = await Promise.all(files.map(createMessageAttachmentMetadata));
            const messageMetadata = {
                ...(messageAttachments.length ? { attachments: messageAttachments } : {}),
                ...(messageReferences.length ? { canvasReferences: messageReferences } : {}),
                ...(messageSkill ? { skill: messageSkill } : {}),
            };
            const modelName = models.find((item) => item.model === model)?.displayName || model || rt("defaultModel");
            const effortName = reasoningEffort ? i18n.t(`agent.composer.effort.${reasoningEffort}`) : rt("defaultEffort");
            addEventLog(rt("sendTask"), `${modelName} · ${effortName}${selectedSkill ? ` · Skill ${selectedSkill.name}` : ""}${files.length ? ` · ${rt("attachmentCount", { count: files.length })}` : ""}${canvasReferences.length ? ` · ${rt("canvasReferenceCount", { count: canvasReferences.length })}` : ""} · ${compactText(text) || rt(canvasReferences.length ? "canvasReferencesOnly" : "attachmentsOnly")}`);
            const accepted = await fetchAgentJson<AgentTurnResponse>(endpoint, token, "/agent/codex/turn", {
                method: "POST",
                headers: { "content-type": "application/json" },
                body: JSON.stringify({
                    prompt: requestPrompt,
                    messageText: userText,
                    messageId,
                    clientId: clientIdRef.current,
                    threadId,
                    conversationId: currentBeforeSend.conversation.conversationId,
                    expectedRevision: currentBeforeSend.conversation.revision,
                    permissionMode,
                    model,
                    effort: reasoningEffort,
                    skill: selectedSkill ? { name: selectedSkill.name, path: selectedSkill.path } : undefined,
                    attachments: requestFiles.map(({ id, name, type, size, width, height, dataUrl }) => ({ id, name, type, size, width, height, dataUrl })),
                    messageMetadata,
                }),
            });
            threadId = accepted.threadId || threadId;
            if (!threadId) throw new Error(rt("startConversationFailed"));
            if (selectedSkill) clearSkillSelection(selectedSkillRevision);
            files.forEach((item) => {
                URL.revokeObjectURL(item.url);
                attachmentUrlsRef.current.delete(item.url);
            });
        } catch (error) {
            const text = error instanceof Error ? error.message : rt("sendFailed");
            const response = error instanceof AgentApiError ? error.response as { code?: string; state?: AgentConversationState } : undefined;
            if (response?.state) applyConversationState(response.state);
            // 失败分类：stale 表示会话已被他端推进（草稿作废），busy 表示 turn 还在跑（消息未发出但会话仍在）。
            const stale = response?.code === "CONVERSATION_STALE";
            const busy = response?.code === "CONVERSATION_BUSY" || text.includes("Codex 正在运行");
            const state = useAgentStore.getState();
            const removeFailedPending = (messages: AgentChatItem[]) => messages.filter((item) => item.clientMessageId !== messageId || Boolean(item.turnId));
            // 从所有线程缓存里撤回这条失败的用户消息（已绑定 turnId 的说明服务端已接受，保留）。
            threadMessagesRef.current.forEach((messages, cachedThreadId) => {
                const next = removeFailedPending(messages);
                if (next.length !== messages.length) threadMessagesRef.current.set(cachedThreadId, next);
            });
            const ownsCurrentThread = state.activeThreadId === (threadId || requestThreadId);
            // 输入框若已被用户重新填写则不回滚草稿，避免覆盖新输入。
            const restoreDraft = state.prompt || state.attachments.length || state.canvasReferences.length ? {} : { prompt, attachments: files, canvasReferences };
            if (ownsCurrentThread) {
                setAgentState({
                    activity: rt(stale ? "conversationSynced" : busy ? "codexRunning" : "sendFailed"),
                    sending: false,
                    messages: removeFailedPending(state.messages),
                    ...restoreDraft,
                });
                addMessage({ threadId: state.activeThreadId, turnId: "", role: "error", title: rt(stale ? "conversationSynced" : busy ? "taskStillRunning" : "sendFailed"), text });
            } else {
                setAgentState({ sending: false, messages: removeFailedPending(state.messages), ...restoreDraft });
            }
            addEventLog(rt("sendFailed"), error);
        }
    };

    /** 中断当前 turn：仅在有任务进行中时可用。 */
    const stopTurn = async () => {
        if (!connected || (!sending && !waiting)) return;
        setAgentState({ activity: rt("stopping") });
        try {
            await interruptCodexTurn(endpoint, token, useAgentStore.getState().activeThreadId || undefined);
            addEventLog(rt("stopTask"), rt("taskStopped"));
        } catch (error) {
            setAgentState({ activity: rt("stopFailed") });
            addEventLog(rt("stopFailed"), error);
        }
    };

    /** 添加图片附件：读取尺寸元数据并登记 objectURL，总负载超限则整体拒绝。 */
    const addAttachments = async (files: FileList | File[] | null) => {
        if (!files) return;
        const images = Array.from(files).filter((file) => file.type.startsWith("image/"));
        const prev = useAgentStore.getState().attachments;
        try {
            const next = await Promise.all(
                images.slice(0, Math.max(0, MAX_ATTACHMENTS - prev.length)).map(async (file) => {
                    const dataUrl = await readDataUrl(file);
                    const meta = await readImageMeta(dataUrl);
                    const url = URL.createObjectURL(file);
                    attachmentUrlsRef.current.add(url);
                    return { id: createId(), name: file.name, type: file.type, size: file.size, width: meta.width, height: meta.height, url, dataUrl };
                }),
            );
            const merged = [...prev, ...next];
            if (attachmentPayloadBytes(merged) > MAX_ATTACHMENT_PAYLOAD_BYTES) {
                next.forEach((item) => {
                    URL.revokeObjectURL(item.url);
                    attachmentUrlsRef.current.delete(item.url);
                });
                addMessage({ role: "error", title: rt("imageTooLarge"), text: rt("imageLimit") });
                return;
            }
            if (next.length) setAgentState({ attachments: merged });
        } catch (error) {
            addMessage({ role: "error", title: rt("imageReadFailed"), text: error instanceof Error ? error.message : rt("imageReadFailed") });
        }
    };

    const removeAttachment = (id: string) => {
        const removed = attachments.find((item) => item.id === id);
        if (removed) {
            URL.revokeObjectURL(removed.url);
            attachmentUrlsRef.current.delete(removed.url);
        }
        setAgentState({ attachments: attachments.filter((item) => item.id !== id) });
    };

    /** 画布写工具的确认入口：开启确认开关时挂起等待用户批准，否则直接执行。 */
    const handleToolCall = async (endpoint: string, token: string, payload: AgentPendingToolCall) => {
        if (confirmToolsRef.current && isCanvasWriteTool(payload.name)) {
            if (pendingToolRef.current) {
                await postToolResult(endpoint, token, clientIdRef.current, { requestId: payload.requestId, error: rt("pendingCanvasTool") });
                return;
            }
            pendingToolRef.current = payload;
            setAgentState({ pendingTool: payload });
            addEventLog(rt("awaitingConfirmation"), payload, payload);
            return;
        }
        await runToolCall(endpoint, token, payload);
    };

    /** 执行工具调用：站点内工具直接在前端执行，画布/导航类工具操作画布上下文并把结果回传 Agent。 */
    const runToolCall = async (endpoint: string, token: string, payload: AgentPendingToolCall) => {
        if (isSiteTool(payload.name)) {
            try {
                addEventLog(toolName(payload.name), payload, payload);
                const result = await runSiteTool(payload.name, payload.input || {}, navigate, { canvasSnapshot: canvasContextRef.current?.snapshot || null });
                await postToolResult(endpoint, token, clientIdRef.current, { requestId: payload.requestId, result });
                addEventLog(rt("toolCompleted", { tool: toolName(payload.name) }), result, result);
            } catch (error) {
                const message = error instanceof Error ? error.message : rt("toolExecutionFailed");
                await postToolResult(endpoint, token, clientIdRef.current, { requestId: payload.requestId, error: message });
            }
            return;
        }
        try {
            const input: { ops?: CanvasAgentOp[]; path?: string } = payload.input || {};
            addEventLog(toolName(payload.name), payload, payload);
            let result: unknown;
            let appliedOps = input.ops || [];
            if (payload.name === "site_navigate") {
                const path = input.path || "/";
                navigate(path);
                result = { ok: true, path };
            } else if (payload.name === "canvas_apply_ops") {
                const context = canvasContextRef.current;
                if (!context) throw new Error(rt("openCanvasFirst"));
                result = context.applyOps(appliedOps);
                void postState(endpoint, token, clientIdRef.current, result as CanvasAgentSnapshot);
            } else if (payload.name === "canvas_create_attachment_nodes") {
                const context = canvasContextRef.current;
                if (!context) throw new Error(rt("openCanvasFirst"));
                appliedOps = await attachmentNodeOps(endpoint, token, clientIdRef.current, payload.input?.nodes);
                result = context.applyOps(appliedOps);
                await postState(endpoint, token, clientIdRef.current, result as CanvasAgentSnapshot);
            } else {
                const snapshot = canvasContextRef.current?.snapshot;
                if (!snapshot) throw new Error(rt("openCanvasFirst"));
                result = snapshot;
            }
            await postToolResult(endpoint, token, clientIdRef.current, { requestId: payload.requestId, result });
            addEventLog(rt("toolCompleted", { tool: toolName(payload.name) }), result, result);
        } catch (error) {
            const message = error instanceof Error ? error.message : rt("canvasOperationFailed");
            await postToolResult(endpoint, token, clientIdRef.current, { requestId: payload.requestId, error: message });
        }
    };

    /** 拒绝待确认的画布写工具：把拒绝原因作为错误结果回传给 Agent。 */
    const rejectPendingTool = async () => {
        if (!pendingTool) return;
        await postToolResult(endpoint, token, clientIdRef.current, { requestId: pendingTool.requestId, error: rt("canvasToolCanceled") });
        pendingToolRef.current = null;
        setAgentState({ pendingTool: null });
    };

    /** 批准待确认的画布写工具并立即执行。 */
    const approvePendingTool = async () => {
        if (!pendingTool) return;
        const tool = pendingTool;
        pendingToolRef.current = null;
        setAgentState({ pendingTool: null });
        await runToolCall(endpoint, token, tool);
    };

    /** 提交 Codex 运行时审批决定；审批已失效则静默移除，否则失败时回退按钮状态。 */
    const decideApproval = async (approval: AgentPendingApproval, decision: "accept" | "acceptForSession" | "decline") => {
        const current = useAgentStore.getState();
        const pending = current.pendingApprovals.find((item) => item.requestId === approval.requestId);
        if (!pending || pending.deciding) return;
        setAgentState({ pendingApprovals: current.pendingApprovals.map((item) => item.requestId === approval.requestId ? { ...item, deciding: decision } : item), activity: rt("submittingApproval") });
        try {
            await postCodexApproval(endpoint, token, approval.requestId, decision);
            const latest = useAgentStore.getState();
            if (latest.pendingApprovals.some((item) => item.requestId === approval.requestId)) setAgentState({ activity: rt("waitingCodexApproval") });
        } catch (error) {
            const latest = useAgentStore.getState();
            const expired = error instanceof Error && error.message.includes("审批请求已失效");
            const resolved = expired || !latest.pendingApprovals.some((item) => item.requestId === approval.requestId);
            const pendingApprovals = resolved
                ? latest.pendingApprovals.filter((item) => item.requestId !== approval.requestId)
                : latest.pendingApprovals.map((item) => item.requestId === approval.requestId ? { ...item, deciding: undefined } : item);
            setAgentState({ pendingApprovals, activity: approvalActivity(pendingApprovals, latest.waiting, latest.activity) });
            if (resolved) return;
            addEventLog(rt("approvalFailed"), error);
            message.error(error instanceof Error ? error.message : rt("approvalFailed"));
        }
    };

    /** 切换权限模式：「完全访问」需要弹窗二次确认。 */
    const changePermissionMode = (nextMode: AgentPermissionMode) => {
        const apply = () => {
            localStorage.setItem("canvas-agent-permission-mode", nextMode);
            setAgentState({ permissionMode: nextMode });
        };
        if (nextMode !== "full") return apply();
        modal.confirm({
            title: rt("enableFullAccess"),
            content: rt("fullAccessDescription"),
            okText: rt("enableFullAccessAction"),
            okType: "danger",
            cancelText: t("common.cancel"),
            onOk: apply,
        });
    };

    /** 连接/断开总开关：地址与 token 支持 URL 参数覆盖与本地发现，校验通过后触发 SSE 建连。 */
    const toggleAgentConnection = async ({ silent = false }: { silent?: boolean } = {}) => {
        if (enabled) {
            clearAgentSession({ enabled: false, connected: false, activity: rt("offline"), connectError: "" });
            return;
        }
        const urlToken = searchParams.get("agentToken") || "";
        const urlEndpoint = searchParams.get("agentUrl") || "";
        const discovered = urlToken ? null : await discoverAgentConfig(endpoint || DEFAULT_AGENT_URL);
        const nextEndpoint = (urlEndpoint || discovered?.url || endpoint || DEFAULT_AGENT_URL).trim().replace(/\/$/, "");
        const nextToken = (urlToken || token.trim() || discovered?.token || "").trim();
        if (!nextEndpoint) {
            const text = rt("addressRequired");
            if (!silent) {
                setAgentState({ connectError: text });
                if (!headless) message.warning(text);
            }
            return;
        }
        if (!nextToken) {
            const text = rt("agentNotFound");
            if (!silent) {
                setAgentState({ connectError: text });
                if (!headless) message.warning(text);
            }
            return;
        }
        try {
            const parsed = new URL(nextEndpoint);
            if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("invalid protocol");
        } catch {
            const text = rt("invalidAddress");
            if (!silent) {
                setAgentState({ connectError: text });
                if (!headless) message.warning(text);
            }
            return;
        }
        errorLoggedRef.current = false;
        setAgentState({ url: nextEndpoint, token: nextToken, enabled: true, connected: false, silentConnect: silent, fragmentBootstrap: false, activity: rt("connecting"), connectError: "", activeTab: "setup" });
    };

    useLayoutEffect(() => {
        // URL hash 引导（fragmentBootstrap）：从分享链接中读取 agentUrl/agentToken 后清掉 hash 并静默连接。
        const bootstrap = readAgentUrlBootstrap(hash);
        if (!bootstrap) return;
        navigate(`${window.location.pathname}${window.location.search}${bootstrap.remainingHash}`, { replace: true });
        if (!bootstrap.url || !bootstrap.token) {
            setAgentState({ fragmentBootstrap: false, activeTab: "setup", connectError: rt(!bootstrap.url ? "addressRequired" : "agentNotFound") });
            useAgentStore.getState().openPanel();
            return;
        }
        try {
            const parsed = new URL(bootstrap.url);
            if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("invalid protocol");
        } catch {
            setAgentState({ fragmentBootstrap: false, activeTab: "setup", connectError: rt("invalidAddress") });
            useAgentStore.getState().openPanel();
            return;
        }
        errorLoggedRef.current = false;
        setAgentState({ url: bootstrap.url.replace(/\/$/, ""), token: bootstrap.token, enabled: true, connected: false, silentConnect: true, fragmentBootstrap: true, confirmTools: false, activity: rt("connecting"), connectError: "", activeTab: "setup" });
    }, [hash, navigate, setAgentState]);

    useEffect(() => {
        if (urlAgentAutoConnect && confirmTools) setAgentState({ confirmTools: false });
    }, [confirmTools, setAgentState, urlAgentAutoConnect]);

    useEffect(() => {
        if ((!autoConnect && !urlAgentAutoConnect) || autoConnectRef.current || enabled || connected) return;
        autoConnectRef.current = true;
        void toggleAgentConnection({ silent: true });
    }, [autoConnect, connected, enabled, urlAgentAutoConnect]);

    /** 清空本地会话全部状态（消息、缓存、turn 集合、审批等），patch 允许覆盖部分字段。 */
    function clearAgentSession(patch: Parameters<typeof setAgentState>[0] = {}) {
        loadThreadsSequenceRef.current += 1;
        threadMessagesRef.current.clear();
        authoritativeHistoryTurnsRef.current.clear();
        liveTurnKeysRef.current.clear();
        threadOperationRef.current = 0;
        setAgentState({
            messages: [],
            tokenUsage: null,
            threads: [],
            activeThreadId: "",
            activeTurnId: "",
            workspacePath: "",
            loadingThreads: false,
            waiting: false,
            sending: false,
            fragmentBootstrap: false,
            pendingTool: null,
            pendingApprovals: [],
            conversation: { revision: 0, conversationId: "", threadId: "", status: "idle", mcpStatuses: {} },
            bootstrapStatus: null,
            mcpStartupStatuses: {},
            ...patch,
        });
        useAgentSkillStore.getState().reset();
        pendingToolRef.current = null;
    }

    // 线程操作序列号：新建/恢复/删除线程期间阻止其它操作并发进入，并驱动 loadingThreads 状态。
    const beginThreadOperation = () => {
        const operation = ++threadOperationSequenceRef.current;
        threadOperationRef.current = operation;
        setAgentState({ loadingThreads: true });
        return operation;
    };

    const finishThreadOperation = (operation: number) => {
        if (threadOperationRef.current !== operation) return;
        threadOperationRef.current = 0;
        setAgentState({ loadingThreads: false });
    };

    /** 新建线程：调用服务端 reset 建立新会话，并清空本地技能选择。 */
    const startNewThread = async () => {
        const current = useAgentStore.getState();
        if (!current.connected || current.sending || current.waiting || current.loadingThreads || ["preparing", "running"].includes(current.conversation.status)) return;
        const operation = beginThreadOperation();
        clearSkillSelection();
        setAgentState({ activeTab: "chat", activity: rt("creatingConversation") });
        try {
            const result = await fetchAgentJson<AgentWorkspaceResponse>(endpoint, token, "/agent/codex/threads/reset", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ clientId: clientIdRef.current, permissionMode }) });
            if (threadOperationRef.current !== operation) return;
            if (result.conversation) applyConversationState(result.conversation);
            setAgentState({ activeTab: "chat", activity: rt("newConversation") });
        } catch (error) {
            const state = agentErrorState(error);
            if (state) applyConversationState(state);
            addEventLog(rt("newConversationFailed"), error);
            message.error(error instanceof Error ? error.message : rt("newConversationFailed"));
            await loadThreads();
        } finally {
            finishThreadOperation(operation);
        }
    };

    /** 恢复历史线程：切换服务端激活线程后重拉列表并合并快照。 */
    const resumeThread = async (threadId: string) => {
        const current = useAgentStore.getState();
        if (!current.connected || !threadId || current.sending || current.waiting || current.loadingThreads || ["preparing", "running"].includes(current.conversation.status)) return;
        const operation = beginThreadOperation();
        try {
            const result = await fetchAgentJson<AgentThreadResponse>(endpoint, token, `/agent/codex/threads/${encodeURIComponent(threadId)}/resume`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ permissionMode, clientId: clientIdRef.current }) });
            if (result.conversation) applyConversationState(result.conversation);
            await loadThreads();
            if (useAgentStore.getState().activeThreadId === threadId) setAgentState({ activeTab: "chat", activity: rt("conversationResumed") });
        } catch (error) {
            const state = agentErrorState(error);
            if (state) applyConversationState(state);
            addEventLog(rt("resumeConversationFailed"), error);
            message.error(error instanceof Error ? error.message : rt("resumeConversationFailed"));
            await loadThreads();
        } finally {
            finishThreadOperation(operation);
        }
    };

    /** 批量删除线程：逐个调用删除接口并同步清理本地消息缓存。 */
    const deleteThreads = async (threadIds: string[]) => {
        if (!connected || !threadIds.length || sending || waiting || loadingThreads) return;
        const operation = beginThreadOperation();
        let deletedCount = 0;
        try {
            for (const threadId of new Set(threadIds)) {
                await fetchAgentJson(endpoint, token, `/agent/codex/threads/${encodeURIComponent(threadId)}/delete`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ clientId: clientIdRef.current }) });
                threadMessagesRef.current.delete(threadId);
                deletedCount += 1;
            }
            await loadThreads();
            message.success(rt("recordsDeleted", { count: deletedCount }));
        } catch (error) {
            await loadThreads();
            addEventLog(rt("deleteConversationFailed"), error);
            message.error(error instanceof Error ? error.message : rt("deleteConversationFailed"));
        } finally {
            finishThreadOperation(operation);
        }
    };

    const confirmDeleteThreads = (threadIds: string[]) => {
        modal.confirm({
            title: rt("deleteConversations", { count: threadIds.length }),
            content: rt("deleteConversationsDescription"),
            okText: t("common.delete"),
            okType: "danger",
            cancelText: t("common.cancel"),
            onOk: () => deleteThreads(threadIds),
        });
    };

    /** 插入或更新一条本地消息（错误提示、乐观用户消息等），统一 scope 到当前线程。 */
    const addMessage = (item: Omit<AgentChatItem, "id"> & { id?: string }) => {
        const text = normalizeText(item.text);
        if (!text && !item.attachments?.length) return;
        const current = useAgentStore.getState();
        const itemId = item.itemId || item.id || createId();
        const next = scopeChatItem({ ...item, id: item.id || itemId, itemId, text } as AgentChatItem, item.threadId ?? current.activeThreadId, item.turnId ?? current.activeTurnId);
        setAgentState({ messages: upsertAgentMessage(current.messages, next) });
    };

    /** 追加事件日志：与上一条标题+内容完全相同时去重不记。 */
    const addEventLog = (title: string, text: unknown, raw?: unknown) => {
        const value = normalizeText(text) || title;
        const last = useAgentStore.getState().eventLogs.at(-1);
        if (last?.title === title && last.text === value) return;
        pushEventLog({ id: `${Date.now()}-${Math.random()}`, time: dayjs().format("YYYY-MM-DD HH:mm:ss"), title, text: value, raw });
    };

    /** 事件驱动的活动消息（推理/命令/计划）统一走 upsert。 */
    const upsertActivityMessage = (item: AgentChatItem) => {
        setAgentState({ messages: upsertAgentMessage(useAgentStore.getState().messages, item) });
    };

    /**
     * 处理 item.updated 的增量：推理摘要按 item.id 聚合进 activityItems；
     * 命令输出按 delta 追加；其它活动用 mergeStreamText 处理可能整段重发的文本。
     */
    const appendActivityDelta = (event: AgentEventPayload) => {
        const item = event.item;
        if (!item?.id) return;
        const text = stringText(item.text) || stringText(item.delta);
        const isDelta = Boolean(stringText(item.delta));
        if (!text) return;
        if (item.type === "reasoning") {
            const scoped = scopeEventChatItem(event, activityDeltaFallback(item, text), "synthetic:reasoning");
            const current = useAgentStore.getState().messages.find((message) => message.id === scoped.id);
            const activityItems = { ...(current?.activityItems || {}) };
            const previous = activityItems[item.id] || "";
            activityItems[item.id] = isDelta ? `${previous === activityPlaceholder("reasoning") ? "" : previous}${text}` : text;
            upsertActivityMessage({ ...scoped, title: i18n.t("agent.events.reasoning"), text: reasoningActivityText(activityItems), activityItems, detail: activityDetail(current?.detail || scoped.detail, "reasoning", "inProgress") });
            return;
        }
        const scoped = scopeEventChatItem(event, activityDeltaFallback(item, text), item.id);
        const currentMessages = useAgentStore.getState().messages;
        const index = currentMessages.findIndex((message) => message.id === scoped.id);
        if (index < 0) {
            if (!text.trim()) return;
            upsertActivityMessage(scoped);
            return;
        }
        const current = currentMessages[index];
        if (item.type === "command_execution") {
            const detail = activityDetail(current.detail, "command", "inProgress");
            detail.output = isDelta ? `${stringText(detail.output)}${text}` : text;
            setAgentState({ messages: currentMessages.map((message, itemIndex) => itemIndex === index ? { ...message, detail } : message) });
            return;
        }
        const placeholder = activityPlaceholder(item.type);
        if (!text.trim() && current.text === placeholder) return;
        const nextText = isDelta ? `${current.text === placeholder ? "" : current.text}${text}` : mergeStreamText(current.text, text);
        setAgentState({ messages: currentMessages.map((message, itemIndex) => itemIndex === index ? { ...message, text: nextText, detail: { ...activityDetail(message.detail, activityKind(item.type), "inProgress") } } : message) });
    };

    /** item.completed / plan.updated 到达时把格式化好的活动卡片 upsert 进时间线。 */
    const upsertEventActivity = (event: AgentEventPayload, item: Omit<AgentChatItem, "id">) => {
        const itemId = event.item?.id;
        if (!itemId) return;
        if (event.item?.type === "reasoning") {
            const scoped = scopeEventChatItem(event, { ...item, id: "synthetic:reasoning" }, "synthetic:reasoning");
            const current = useAgentStore.getState().messages.find((message) => message.id === scoped.id);
            const activityItems = { ...(current?.activityItems || {}) };
            const previous = activityItems[itemId] || "";
            const incoming = normalizeText(item.text);
            activityItems[itemId] = incoming === "已完成分析" && previous && previous !== activityPlaceholder("reasoning") ? previous : incoming;
            upsertActivityMessage({ ...scoped, title: i18n.t("agent.events.reasoning"), text: reasoningActivityText(activityItems, incoming), activityItems });
            return;
        }
        upsertActivityMessage(scopeEventChatItem(event, { ...item, id: itemId }, itemId));
    };

    /** 推理块完成时移除对应摘要；所有摘要都无效则整条推理消息删除，避免留下空「分析中」卡片。 */
    const finishEmptyReasoningActivity = (event: AgentEventPayload) => {
        const itemId = event.item?.id;
        if (!itemId) return;
        const scopedId = scopeEventChatItem(event, { id: "synthetic:reasoning", role: "tool", text: "" }, "synthetic:reasoning").id;
        const currentMessages = useAgentStore.getState().messages;
        const index = currentMessages.findIndex((message) => message.id === scopedId);
        if (index < 0) return;
        const current = currentMessages[index];
        const activityItems = { ...(current.activityItems || {}) };
        delete activityItems[itemId];
        if (!Object.values(activityItems).some(isReasoningSummary)) {
            setAgentState({ messages: currentMessages.filter((_, itemIndex) => itemIndex !== index) });
            return;
        }
        setAgentState({ messages: currentMessages.map((message, itemIndex) => itemIndex === index ? { ...message, text: reasoningActivityText(activityItems), activityItems, detail: activityDetail(message.detail, "reasoning", "completed") } : message) });
    };

    /** turn 结束时把计划卡片状态收敛为最终态（完成/失败/中断）。 */
    const finishPlanActivity = (event: AgentEventPayload) => {
        const id = scopeEventChatItem(event, { id: "synthetic:plan", role: "tool", text: "" }, "synthetic:plan").id;
        const currentMessages = useAgentStore.getState().messages;
        const index = currentMessages.findIndex((message) => message.id === id);
        if (index < 0) return;
        const current = currentMessages[index];
        const detail = activityDetail(current.detail, "todo", turnPlanStatus(current.detail, event.status));
        setAgentState({ messages: currentMessages.map((message, itemIndex) => itemIndex === index ? { ...message, detail } : message) });
    };

    /** 展示错误消息：同一 ID 已存在且无新内容时跳过，避免重放事件重复刷错误。 */
    const showAgentError = (value: unknown, event?: AgentEventPayload, log = true) => {
        const error = agentErrorView(value);
        const item = event
            ? scopeEventChatItem(event, { id: "synthetic:error", role: "error", title: error.title, text: error.text }, "synthetic:error")
            : scopeChatItem({ id: createId(), role: "error", title: error.title, text: error.text }, useAgentStore.getState().activeThreadId, useAgentStore.getState().activeTurnId);
        const state = useAgentStore.getState();
        const current = state.messages.find((message) => message.id === item.id);
        if (current && !normalizeText(value)) return;
        upsertActivityMessage(item);
        setAgentState({ activity: rt("processingFailed"), pendingApprovals: [] });
        if (log) addEventLog(rt("processingFailed"), error.text, value);
    };

    /**
     * 事件处理主入口：按事件类型分派到日志、活动卡片、流式正文、计划与用量等分支。
     * turn.started 绑定 pending 用户消息；item.completed(agent_message) 以服务端全文覆盖流式文本；
     * turn.completed 收尾 turn 并清 streamId；图片生成结果还会尝试导入画布。
     */
    const handleAgentEvent = async (event: AgentEventPayload) => {
        if (event.type === "usage.updated") setAgentState({ tokenUsage: eventUsage(event) });
        const log = event.replayed ? null : formatAgentEventLog(event);
        const activity = formatAgentActivity(event);
        if (log) addEventLog(log.title, log.text);
        if (event.type === "turn.started" && (event.turnId || event.turn_id)) {
            const scope = eventScope(event);
            const current = useAgentStore.getState();
            if (!scope.threadId || !scope.turnId) return;
            liveTurnKeysRef.current.add(`${scope.threadId}\0${scope.turnId}`);
            setAgentState({ activeTurnId: scope.turnId, bootstrapStatus: null, mcpStartupStatuses: {}, messages: bindPendingTurnMessages(current.messages, scope.threadId, scope.turnId) });
        }
        if (event.type === "item.updated" && event.item?.type === "agent_message" && event.item.id) {
            const delta = stringText(event.item.delta);
            appendStreamText(event, delta || stringText(event.item.text), Boolean(delta));
            return;
        }
        if (event.type === "item.updated" && event.item) {
            appendActivityDelta(event);
            return;
        }
        if (event.type === "plan.updated" && event.turn_id) {
            const plan = formatAgentPlan(event);
            if (plan) upsertActivityMessage(scopeEventChatItem(event, { ...plan, id: "synthetic:plan" }, "synthetic:plan"));
            return;
        }
        if (event.type === "item.completed" && event.item?.type === "error") {
            showAgentError(event.item.message, event, !event.replayed);
            return;
        }
        if (event.type === "item.completed" && event.item?.type === "agent_message" && event.item.id) {
            const scoped = scopeEventChatItem(event, { id: event.item.id, role: "assistant", title: "Codex", text: stringText(event.item.text) }, event.item.id);
            const currentMessages = useAgentStore.getState().messages;
            const index = currentMessages.findIndex((message) => message.id === scoped.id);
            if (index >= 0) {
                const text = stringText(event.item.text);
                setAgentState({ messages: currentMessages.map((message, itemIndex) => itemIndex === index ? { ...message, text: text || message.text, streamId: undefined } : message) });
                return;
            }
            addMessage(scoped);
            return;
        }
        if (event.type === "item.completed" && event.item?.type === "reasoning" && !activity) {
            finishEmptyReasoningActivity(event);
            return;
        }
        if (event.type === "item.completed" && !activity && event.item?.id && event.item.type === "plan") {
            const id = scopeEventChatItem(event, { id: event.item.id, role: "tool", text: "" }, event.item.id).id;
            setAgentState({ messages: useAgentStore.getState().messages.filter((item) => item.id !== id) });
            return;
        }
        if (!event.replayed && event.type === "item.completed" && event.item?.type === "image_generation" && event.item.id && event.sourceClientId === clientIdRef.current) {
            // 只有本客户端产生的图片生成结果才导入画布，且放在现有节点最右侧，纵向按序错开。
            const generated = await importGeneratedImages(endpoint, token, event.item);
            if (generated.length) {
                const context = canvasContextRef.current;
                if (context) {
                    const right = Math.max(0, ...context.snapshot.nodes.map((node) => node.position.x + node.width)) + 80;
                    const ops = generated.map<CanvasAgentOp>((image, index) => {
                        const size = fitNodeSize(image.upload.width, image.upload.height);
                        return {
                            type: "add_node",
                            id: `image-${createId()}`,
                            nodeType: "image",
                            title: image.name,
                            position: { x: right + index * 40, y: index * 40 },
                            ...size,
                            metadata: imageMetadata(image.upload),
                        };
                    });
                    const result = context.applyOps(ops);
                    void postState(endpoint, token, clientIdRef.current, result);
                }
                addEventLog(rt("importGeneratedImages"), rt(context ? "addedToSourceCanvas" : "imageGenerated"));
            }
        }
        if (activity && event.item?.id) {
            upsertEventActivity(event, activity);
            return;
        }
        if (event.type === "turn.completed") {
            const scope = eventScope(event);
            if (scope.turnId) {
                finishPlanActivity(event);
                // turn 正常结束后把该 turn 转入 live 集合保留，等待权威历史快照统一收敛。
                liveTurnKeysRef.current.add(`${scope.threadId}\0${scope.turnId}`);
            }
            const current = useAgentStore.getState();
            setAgentState({
                activeTurnId: current.activeTurnId === scope.turnId ? "" : current.activeTurnId,
                messages: current.messages.map((message) => message.threadId === scope.threadId && message.turnId === scope.turnId && message.streamId ? { ...message, streamId: undefined } : message),
            });
            if (event.status === "failed") showAgentError(event.error?.message, event, !event.replayed);
        }
        const item = formatAgentEvent(event);
        if (item) addMessage(scopeEventChatItem(event, { ...item, id: event.item?.id || createId() }, event.item?.id || createId()));
    };

    /** 助手正文的流式增量：按 itemId 定位消息，delta 追加、整段重发时用 mergeStreamText 合并。 */
    const appendStreamText = (event: AgentEventPayload, text: string, isDelta = false) => {
        if (!text) return;
        const itemId = event.item?.id;
        if (!itemId) return;
        const scoped = scopeEventChatItem(event, { id: itemId, role: "assistant", title: "Codex", text, streamId: itemId }, itemId);
        const currentMessages = useAgentStore.getState().messages;
        const index = currentMessages.findIndex((message) => message.id === scoped.id);
        if (index < 0) {
            pushMessage(scoped);
            return;
        }
        setAgentState({ messages: currentMessages.map((message, itemIndex) => itemIndex === index ? { ...message, text: isDelta ? `${message.text}${text}` : mergeStreamText(message.text, text) } : message) });
    };

    const connectionStatus = t(connectError ? "agent.status.failed" : connected ? "agent.status.connected" : enabled ? "agent.status.connecting" : "agent.status.disconnected");
    const connectionStatusColor = connectError ? "#dc2626" : connected ? "#16a34a" : enabled ? "#d97706" : theme.node.muted;
    const content = (
        <>
            <AgentPanelTabs
                value={activeTab}
                theme={theme}
                leading={
                    <div className="flex items-center gap-1">
                        <span className="grid size-8 place-items-center">
                            <Bot className="size-4" />
                        </span>
                        <div className="hidden text-base font-semibold leading-5 @min-[560px]:block">Agent</div>
                        <Tooltip title={t("agent.panel.connectionSettings", { status: connectionStatus })} placement="bottom">
                            <Button size="small" type="text" className="!h-8 !w-8 !min-w-8 !px-0 @min-[560px]:!w-auto @min-[560px]:!min-w-0 @min-[560px]:!px-[7px]" aria-label={t("agent.panel.connectionSettingsLabel", { status: connectionStatus })} icon={<PlugZap className="size-3.5" style={{ color: connectionStatusColor }} />} onClick={() => setAgentState({ activeTab: "setup" })}>
                                <span className="hidden @min-[560px]:inline">{connectionStatus}</span>
                            </Button>
                        </Tooltip>
                    </div>
                }
                items={[
                    { value: "chat", label: t("agent.panel.chat"), icon: <MessageSquare className="size-3.5" /> },
                    { value: "history", label: t("agent.panel.history"), icon: <History className="size-3.5" />, count: threads.length },
                    { value: "skills", label: t("agent.panel.skills"), icon: <Sparkles className="size-3.5" />, count: skillCount },
                    { value: "log", label: t("agent.panel.logs"), icon: <Terminal className="size-3.5" />, count: eventLogs.length },
                ]}
                onChange={(activeTab) => {
                    setAgentState({ activeTab });
                    if (activeTab === "history") void loadThreads();
                }}
                right={
                    <>
                        <Tooltip title={t("agent.history.newThread")} placement="bottom">
                            <Button size="small" type="text" className="!h-8 !w-8 !min-w-8 !px-0 @min-[560px]:!w-auto @min-[560px]:!min-w-0 @min-[560px]:!px-[7px]" aria-label={t("agent.history.newThread")} disabled={!connected || loadingThreads || sending || waiting || conversationBusy} icon={<Plus className="size-3.5" />} onClick={startNewThread}>
                                <span className="hidden @min-[560px]:inline">{t("agent.history.newThread")}</span>
                            </Button>
                        </Tooltip>
                        <Tooltip title={t("agent.panel.collapse")}>
                            <Button type="text" shape="circle" className="!h-8 !w-8 !min-w-8" aria-label={t("agent.panel.collapseLabel")} style={{ color: theme.node.muted }} icon={<PanelRightClose className="size-4" />} onClick={closePanel} />
                        </Tooltip>
                    </>
                }
            />

            {activeTab === "setup" ? (
                <AgentConnectView
                    theme={theme}
                    url={url}
                    token={token}
                    enabled={enabled}
                    connected={connected}
                    activity={activity}
                    connectError={connectError}
                    onUrlChange={(url) => setAgentState({ url, connectError: "" })}
                    onTokenChange={(token) => setAgentState({ token, connectError: "" })}
                    onToggleEnabled={toggleAgentConnection}
                />
            ) : activeTab === "skills" ? (
                <AgentSkillsView clientId={clientIdRef.current} />
            ) : activeTab === "history" ? (
                <AgentHistoryView
                    theme={theme}
                    threads={threads}
                    activeThreadId={activeThreadId}
                    workspacePath={workspacePath}
                    loading={loadingThreads}
                    busy={sending || waiting || conversationBusy}
                    connected={connected}
                    onRefresh={() => void loadThreads()}
                    onNewThread={() => void startNewThread()}
                    onResumeThread={(threadId) => void resumeThread(threadId)}
                    onDeleteThreads={confirmDeleteThreads}
                />
            ) : activeTab === "log" ? (
                <AgentLogView
                    logs={eventLogs}
                    theme={theme}
                    context={{ endpoint, connected, enabled, activity, waiting, sending, messages: messageCount, pendingTool: pendingTool?.name }}
                    onClear={clearEventLogs}
                    onCopied={(text) => message.success(text)}
                    onCopyBlocked={(text) => message.warning(text)}
                />
            ) : (
                <>
                    <AgentChatTimeline theme={theme} pendingTool={pendingTool} pendingApprovals={pendingApprovals} sending={sending} waiting={waiting} onRejectTool={rejectPendingTool} onApproveTool={approvePendingTool} onApprovalDecision={decideApproval} />
                    <AgentTaskProgress theme={theme} busy={sending || waiting} />
                    {tokenUsage ? <AgentUsageBar usage={tokenUsage} theme={theme} /> : null}
                    <AgentChatComposer
                        prompt={prompt}
                        attachments={attachments.map((attachment) => agentAttachmentToChatAttachment(attachment, endpoint, token))}
                        disabled={!connected || !conversationReady || loadingThreads}
                        sending={sending || waiting}
                        placeholder={conversation.status === "idle" || conversation.status === "preparing"
                            ? t("agent.panel.mcpInitializing")
                            : conversation.status === "failed"
                                ? t("agent.panel.initFailed")
                                : t("agent.panel.placeholder")}
                        theme={theme}
                        onPromptChange={(prompt) => setAgentState({ prompt })}
                        onSubmit={sendPrompt}
                        onStop={stopTurn}
                        onAddFiles={addAttachments}
                        onRemoveAttachment={removeAttachment}
                        confirmTools={confirmTools}
                        onConfirmToolsChange={(confirmTools) => setAgentState({ confirmTools })}
                        permissionMode={permissionMode}
                        onPermissionModeChange={changePermissionMode}
                        models={models}
                        model={model}
                        reasoningEffort={reasoningEffort}
                        onModelChange={(model) => {
                            const selected = models.find((item) => item.model === model);
                            if (!selected) return;
                            const effort = selected.defaultReasoningEffort || selected.supportedReasoningEfforts[0]?.reasoningEffort;
                            localStorage.setItem("canvas-agent-model", model);
                            if (effort) localStorage.setItem("canvas-agent-reasoning-effort", effort);
                            setAgentState({ model, ...(effort ? { reasoningEffort: effort } : {}) });
                        }}
                        onReasoningEffortChange={(reasoningEffort) => {
                            localStorage.setItem("canvas-agent-reasoning-effort", reasoningEffort);
                            setAgentState({ reasoningEffort });
                        }}
                        left={
                            attachments.length ? (
                                <span className="hidden text-[11px] @min-[660px]:inline" style={{ color: theme.node.muted }}>
                                    {formatBytes(attachmentPayloadBytes(attachments))} / 30MB
                                </span>
                            ) : null
                        }
                    />
                </>
            )}
        </>
    );

    // headless（分享链接引导页）不渲染；embedded 模式由 AgentPanel 提供外壳后直接输出内容。
    if (headless) return null;
    return embedded ? content : null;
}

/**
 * 获取跨标签页唯一的 clientId：用 Web Locks 独占 `infinite-canvas-agent:<id>` 锁，
 * 锁被占用则换新 ID 重试，保证同一时刻只有一个标签页作为「激活客户端」接收实时事件。
 * 结果缓存在 globalThis 上，页面生命周期内只竞速一次。
 */
function acquireAgentClientId() {
    const scope = globalThis as AgentClientGlobal;
    scope.__infiniteCanvasAgentClientIdPromise ||= (async () => {
        const storedClientId = readAgentClientId();
        let clientId = storedClientId || randomId();
        if (!navigator.locks) {
            if (!storedClientId) saveAgentClientId(clientId);
            return clientId;
        }
        while (true) {
            const acquired = await new Promise<boolean>((resolve, reject) => {
                void navigator.locks.request(`infinite-canvas-agent:${clientId}`, { ifAvailable: true }, async (lock) => {
                    if (!lock) return resolve(false);
                    resolve(true);
                    await new Promise<void>(() => undefined);
                }).catch(reject);
            });
            if (acquired) {
                saveAgentClientId(clientId);
                return clientId;
            }
            clientId = randomId();
        }
    })().catch(() => {
        const clientId = randomId();
        saveAgentClientId(clientId);
        return clientId;
    });
    return scope.__infiniteCanvasAgentClientIdPromise;
}

// clientId 存 sessionStorage：每个标签页独立身份；存储不可用时仅用内存身份，当前页面内的归属关系仍然一致。
function readAgentClientId() {
    try {
        return sessionStorage.getItem("canvas-agent-client-id") || "";
    } catch {
        return "";
    }
}

function saveAgentClientId(clientId: string) {
    try {
        sessionStorage.setItem("canvas-agent-client-id", clientId);
    } catch {
        // The in-memory identity still keeps request ownership consistent within the current page session.
    }
}

/** 从事件负载中取出 threadId/turnId（兼容 snake_case），作为消息归属的作用域。 */
function eventScope(event: AgentEventPayload) {
    return {
        threadId: event.threadId || event.thread_id || "",
        turnId: event.turnId || event.turn_id || "",
    };
}

/** 事件作用域 + 指定 itemId 的快捷 scope 封装，供事件分支构造规范消息。 */
function scopeEventChatItem(event: AgentEventPayload, item: AgentChatItem, itemId: string) {
    const scope = eventScope(event);
    return scopeChatItem({ ...item, itemId }, scope.threadId, scope.turnId);
}

/** 根据剩余审批与等待状态推导活动栏文案，避免审批消失后状态停留在「等待审批」。 */
function approvalActivity(pendingApprovals: AgentPendingApproval[], waiting: boolean, fallback: string) {
    if (pendingApprovals.length) return rt("awaitingApproval");
    return waiting ? rt("codexRunning") : fallback;
}

/** 把 canvas_create_attachment_nodes 的入参转成加图节点 ops：附件从 Agent 服务取回并上传到素材库。 */
async function attachmentNodeOps(endpoint: string, token: string, clientId: string, value: unknown): Promise<CanvasAgentOp[]> {
    const nodes = Array.isArray(value) ? value : [];
    if (!nodes.length) throw new Error(rt("noImageAttachments"));
    return await Promise.all(
        nodes.map(async (value) => {
            const item = value as { id?: unknown; attachmentId?: unknown; title?: unknown; position?: unknown };
            const id = String(item.id || "");
            const attachmentId = String(item.attachmentId || "");
            if (!id || !attachmentId) throw new Error(rt("invalidAttachmentNode"));
            const res = await fetch(`${endpoint}/agent/attachments/${encodeURIComponent(attachmentId)}?token=${encodeURIComponent(token)}&clientId=${encodeURIComponent(clientId)}`);
            if (!res.ok) {
                const body = (await res.json().catch(() => null)) as { error?: string } | null;
                throw new Error(body?.error || rt("attachmentReadFailed"));
            }
            const image = await uploadImage(await res.blob());
            const size = fitNodeSize(image.width, image.height);
            const position = item.position && typeof item.position === "object" ? (item.position as { x?: unknown; y?: unknown }) : {};
            return {
                type: "add_node" as const,
                id,
                nodeType: "image" as const,
                title: String(item.title || rt("referenceImage")),
                position: { x: Number(position.x) || 0, y: Number(position.y) || 0 },
                width: size.width,
                height: size.height,
                metadata: imageMetadata(image),
            };
        }),
    );
}

function createId() {
    return randomId();
}

/** 生成随消息落库的附件元数据：超大图降采样到预览尺寸，控制历史消息体积。 */
async function createMessageAttachmentMetadata(item: AgentAttachment) {
    const url = Math.max(item.width, item.height) > MESSAGE_PREVIEW_LONG_EDGE || item.dataUrl.length > MESSAGE_PREVIEW_MAX_LENGTH
        ? await upscaleDataUrl(item.dataUrl, { targetLongEdge: MESSAGE_PREVIEW_LONG_EDGE, algorithm: "high" })
        : item.dataUrl;
    return { id: item.id, name: item.name, type: item.type, size: item.size, width: item.width, height: item.height, url };
}

function clamp(value: number, min: number, max: number) {
    return Math.min(max, Math.max(min, value));
}

/** 把 Agent 生成的图片（dataUrl 或本地路径）上传素材库并返回消息附件结构。 */
async function importGeneratedImages(endpoint: string, token: string, item: AgentEventItem) {
    const sources = Array.from(generatedImageSources(item));
    return await Promise.all(
        sources.map(async (source, index) => {
            const response = source.startsWith("data:image/")
                ? await fetch(source)
                : await fetch(`${endpoint}/agent/local-image?token=${encodeURIComponent(token)}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ path: source }) });
            if (!response.ok) throw new Error(rt("generatedImageReadFailed"));
            const blob = await response.blob();
            const upload = await uploadImage(blob);
            const dataUrl = await readDataUrl(blob);
            const name = source.startsWith("/") ? source.split("/").at(-1) || rt("generatedImageName", { index: index + 1 }) : rt("generatedImageName", { index: index + 1 });
            return { upload, name, attachment: { id: createId(), name, type: blob.type || upload.mimeType, size: blob.size, width: upload.width, height: upload.height, url: upload.url, dataUrl } };
        }),
    );
}

// 递归收集事件结果里的图片引用：data URL 或根路径下的图片文件（排除含换行的误匹配）。
function generatedImageSources(value: unknown, result = new Set<string>()) {
    if (typeof value === "string") {
        if (value.startsWith("data:image/") || (/^\/.+\.(?:avif|gif|jpe?g|png|webp)$/i.test(value) && !value.includes("\n"))) result.add(value);
        return result;
    }
    if (Array.isArray(value)) value.forEach((item) => generatedImageSources(item, result));
    else if (value && typeof value === "object") Object.values(value).forEach((item) => generatedImageSources(item, result));
    return result;
}

function readDataUrl(file: Blob) {
    return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(reader.error || new Error(rt("imageReadFailed")));
        reader.readAsDataURL(file);
    });
}

function delay(ms: number) {
    return new Promise<void>((resolve) => setTimeout(resolve, ms));
}
