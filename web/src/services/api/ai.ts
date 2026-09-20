import { API_BASE_URL, apiRequest, refreshSession } from "@/services/api/client";
import { useAuthStore } from "@/stores/use-auth-store";
import { getSessionId } from "@/lib/session-id";
import type { ModelParameterValue } from "@/services/api/catalog";

// AI 生成底层接口客户端（/api/v1/ai/*）：报价、图像、语音、视频任务与文本流式对话。
// 全部生成行为的唯一底层调用方。浏览器不持有任何上游地址与密钥。
// 生成类请求统一附加 sessionId 与幂等键；上层封装见 image.ts / video.ts / audio.ts。

/** 能力域：图像 / 视频 / 文本 / 语音。 */
export type AiCapability = "image" | "video" | "text" | "audio";

/** 折扣信息：discountBps 为基点（8500 = 8.5 折）。 */
export type AiDiscount = {
    promotionId: string;
    name: string;
    discountBps: number;
    endsAt?: string;
};

/** 本次生成的计费结果：原价/折后价（微元）、折扣与退款信息。 */
export type AiCredits = {
    baseCostMicros: number;
    finalCostMicros: number;
    finalCostYuan: string;
    discount?: AiDiscount | null;
    remainingMicros?: number;
    refundedMicros?: number;
};

/**
 * 一次报价结果：finalCostPoints 为展示点数，affordable=false 时 shortfallMicros 为缺口；
 * quoteToken 必须随生成请求回传，expiresAt 前有效，参数集变化会判定失效（QUOTE_STALE）。
 */
export type QuoteResult = {
    billingMode: "credits" | "free_trial";
    baseCostMicros: number;
    finalCostMicros: number;
    finalCostPoints: number;
    finalCostYuan: string;
    availableMicros: number;
    affordable: boolean;
    shortfallMicros: number;
    priceVersion: number;
    discount?: AiDiscount | null;
    freeTrialsLeft?: number;
    quoteToken: string;
    expiresAt: string;
};

export type GeneratedImageResult = {
    storageKey: string;
    width?: number;
    height?: number;
    bytes: number;
    mimeType: string;
};

export type GenerateImagesResponse = {
    model: string;
    credits: AiCredits;
    images: GeneratedImageResult[];
    generationId: string;
    durationMs: number;
};

export type GenerateSpeechResponse = {
    model: string;
    credits: AiCredits;
    audio: { storageKey: string; bytes: number; mimeType: string };
    durationMs: number;
};

export type VideoTaskStatus = "pending" | "succeeded" | "failed";

export type CreateVideoTaskResponse = {
    taskId: string;
    status: VideoTaskStatus;
    credits: AiCredits;
    generationId: string;
    pollAfterMs: number;
};

export type VideoTaskResponse = {
    taskId: string;
    status: VideoTaskStatus;
    credits: AiCredits;
    generationId: string;
    pollAfterMs: number;
    video?: { storageKey: string; bytes: number; mimeType: string };
    error?: { code: string; message: string };
};

export type ChatToolCall = { id: string; name: string; arguments: string };

export type ChatStreamHandlers = {
    onDelta?: (delta: string) => void;
    onToolCall?: (call: ChatToolCall) => void;
    onDone?: (payload: { credits: AiCredits; finishReason: string }) => void;
};

