import i18n from "@/i18n";
import { ApiError } from "@/lib/api-error";
import { normalizeAudioFormatValue, normalizeAudioSpeedValue, normalizeAudioVoiceValue } from "@/lib/audio-generation";
import { generateSpeech, quoteGeneration, type GenerateSpeechResponse, type QuoteResult } from "@/services/api/ai";
import { mediaUrl } from "@/services/api/media";
import type { AiConfig } from "@/stores/use-config-store";

type RequestOptions = { signal?: AbortSignal };

export type GeneratedAudio = { url: string; storageKey: string; bytes: number; mimeType: string };

/**
 * 语音合成：POST /api/v1/ai/audio/speech，返回服务端落盘的音频地址与元信息。
 * 报价过期（QUOTE_STALE）会静默重新报价一次，再次过期交给统一错误处理提示用户。
 */
export async function requestAudioGeneration(config: AiConfig, prompt: string, options?: RequestOptions): Promise<GeneratedAudio> {
    const model = (config.model || "").trim();
    if (!model) throw new Error(i18n.t("apiErrors.modelNotSupported"));
    const voice = normalizeAudioVoiceValue(config.audioVoice);
    const format = normalizeAudioFormatValue(config.audioFormat);
    const speed = Number(normalizeAudioSpeedValue(config.audioSpeed));
    const instructions = config.audioInstructions.trim();
    const quote = () => quoteGeneration({ model, capability: "audio", params: {} });
    const response = await withQuoteRetry(quote, (quoteToken) =>
        generateSpeech({
            model,
            input: prompt,
            voice,
            format,
            speed,
            ...(instructions ? { instructions } : {}),
            quoteToken,
            signal: options?.signal,
        }),
    );
    return toGeneratedAudio(response);
}

function toGeneratedAudio(response: GenerateSpeechResponse): GeneratedAudio {
    const { audio } = response;
    return { url: mediaUrl(audio.storageKey), storageKey: audio.storageKey, bytes: audio.bytes, mimeType: audio.mimeType };
}

// 报价过期时静默重新报价一次；再次过期由页面提示用户重新生成（见 useAiGenerationError）。
async function withQuoteRetry<T>(quote: () => Promise<QuoteResult>, run: (quoteToken: string) => Promise<T>): Promise<T> {
    const first = await quote();
    try {
        return await run(first.quoteToken);
    } catch (error) {
        if (!(error instanceof ApiError) || error.code !== "QUOTE_STALE") throw error;
        const next = await quote();
        return await run(next.quoteToken);
    }
}
