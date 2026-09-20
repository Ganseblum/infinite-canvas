// office 域 API（契约一 /api/v1/office，spec §1/§2）：普通请求走共享 client（自动前缀/Bearer/401 刷新重放），
// SSE 走 lib/office/sse.ts 自行 fetch（D1：EventSource 无法携带 Bearer 头），401 复用 client 的单飞刷新重试一次。
import { SseAuthError, openSse, type SseFrame } from "@/lib/office/sse";
import { API_BASE_URL, apiRequest, refreshSession } from "@/services/api/client";
import { useAuthStore } from "@/stores/use-auth-store";

// ---- SSE 事件（spec §2.1 信封 + §2.2 payload，冻结；预留 type 本期不发送，未知 type 忽略） ----

export type OfficeEvent =
    | { v: 1; seq: number; type: "run_started"; runId: string; ts: number; payload: { model: string; agentName: string } }
    | { v: 1; seq: number; type: "delta"; runId: string; ts: number; payload: { text: string } }
    | { v: 1; seq: number; type: "tool_call"; runId: string; ts: number; payload: { toolCallId: string; name: string; inputPreview?: string; status: "running" } }
    | { v: 1; seq: number; type: "tool_result"; runId: string; ts: number; payload: { toolCallId: string; status: "ok" | "error"; isError: boolean; outputPreview?: string } }
    | { v: 1; seq: number; type: "artifact"; runId: string; ts: number; payload: { artifactId: string; kind: "markdown" | "code" | "html" | "file"; name: string; size: number } }
    | { v: 1; seq: number; type: "usage"; runId: string; ts: number; payload: { inputTokens: number; outputTokens: number } }
    | { v: 1; seq: number; type: "notice"; runId: string; ts: number; payload: { level: "info" | "warn"; text: string } }
    | { v: 1; seq: number; type: "done"; runId: string; ts: number; payload: { inputTokens: number; outputTokens: number; credits: number } }
    | { v: 1; seq: number; type: "error"; runId: string; ts: number; payload: { code: string; message?: string; retryable: boolean } }
    | { v: 1; seq: number; type: "quota_exhausted"; runId: string; ts: number; payload: { usedCredits: number } };

// ---- 数据类型（spec §1 端点响应 / §4 数据字典；时间统一转毫秒数供展示层使用） ----

export type SessionSummary = {
    id: string;
    title: string;
    agentId: string;
    activeRunId: string; // 空串表示无活动 run
    workspacePath: string;
    status: string;
    createdAt: number;
    updatedAt: number;
};

export type MessageItem = {
    id: string;
    sessionId: string;
    runId: string;
    threadId: string;
    turnId: string;
    role: "user" | "assistant";
    text: string; // content 存储 JSON {schema_version, text} 的 text 展开（spec §4）
    createdAt: number;
};

export type ArtifactItem = {
    id: string;
    sessionId: string;
    runId: string;
    kind: "markdown" | "code" | "html" | "file";
    name: string;
    mime: string;
    size: number;
    storageKey: string;
    sourceView: boolean;
    createdAt: number;
    text?: string; // M1 元数据接口不带正文（在工作区），抽屉以空值显示占位
};

export type ToolCallItem = { toolCallId: string; name: string; inputPreview?: string; outputPreview?: string; status: "running" | "ok" | "error" };

export type PostMessageResult = { runId: string; sessionId: string; status: string; clientMsgId: string };
export type CancelRunResult = { runId: string; sessionId: string; status: string; errorCode?: string };

// ---- 后端线格式（server/internal/office/handler.go 的响应 DTO） ----

type SessionResp = Omit<SessionSummary, "createdAt" | "updatedAt"> & { createdAt: string; updatedAt: string };
type MessageResp = { id: string; sessionId: string; runId: string; role: string; content: { text?: string }; threadId: string; turnId: string; createdAt: string };
type ArtifactResp = Omit<ArtifactItem, "createdAt" | "text"> & { createdAt: string };

function toMillis(value: string): number {
    const ms = Date.parse(value);
    return Number.isNaN(ms) ? 0 : ms;
}

const toSession = (s: SessionResp): SessionSummary => ({ ...s, createdAt: toMillis(s.createdAt), updatedAt: toMillis(s.updatedAt) });

const toMessage = (m: MessageResp): MessageItem => ({
    id: m.id,
    sessionId: m.sessionId,
    runId: m.runId,
    threadId: m.threadId,
    turnId: m.turnId,
    role: m.role === "assistant" ? "assistant" : "user",
    text: m.content?.text ?? "",
    createdAt: toMillis(m.createdAt),
});

const ARTIFACT_KINDS = ["markdown", "code", "html", "file"];

const toArtifact = (a: ArtifactResp): ArtifactItem => ({
    ...a,
    kind: ARTIFACT_KINDS.includes(a.kind) ? a.kind : "file",
    createdAt: toMillis(a.createdAt),
});

// ---- 端点（前端方案 §7 函数清单） ----

/** A1 创建会话（agentId 缺省用默认智能体）。 */
export function createOfficeSession(agentId?: string): Promise<SessionSummary> {
    return apiRequest<SessionResp>("/office/sessions", { method: "POST", body: { agentId: agentId ?? "" } }).then(toSession);
}