function newIdempotencyKey() {
    // 花钱的接口必须有幂等键；这里统一生成，调用方不感知。
    return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `idem-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function quoteGeneration(input: { model: string; capability: AiCapability; params: Record<string, ModelParameterValue> }) {
    return apiRequest<QuoteResult>("/ai/quote", { method: "POST", body: input });
}

/** 先报价后提交生成。POST /api/v1/ai/images/generations。 */
export function generateImages(input: {
    model: string;
    prompt: string;
    n?: number;
    size?: string;
    quality?: string;
    background?: string;
    references?: string[];
    mask?: string;
    quoteToken: string;
    signal?: AbortSignal;
}) {
    const { signal, ...body } = input;
    return apiRequest<GenerateImagesResponse>("/ai/images/generations", {
        method: "POST",
        body: { ...body, sessionId: getSessionId(), idempotencyKey: newIdempotencyKey() },
        signal,
    });
}

/** 语音合成。POST /api/v1/ai/audio/speech。 */
export function generateSpeech(input: {
    model: string;
    input: string;
    voice?: string;
    format?: string;
    speed?: number;
    instructions?: string;
    quoteToken: string;
    signal?: AbortSignal;
}) {
    const { signal, ...body } = input;
    return apiRequest<GenerateSpeechResponse>("/ai/audio/speech", {
        method: "POST",
        body: { ...body, sessionId: getSessionId(), idempotencyKey: newIdempotencyKey() },
        signal,
    });
}

/** 创建视频生成任务（异步），凭返回的 taskId 轮询。POST /api/v1/ai/videos/generations。 */
export function createVideoTask(input: {
    model: string;
    prompt: string;
    duration?: number;
    ratio?: string;
    resolution?: string;
    generateAudio?: boolean;
    watermark?: boolean;
    mode?: string;
    references?: string[];
    videoReferences?: string[];
    audioReferences?: string[];
    quoteToken: string;
    signal?: AbortSignal;
}) {
    const { signal, ...body } = input;
    return apiRequest<CreateVideoTaskResponse>("/ai/videos/generations", {
        method: "POST",
        body: { ...body, sessionId: getSessionId(), idempotencyKey: newIdempotencyKey() },
        signal,
    });
}

/** 查询视频任务状态，succeeded 时携带 video 元信息。GET /api/v1/ai/videos/tasks/{taskId}。 */
export function getVideoTask(taskId: string, signal?: AbortSignal) {
    return apiRequest<VideoTaskResponse>(`/ai/videos/tasks/${taskId}`, { signal });
}

export type ChatMessageInput = {
    role: string;
    content: unknown;
    name?: string;
    toolCallId?: string;
};

/**
 * 文本流式对话：POST /api/v1/ai/chat/completions（SSE）。
 * 直接消费 SSE；鉴权与过期刷新复用 client 的单飞逻辑，不重写第二份。
 * 事件按空行分块（event:/data: 行），delta → onDelta、tool_call → onToolCall、
 * done → onDone（含计费）；error 事件抛 ChatStreamError。
 */
export async function streamChat(
    input: {
        model: string;
        messages: ChatMessageInput[];
        reasoningEffort?: string;
        tools?: unknown[];
        toolChoice?: unknown;
        quoteToken: string;
    },
    handlers: ChatStreamHandlers = {},
    signal?: AbortSignal,
): Promise<void> {
    const body = { ...input, stream: true, sessionId: getSessionId(), idempotencyKey: newIdempotencyKey() };
    const response = await authedFetch("/ai/chat/completions", body, signal, true);
    if (!response.ok) {
        // SSE 建立前的错误走普通 HTTP 状态码与统一错误结构。
        await throwFromResponse(response);
    }
    if (!response.body) {
        const payload = (await response.json()) as { credits: AiCredits; content: string; finishReason: string };
        if (payload.content) handlers.onDelta?.(payload.content);
        handlers.onDone?.({ credits: payload.credits, finishReason: payload.finishReason });
        return;
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        buffer = consumeBlocks(buffer, handlers);
    }
    buffer += decoder.decode();
    consumeBlocks(buffer, handlers, true);
}

function consumeBlocks(buffer: string, handlers: ChatStreamHandlers, flush = false): string {
    let rest = buffer;
    for (;;) {
        const match = rest.match(/\r?\n\r?\n/);
        if (!match) break;
        const index = match.index ?? 0;
        dispatchBlock(rest.slice(0, index), handlers);
        rest = rest.slice(index + match[0].length);
    }
    if (flush && rest.trim()) {
        dispatchBlock(rest, handlers);
        rest = "";
    }
    return rest;
}

function dispatchBlock(block: string, handlers: ChatStreamHandlers) {
    let event = "message";
    const dataLines: string[] = [];
    for (const line of block.split(/\r?\n/)) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) dataLines.push(line.slice(5).replace(/^ /, ""));
    }
    if (dataLines.length === 0) return;
    let payload: Record<string, unknown> = {};
    try {
        payload = JSON.parse(dataLines.join("\n")) as Record<string, unknown>;
    } catch {
        return;
    }
    if (event === "delta" && typeof payload.text === "string") handlers.onDelta?.(payload.text);
    else if (event === "tool_call" && typeof payload.name === "string") {
        handlers.onToolCall?.({ id: String(payload.id ?? ""), name: payload.name, arguments: String(payload.arguments ?? "{}") });
    } else if (event === "done") {
        handlers.onDone?.({
            credits: payload.credits as AiCredits,
            finishReason: typeof payload.finishReason === "string" ? payload.finishReason : "stop",
        });
    } else if (event === "error") {
        const code = typeof payload.code === "string" ? payload.code : "UPSTREAM_ERROR";
        const message = typeof payload.message === "string" ? payload.message : "上游服务返回异常";
        throw new ChatStreamError(code, message);
    }
}

export class ChatStreamError extends Error {
    readonly code: string;

    constructor(code: string, message: string) {
        super(message);
        this.name = "ChatStreamError";
        this.code = code;
    }
}

async function authedFetch(path: string, body: unknown, signal: AbortSignal | undefined, canRetry: boolean): Promise<Response> {
    const headers = new Headers({ "Content-Type": "application/json", Accept: "text/event-stream" });
    const accessToken = useAuthStore.getState().accessToken;
    if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
    const response = await fetch(`${API_BASE_URL}/api/v1${path}`, {
        method: "POST",
        headers,
        body: JSON.stringify(body),
        credentials: "include",
        signal,
    });
    if (response.status === 401 && canRetry) {
        // 复用 client 的单飞刷新，成功后重放一次。
        const session = await refreshSession();
        if (session) return authedFetch(path, body, signal, false);
    }
    return response;
}

async function throwFromResponse(response: Response): Promise<never> {
    let code = `HTTP_${response.status}`;
    let message = "请求失败";
    try {
        const payload = (await response.json()) as { error?: { code?: string; message?: string } };
        if (payload?.error?.code) code = payload.error.code;
        if (payload?.error?.message) message = payload.error.message;
    } catch {
        // 响应体不是 JSON 时保留状态码文案。
    }
    const { ApiError } = await import("@/lib/api-error");
    throw new ApiError({ code, message, status: response.status });
}
