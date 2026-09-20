// fetch-stream SSE 解析器（前端方案 §5.1 / 决策 D1）：平台鉴权是 Authorization: Bearer 注入头，
// 原生 EventSource 无法携带自定义请求头，因此用 fetch + ReadableStream 手写解析。
// 本文件只做 SSE 协议层（建连错误分派 + 帧边界/字段解析），不感知 office 业务 JSON 结构。
import { ApiError } from "@/lib/api-error";

/** 单个 SSE 事件帧：id 行承载 seq（spec §2.3 重连载体），data 行承载事件 JSON（可多行）。 */
export type SseFrame = { id: string; event: string; data: string };

/** 建连返回 401：可识别错误，调用方用 client 同款单飞刷新后重试一次。 */
export class SseAuthError extends Error {
    constructor() {
        super("SSE 鉴权失败");
        this.name = "SseAuthError";
    }
}

/** 建连返回 410（spec §2.3 E11 重放空洞）：调用方转消息快照重建并停止重连。 */
export class EventsExpiredError extends ApiError {
    constructor() {
        super({ code: "events_expired", status: 410, message: "事件流已过期，请从消息快照重建视图" });
        this.name = "EventsExpiredError";
    }
}

export type SseOpenOptions = { token?: string; signal?: AbortSignal };

/**
 * 建立 SSE 连接并返回事件帧异步迭代器：流结束（服务端终态收尾或连接断开）即迭代完成。
 * 建连失败按状态码分派：401 → SseAuthError；410 → EventsExpiredError；其余 → ApiError。
 */
export async function openSse(url: string, options: SseOpenOptions = {}): Promise<AsyncIterable<SseFrame>> {
    const headers: Record<string, string> = { Accept: "text/event-stream" };
    if (options.token) headers.Authorization = `Bearer ${options.token}`;
    let response: Response;
    try {
        response = await fetch(url, { headers, credentials: "include", signal: options.signal });
    } catch (error) {
        if (options.signal?.aborted) throw error;
        throw new ApiError({ code: "NETWORK_ERROR", status: 0 });
    }
    if (!response.ok) {
        const { code, message } = await readErrorBody(response);
        if (response.status === 401) throw new SseAuthError();
        if (response.status === 410) throw new EventsExpiredError();
        throw new ApiError({ code: code || `HTTP_${response.status}`, message, status: response.status });
    }
    if (!response.body) throw new ApiError({ code: "SSE_NO_BODY", status: 0 });
    return parseSseStream(response.body);
}

// 错误体兼容平台统一 {error:{code,message}} 与 spec §1.1 顶层 {code,message} 两种形状。
async function readErrorBody(response: Response): Promise<{ code: string; message: string }> {
    try {
        const payload = (await response.json()) as { code?: string; message?: string; error?: { code?: string; message?: string } };
        const err = payload.error ?? payload;
        return { code: typeof err.code === "string" ? err.code : "", message: typeof err.message === "string" ? err.message : "" };
    } catch {
        return { code: "", message: "" };
    }
}

// 帧缓冲按行解析：跨 chunk 半包/粘包以「缓冲到换行为止」承接，UTF-8 多字节断开由 TextDecoder(stream) 承接。
// 空行才派发帧：EOF 时未被空行终结的残帧按 SSE 规范丢弃（服务端帧恒以 \n\n 结尾，正常流不受影响）。
async function* parseSseStream(body: ReadableStream<Uint8Array>): AsyncGenerator<SseFrame> {
    const decoder = new TextDecoder();
    const reader = body.getReader();
    const state = { data: [] as string[], event: "", id: "" };
    let buffer = "";
    try {
        for (;;) {
            const { done, value } = await reader.read();
            if (done) return;
            buffer += decoder.decode(value, { stream: true });
            let index = buffer.indexOf("\n");
            while (index >= 0) {
                const frame = feedLine(state, buffer.slice(0, index));
                buffer = buffer.slice(index + 1);
                if (frame) yield frame;
                index = buffer.indexOf("\n");
            }
        }
    } finally {
        reader.releaseLock();
    }
}

// 单行喂入：空行派发帧（有 data 才派发，多行 data 以 \n 拼接为单事件 JSON）；`:` 开头为心跳注释；未知字段忽略。
function feedLine(state: { data: string[]; event: string; id: string }, rawLine: string): SseFrame | null {
    if (rawLine === "") {
        if (state.data.length === 0) return null;
        const frame = { id: state.id, event: state.event, data: state.data.join("\n") };
        state.data = [];
        state.event = "";
        return frame;
    }
    if (rawLine.startsWith(":")) return null;
    const colon = rawLine.indexOf(":");
    const field = colon === -1 ? rawLine : rawLine.slice(0, colon);
    const value = colon === -1 ? "" : rawLine.slice(colon + 1).replace(/^ /, "");
    if (field === "data") state.data.push(value);
    else if (field === "id") state.id = value;
    else if (field === "event") state.event = value;
    return null;
}