/** A2 会话列表（updated_at 倒序，?cursor= 分页）。 */
export async function listOfficeSessions(cursor?: string): Promise<SessionSummary[]> {
    const page = await apiRequest<{ items: SessionResp[] }>("/office/sessions", { query: cursor ? { cursor } : undefined });
    return page.items.map(toSession);
}

/** A3 会话详情（含 activeRunId，回连流用）。 */
export function getOfficeSession(id: string): Promise<SessionSummary> {
    return apiRequest<SessionResp>(`/office/sessions/${encodeURIComponent(id)}`).then(toSession);
}

/** A4 消息快照（权威；?afterTurnId= 增量）。 */
export async function getOfficeMessages(sessionId: string, afterTurnId?: string): Promise<MessageItem[]> {
    const page = await apiRequest<{ items: MessageResp[] }>(`/office/sessions/${encodeURIComponent(sessionId)}/messages`, {
        query: afterTurnId ? { afterTurnId } : undefined,
    });
    return page.items.map(toMessage);
}

/** A5 发消息起 run：201 新建 / 200 幂等重放（同 clientMsgId）/ 409 run_conflict / 400 credits_exhausted。 */
export function postOfficeMessage(sessionId: string, content: string, clientMsgId: string): Promise<PostMessageResult> {
    return apiRequest<PostMessageResult>(`/office/sessions/${encodeURIComponent(sessionId)}/messages`, {
        method: "POST",
        body: { content, clientMsgId },
    });
}

/** A7 取消（幂等；终态事件经 SSE 到达后由事件表收尾）。 */
export function cancelOfficeRun(runId: string): Promise<CancelRunResult> {
    return apiRequest<CancelRunResult>(`/office/runs/${encodeURIComponent(runId)}/cancel`, { method: "POST", body: {} });
}

/** A8 会话产物列表。 */
export async function listOfficeArtifacts(sessionId: string): Promise<ArtifactItem[]> {
    const page = await apiRequest<{ items: ArtifactResp[] }>(`/office/sessions/${encodeURIComponent(sessionId)}/artifacts`);
    return page.items.map(toArtifact);
}

/** A9 产物详情（kind=html 返回源码视图标记）。 */
export function getOfficeArtifact(id: string): Promise<ArtifactItem> {
    return apiRequest<ArtifactResp>(`/office/artifacts/${encodeURIComponent(id)}`).then(toArtifact);
}

// ---- A6 SSE 订阅 ----

export type OfficeStreamHandlers = {
    onOpen?: () => void;
    onEvent: (event: OfficeEvent) => void;
};

/**
 * 订阅 run 的 SSE 事件流并消费到流结束（服务端终态收尾或连接断开）。
 * lastSeq 由调用方维护：服务端重放 seq > lastSeq（spec §2.3）；401 用 client 单飞刷新后重试一次；
 * 410 抛 EventsExpiredError 由调用方快照重建；流中断（网络等）由调用方退避重连。
 */
export async function openOfficeStream(sessionId: string, runId: string, lastSeq: number, handlers: OfficeStreamHandlers, signal?: AbortSignal): Promise<void> {
    const frames = await openStream(`/office/sessions/${encodeURIComponent(sessionId)}/runs/${encodeURIComponent(runId)}/stream`, lastSeq, signal, true);
    handlers.onOpen?.();
    for await (const frame of frames) {
        const event = parseOfficeEvent(frame);
        if (event) handlers.onEvent(event);
    }
}

async function openStream(path: string, lastSeq: number, signal: AbortSignal | undefined, canRetry: boolean): Promise<AsyncIterable<SseFrame>> {
    const token = useAuthStore.getState().accessToken;
    try {
        return await openSse(`${API_BASE_URL}/api/v1${path}?lastSeq=${lastSeq}`, { token: token ?? undefined, signal });
    } catch (error) {
        if (error instanceof SseAuthError && canRetry) {
            const session = await refreshSession();
            if (session) return openStream(path, lastSeq, signal, false);
        }
        throw error;
    }
}

// 信封校验与可选字段补全（spec §2.1/§2.2）：坏帧与未知 type 静默忽略（向前兼容规则）。
// done.credits / error.retryable 后端可省略，补默认值保证类型完备（渲染层 toFinite/布尔分支不炸）。
function parseOfficeEvent(frame: SseFrame): OfficeEvent | null {
    let event: OfficeEvent;
    try {
        event = JSON.parse(frame.data) as OfficeEvent;
    } catch {
        return null;
    }
    if (event === null || typeof event !== "object") return null;
    if (event.v !== 1 || typeof event.seq !== "number" || typeof event.runId !== "string" || event.payload === null || typeof event.payload !== "object") return null;
    if (event.type === "delta" && typeof event.payload.text !== "string") return null;
    if (event.type === "done" && typeof event.payload.credits !== "number") return { ...event, payload: { ...event.payload, credits: 0 } };
    if (event.type === "error" && typeof event.payload.retryable !== "boolean") return { ...event, payload: { ...event.payload, retryable: false } };
    return event;
}
