import i18n from "@/i18n";
import { ApiError } from "@/lib/api-error";
import { buildImageReferencePromptText } from "@/lib/image-reference-prompt";
import { ChatStreamError, generateImages, quoteGeneration, streamChat, type ChatMessageInput, type QuoteResult } from "@/services/api/ai";
import type { ModelParameterValue } from "@/services/api/catalog";
import { mediaUrl } from "@/services/api/media";
import { uploadMediaFile } from "@/services/media-ingest";
import { modelConstraints } from "@/stores/use-model-catalog-store";
import type { AiConfig } from "@/stores/use-config-store";
import type { ReferenceImage } from "@/types/image";

type RequestOptions = { signal?: AbortSignal };

export type AiTextMessage = {
    role: "system" | "user" | "assistant";
    content: string | Array<{ type: "text"; text: string } | { type: "image_url"; image_url: { url: string } }>;
};

export type GeneratedImage = {
    id: string;
    url: string;
    dataUrl: string;
    storageKey: string;
    width: number;
    height: number;
    bytes: number;
    mimeType: string;
    // 所属生成记录 id：生成结果点赞点踩定位用（工作台结果卡）。
    generationId?: string;
};

const apiText = (key: string, options?: Record<string, unknown>) => i18n.t(`apiErrors.${key}`, options);

export function withSystemPrompt(config: AiConfig, prompt: string) {
    const systemPrompt = config.systemPrompt.trim();
    return systemPrompt ? `${systemPrompt}\n\n${prompt}` : prompt;
}

export function withSystemMessage<T extends { role: string; content: unknown }>(config: AiConfig, messages: T[]): T[] {
    const systemPrompt = config.systemPrompt.trim();
    return systemPrompt ? [{ role: "system", content: systemPrompt } as T, ...messages] : messages;
}

function resolveModel(config: AiConfig) {
    const model = (config.model || "").trim();
    if (!model) throw new Error(apiText("modelNotSupported"));
    return model;
}

function clampCount(config: AiConfig, fallback = 1) {
    const model = (config.model || "").trim();
    const max = modelConstraints(model)?.n?.max ?? 15;
    return Math.max(1, Math.min(max, Math.floor(Math.abs(Number(config.count)) || fallback)));
}

/** 报价参数必须与生成接口实际读取的字段完全一致，否则服务端会判定报价失效。 */
function imageQuoteParams(config: AiConfig, n: number): Record<string, ModelParameterValue> {
    const size = config.size.trim();
    const quality = config.quality.trim();
    return {
        n,
        ...(size && size !== "auto" ? { size } : {}),
        ...(quality && quality !== "auto" ? { quality } : {}),
    };
}

/** 参考图统一收敛成服务端可读的 storageKey；本地临时图先上传。 */
async function toReferenceKeys(references: ReferenceImage[]) {
    const keys: string[] = [];
    for (const reference of references) {
        if (reference.storageKey) {
            keys.push(reference.storageKey);
            continue;
        }
        const source = reference.dataUrl || reference.url || "";
        if (!source) throw new Error(apiText("referenceImageReadFailed"));
        const uploaded = await uploadMediaFile(source, "image");
        if (!uploaded.storageKey) throw new Error(apiText("referenceImageReadFailed"));
        keys.push(uploaded.storageKey);
    }
    return keys;
}

// 报价过期时静默重新报价一次；再次过期由页面提示用户重新生成（见 useAiGenerationError）。
async function runWithQuoteRetry<T>(quote: () => Promise<QuoteResult>, run: (quoteToken: string) => Promise<T>): Promise<T> {
    const first = await quote();
    try {
        return await run(first.quoteToken);
    } catch (error) {
        if (!(error instanceof ApiError) || error.code !== "QUOTE_STALE") throw error;
        const next = await quote();
        return await run(next.quoteToken);
    }
}

export async function requestGeneration(config: AiConfig, prompt: string, options?: RequestOptions): Promise<GeneratedImage[]> {
    const model = resolveModel(config);
    const n = clampCount(config);
    const quote = () => quoteGeneration({ model, capability: "image", params: imageQuoteParams(config, n) });
    const size = config.size.trim();
    const quality = config.quality.trim();
    const background = config.background.trim();
    const response = await runWithQuoteRetry(quote, (quoteToken) =>
        generateImages({
            model,
            prompt: withSystemPrompt(config, prompt),
            n,
            ...(size && size !== "auto" ? { size } : {}),
            ...(quality && quality !== "auto" ? { quality } : {}),
            ...(background ? { background } : {}),
            quoteToken,
            signal: options?.signal,
        }),
    );
    return response.images.map((image) => ({ ...toGeneratedImage(image), generationId: response.generationId }));
}

export async function requestEdit(config: AiConfig, prompt: string, references: ReferenceImage[], options?: RequestOptions): Promise<GeneratedImage[]> {
    const model = resolveModel(config);
    const n = clampCount(config);
    const referenceKeys = await toReferenceKeys(references);
    const requestPrompt = buildImageReferencePromptText(prompt, references);
    const quote = () => quoteGeneration({ model, capability: "image", params: imageQuoteParams(config, n) });
    const size = config.size.trim();
    const quality = config.quality.trim();
    const background = config.background.trim();
    const response = await runWithQuoteRetry(quote, (quoteToken) =>
        generateImages({
            model,
            prompt: withSystemPrompt(config, requestPrompt),
            n,
            ...(referenceKeys.length ? { references: referenceKeys } : {}),
            ...(size && size !== "auto" ? { size } : {}),
            ...(quality && quality !== "auto" ? { quality } : {}),
            ...(background ? { background } : {}),
            quoteToken,
            signal: options?.signal,
        }),
    );
    return response.images.map((image) => ({ ...toGeneratedImage(image), generationId: response.generationId }));
}

export async function requestImageQuestion(config: AiConfig, messages: AiTextMessage[], onDelta: (text: string) => void, options?: RequestOptions) {
    const model = resolveModel(config);
    const input = withSystemMessage(config, messages) as AiTextMessage[];
    const quote = () => quoteGeneration({ model, capability: "text", params: {} });
    let answer = "";
    await runWithQuoteRetry(quote, async (quoteToken) => {
        try {
            await streamChat(
                {
                    model,
                    messages: input.map((message): ChatMessageInput => ({ role: message.role, content: message.content })),
                    ...(config.reasoningEffort === "auto" ? {} : { reasoningEffort: config.reasoningEffort }),
                    quoteToken,
                },
                {
                    onDelta: (delta) => {
                        answer += delta;
                        onDelta(answer);
                    },
                },
                options?.signal,
            );
        } catch (error) {
            if (error instanceof ChatStreamError) throw new ApiError({ code: error.code, message: error.message, status: 200 });
            throw error;
        }
    });
    const text = answer.trim() || apiText("noContent");
    if (answer.trim() === "") onDelta(text);
    return text;
}

function toGeneratedImage(image: { storageKey: string; width?: number; height?: number; bytes: number; mimeType: string }): GeneratedImage {
    const url = mediaUrl(image.storageKey);
    return {
        id: image.storageKey,
        url,
        dataUrl: url,
        storageKey: image.storageKey,
        width: image.width || 0,
        height: image.height || 0,
        bytes: image.bytes,
        mimeType: image.mimeType,
    };
}
