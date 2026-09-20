// office 工作台状态：状态形状对齐《前端方案》§4；handleEvent 实现 §5.2 事件表。
// SSE 连接生命周期由本 store 的重连循环驱动（D1 fetch-stream：EventSource 无法携带 Bearer 头），
// 连接对象不进 store 状态；事件入口集中在 handleEvent，终态后以服务端快照为准。
import { create } from "zustand";

import { getApiErrorMessage } from "@/lib/api-error";
import { EventsExpiredError, SseAuthError } from "@/lib/office/sse";
import type { ArtifactItem, MessageItem, OfficeEvent, SessionSummary, ToolCallItem } from "@/services/api/office";
import { cancelOfficeRun, createOfficeSession, getOfficeMessages, getOfficeSession, listOfficeArtifacts, listOfficeSessions, openOfficeStream, postOfficeMessage } from "@/services/api/office";

type StreamStatus = "idle" | "queued" | "running" | "succeeded" | "failed" | "cancelled";

type OfficeState = {
    sessions: SessionSummary[];
    sessionsLoading: boolean;
    currentSessionId: string | null;
    messages: MessageItem[];
    messagesLoading: boolean;
    // 流式域（当前 run）
    streamStatus: StreamStatus;
    streamRunId: string | null;
    streamSeq: number; // 最近收到的事件 seq：重连时作为 ?lastSeq= 续流（spec §2.3）
    connected: boolean; // SSE 连接状态：false 时对话区显示重连 banner
    bufferText: string;
    displayText: string;
    tools: ToolCallItem[];
    startedAt: number | null;
    errorCode: string | null;
    lastUsage: { inputTokens: number; outputTokens: number; credits: number } | null;
    sendError: string | null; // 发送失败提示（409 run_conflict 等），下一次发送前清除
    // 产物域
    artifacts: ArtifactItem[];
    activeArtifactId: string | null;
    drawerOpen: boolean;
    // 输入域
    draft: string;

    loadSessions: () => Promise<void>;
    openSession: (id: string) => Promise<void>;
    newSession: () => Promise<string | null>;
    setDraft: (draft: string) => void;
    send: (content: string) => void;
    cancelRun: () => void;
    handleEvent: (event: OfficeEvent) => void;
    flushBuffer: () => void;
    openArtifact: (id: string) => void;
    toggleDrawer: (open?: boolean) => void;
    abortStream: () => void;
};

// SSE 连接与重连循环的运行时句柄（切换会话/组件卸载时 abort；run 在服务端继续，回来走快照+重放）。
let streamAbort: AbortController | null = null;

// 重连退避 1s/2s/4s/8s…上限 30s（前端方案 §5.2）。
const RECONNECT_BACKOFF_MS = 1000;
const RECONNECT_BACKOFF_MAX_MS = 30000;

/**
 * office 工作台 store：管会话列表、消息、流式运行与产物抽屉，office 页面读写。
 * 事件入口集中在 handleEvent，终态后以服务端快照为准（D3 整体替换）。
 */
