import { API_BASE_URL, apiRequest, refreshSession } from "@/services/api/client";
import { useAuthStore } from "@/stores/use-auth-store";
import { getSessionId } from "@/lib/session-id";
import type { ModelParameterValue } from "@/services/api/catalog";

// 全部生成行为的唯一调用方。浏览器不持有任何上游地址与密钥。
export type AiCapability = "image" | "video" | "text" | "audio";

export type AiDiscount = {
    promotionId: string;
    name: string;
    discountBps: number;
    endsAt?: string;
};

export type AiCredits = {
    baseCostMicros: number;
    finalCostMicros: number;
    finalCostYuan: string;
    discount?: AiDiscount | null;
    remainingMicros?: number;
    refundedMicros?: number;
};

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

export function getVideoTask(taskId: string, signal?: AbortSignal) {
    return apiRequest<VideoTaskResponse>(`/ai/videos/tasks/${taskId}`, { signal });
}

export type ChatMessageInput = {
    role: string;
    content: unknown;
    name?: string;
    toolCallId?: string;
};

// streamChat 直接消费 SSE；鉴权与刷新复用 client 的单飞逻辑，不重写第二份。
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
    const response = await fetch(`${API_BASE_URL}/api${path}`, {
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