export const useOfficeStore = create<OfficeState>((set, get) => ({
    sessions: [],
    sessionsLoading: true,
    currentSessionId: null,
    messages: [],
    messagesLoading: false,
    streamStatus: "idle",
    streamRunId: null,
    streamSeq: 0,
    connected: true,
    bufferText: "",
    displayText: "",
    tools: [],
    startedAt: null,
    errorCode: null,
    lastUsage: null,
    sendError: null,
    artifacts: [],
    activeArtifactId: null,
    drawerOpen: false,
    draft: "",

    loadSessions: async () => {
        try {
            set({ sessions: await listOfficeSessions(), sessionsLoading: false });
        } catch {
            set({ sessionsLoading: false });
        }
    },

    openSession: async (id) => {
        // 切换会话：断开当前连接（run 在服务端继续）；连接句柄先于快照请求建立，卸载 abort 同样生效。
        get().abortStream();
        const controller = new AbortController();
        streamAbort = controller;
        set({
            currentSessionId: id,
            messagesLoading: true,
            messages: [],
            artifacts: [],
            activeArtifactId: null,
            streamStatus: "idle",
            streamRunId: null,
            streamSeq: 0,
            bufferText: "",
            displayText: "",
            tools: [],
            errorCode: null,
            sendError: null,
            connected: true,
        });
        let session: SessionSummary;
        let messages: MessageItem[];
        let artifacts: ArtifactItem[];
        try {
            [session, messages, artifacts] = await Promise.all([getOfficeSession(id), getOfficeMessages(id), listOfficeArtifacts(id)]);
        } catch {
            if (!controller.signal.aborted) set({ messagesLoading: false });
            return;
        }
        if (controller.signal.aborted || get().currentSessionId !== id) return; // 已卸载/又切换了会话
        set({ messages, artifacts, messagesLoading: false });
        // 会话有活动 run：订阅重放续流（快照只含已物化 turn，进行中内容由事件重放重建）。
        if (session.activeRunId) {
            set({ streamStatus: "queued", streamRunId: session.activeRunId, startedAt: Date.now() });
            void runStreamLoop(session.activeRunId, controller.signal);
        }
    },

    newSession: async () => {
        try {
            const session = await createOfficeSession();
            await get().loadSessions();
            set({ currentSessionId: session.id, messages: [], artifacts: [], streamStatus: "idle", displayText: "", tools: [] });
            return session.id;
        } catch {
            return null;
        }
    },

    setDraft: (draft) => set({ draft }),

    send: (content) => {
        const sessionId = get().currentSessionId;
        if (!sessionId || get().streamStatus === "queued" || get().streamStatus === "running") return;
        const clientMsgId = newClientMsgId();
        const tmpId = `tmp_${clientMsgId}`;
        // 连接句柄先于 POST 建立：卸载/切会话的 abort 对在途请求同样生效。
        streamAbort?.abort();
        const controller = new AbortController();
        streamAbort = controller;
        // 乐观入列：user 消息立即展示，终态后由快照整体替换（D3）；runId 待 201 回填。
        set((state) => ({
            messages: [...state.messages, { id: tmpId, sessionId, runId: "", threadId: sessionId, turnId: "", role: "user", text: content, createdAt: Date.now() }],
            draft: "",
            sendError: null,
            streamStatus: "queued",
            streamRunId: null,
            streamSeq: 0,
            bufferText: "",
            displayText: "",
            tools: [],
            errorCode: null,
            lastUsage: null,
            startedAt: Date.now(),
        }));
        void postOfficeMessage(sessionId, content, clientMsgId)
            .then((result) => {
                if (controller.signal.aborted || useOfficeStore.getState().currentSessionId !== sessionId) return; // 已卸载/切换会话
                set({ streamRunId: result.runId });
                void runStreamLoop(result.runId, controller.signal);
            })
            .catch((error: unknown) => {
                // 409 run_conflict / 400 credits_exhausted / 429：撤回乐观消息，输入保留，回 idle。
                if (controller.signal.aborted || useOfficeStore.getState().currentSessionId !== sessionId) return;
                const state = useOfficeStore.getState();
                useOfficeStore.setState({
                    messages: state.messages.filter((m) => m.id !== tmpId),
                    draft: state.draft || content,
                    sendError: getApiErrorMessage(error),
                    streamStatus: "idle",
                    startedAt: null,
                });
            });
    },

    cancelRun: () => {
        const runId = get().streamRunId;
        if (!runId) return;
        // A7 幂等取消：cancelled 终态事件经 SSE 到达后由事件表收尾；请求失败不中断流（服务端超时兜底）。
        void cancelOfficeRun(runId).catch(() => undefined);
    },

    // §5.2 事件表：每类事件到状态的映射集中在此（实现规格见《前端方案》）。
    handleEvent: (event) => {
        const state = get();
        if (event.runId !== state.streamRunId) return;
        switch (event.type) {
            case "run_started":
                set({ streamStatus: "running" });
                break;
            case "delta":
                set({ bufferText: state.bufferText + event.payload.text });
                break;
            case "tool_call":
                set({ tools: [...state.tools, { toolCallId: event.payload.toolCallId, name: event.payload.name, inputPreview: event.payload.inputPreview, status: "running" }] });
                break;
            case "tool_result":
                set({
                    tools: state.tools.map((t) => (t.toolCallId === event.payload.toolCallId ? { ...t, status: event.payload.status === "error" ? "error" : "ok", outputPreview: event.payload.outputPreview } : t)),
                });
                break;
            case "artifact":
                // 事件只带元数据（spec §2.2）：M1 无正文，其余字段以中性值占位，终态快照整体替换。
                set((s) => ({
                    artifacts: [
                        ...s.artifacts,
                        {
                            id: event.payload.artifactId,
                            sessionId: s.currentSessionId ?? "",
                            runId: event.runId,
                            kind: event.payload.kind,
                            name: event.payload.name,
                            mime: "",
                            size: event.payload.size,
                            storageKey: "",
                            sourceView: event.payload.kind === "html",
                            createdAt: Date.now(),
                        },
                    ],
                    drawerOpen: true,
                }));
                break;
            case "notice":
                break;
            case "done":
                set({ lastUsage: event.payload, streamStatus: "succeeded" });
                void refreshSnapshot(state.currentSessionId);
                break;
            case "error":
                set({ errorCode: event.payload.code, streamStatus: event.payload.code === "cancelled" ? "cancelled" : "failed" });
                // 终态对齐快照：失败/取消的已生成部分同样由服务端物化（spec §5.2「同步拉快照对齐」）。
                void refreshSnapshot(state.currentSessionId);
                break;
            default:
                // 未知 type 静默忽略（spec §2.2 向前兼容规则）。
                break;
        }
    },

    flushBuffer: () => {
        const { bufferText, displayText } = get();
        if (!bufferText) return;
        set({ displayText: displayText + bufferText, bufferText: "" });
    },

    openArtifact: (id) => set({ activeArtifactId: id, drawerOpen: true }),
    toggleDrawer: (open) => set((s) => ({ drawerOpen: open ?? !s.drawerOpen })),
    abortStream: () => {
        streamAbort?.abort();
        streamAbort = null;
    },
}));

// 消费当前 run 的 SSE 流并驱动重连循环：流断开按指数退避重连（lastSeq 续流），
// 410 转快照重建并停止，401 刷新重试仍失败（登录态已被清空）即停止，终态/换会话/卸载即止。
async function runStreamLoop(runId: string, signal: AbortSignal) {
    let attempt = 0;
    for (;;) {
        const sessionId = useOfficeStore.getState().currentSessionId;
        if (!sessionId) return;
        try {
            await openOfficeStream(
                sessionId,
                runId,
                useOfficeStore.getState().streamSeq,
                {
                    onOpen: () => {
                        attempt = 0;
                        useOfficeStore.setState({ connected: true });
                    },
                    onEvent: (event) => {
                        useOfficeStore.setState({ streamSeq: event.seq });
                        useOfficeStore.getState().handleEvent(event);
                    },
                },
                signal,
            );
        } catch (error) {
            if (signal.aborted) return;
            if (error instanceof EventsExpiredError) {
                // 410（spec §2.3 E11）：事件已清理，转快照重建并停止重连（§8「无感重建」）。
                await refreshSnapshot(useOfficeStore.getState().currentSessionId);
                useOfficeStore.setState({ connected: true, streamStatus: "idle", streamRunId: null, startedAt: null });
                return;
            }
            if (error instanceof SseAuthError) {
                // 刷新后重试仍 401：登录态已由 clearSession 清空（RequireAuth 接管跳转），停止重连。
                useOfficeStore.setState({ connected: false });
                return;
            }
            // 网络/网关错误等：进入退避重连。
        }
        if (signal.aborted) return;
        const status = useOfficeStore.getState().streamStatus;
        if (status === "succeeded" || status === "failed" || status === "cancelled") return; // 终态事件已收，流正常收尾
        useOfficeStore.setState({ connected: false });
        await new Promise((resolve) => setTimeout(resolve, Math.min(RECONNECT_BACKOFF_MS * 2 ** attempt, RECONNECT_BACKOFF_MAX_MS)));
        attempt += 1;
        if (signal.aborted || useOfficeStore.getState().streamRunId !== runId) return; // 已切换会话/run
    }
}

// 快照权威（D3）：整体替换，不做逐条合并；流式 buffer 同时清空。
async function refreshSnapshot(sessionId: string | null) {
    if (!sessionId) return;
    try {
        const [messages, artifacts] = await Promise.all([getOfficeMessages(sessionId), listOfficeArtifacts(sessionId)]);
        if (useOfficeStore.getState().currentSessionId === sessionId) useOfficeStore.setState({ messages, artifacts, bufferText: "", displayText: "", tools: [] });
    } catch {
        // 快照拉取失败保持现有视图；下一轮重连/终态事件会再次对齐。
    }
}

// clientMsgId（uuid v4，spec §1.1 幂等键）：crypto.randomUUID 仅在安全上下文暴露，退化为时间戳+随机数。
function newClientMsgId(): string {
    return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `idem-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
