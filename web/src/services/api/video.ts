import i18n from "@/i18n";
import { ApiError } from "@/lib/api-error";
import { VIDEO_SECONDS_MAX, VIDEO_SECONDS_MIN } from "@/lib/media-size";
import { createVideoTask, getVideoTask, quoteGeneration, type VideoTaskStatus } from "@/services/api/ai";
import { mediaUrl } from "@/services/api/media";
import { uploadMediaFile } from "@/services/media-ingest";
import type { AiConfig } from "@/stores/use-config-store";
import type { ReferenceImage } from "@/types/image";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";

type RequestOptions = { signal?: AbortSignal };
type VideoMediaOptions = RequestOptions & { videos?: ReferenceVideo[]; audios?: ReferenceAudio[] };

const apiText = (key: string, options?: Record<string, unknown>) => i18n.t(`apiErrors.${key}`, options);

// 视频生成的高层封装：创建任务是异步的（POST /ai/videos/generations 后轮询任务状态），
// 常用入口是 requestVideoGeneration（创建 + 轮询到终态）；页面可拆开两步自绘进度。

export type VideoGenerationResult = { url: string; storageKey: string; bytes: number; mimeType: string; width?: number; height?: number; durationMs?: number; generationId?: string };
// 任务携带 generationId：创建响应下发一次，轮询视图每次都带，用于结果卡点赞点踩。
export type VideoGenerationTask = { id: string; model: string; generationId?: string };

export type VideoGenerationTaskState = { status: "pending"; pollAfterMs?: number } | { status: "completed"; result: VideoGenerationResult } | { status: "failed"; error: string };

/** 创建任务并轮询到终态，返回结果或抛 VideoTaskFailed。 */
export async function requestVideoGeneration(config: AiConfig, prompt: string, references: ReferenceImage[] = [], options?: VideoMediaOptions): Promise<VideoGenerationResult> {
    return waitForVideoGenerationTask(config, await createVideoGenerationTask(config, prompt, references, options), options);
}

export async function waitForVideoGenerationTask(config: AiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationResult> {
    for (;;) {
        if (options?.signal?.aborted) throw new DOMException("Aborted", "AbortError");
        const state = await pollVideoGenerationTask(config, task, options);
        if (state.status === "completed") return state.result;
        if (state.status === "failed") throw videoTaskFailed(state.error);
        await delay(Math.max(1000, state.pollAfterMs || 5000), options?.signal);
    }
}

export function isVideoTaskFailed(error: unknown) {
    return error instanceof Error && error.name === "VideoTaskFailed";
}

function videoTaskFailed(message: string) {
    const error = new Error(message);
    error.name = "VideoTaskFailed";
    return error;
}

/** 创建视频生成任务：先报价再创建，QUOTE_STALE 时静默重报一次重试创建。 */
export async function createVideoGenerationTask(config: AiConfig, prompt: string, references: ReferenceImage[] = [], options?: VideoMediaOptions): Promise<VideoGenerationTask> {
    const model = (config.model || "").trim();
    if (!model) throw new Error(apiText("modelNotSupported"));
    const params = videoRequestParams(config);
    const referencesKeys = await toStorageKeys(references, "image");
    const videoKeys = await toStorageKeys(options?.videos || [], "video");
    const audioKeys = await toStorageKeys(options?.audios || [], "audio");
    // mode 不参与计价，只用于上游请求，不能进报价参数：服务端校验报价时重建的参数集是
    // ratio/resolution/duration，多带 mode 会让参数哈希对不上并返回 409 QUOTE_STALE。
    const { mode, ...quoteParams } = params;
    // 报价与创建接口读取同一组参数，报价过期时静默重报一次；再次过期由页面提示用户重新生成。
    let quote = await quoteGeneration({ model, capability: "video", params: quoteParams });
    let response;
    try {
        response = await createVideoTask({
            model,
            prompt,
            ...params,
            ...(referencesKeys.length ? { references: referencesKeys } : {}),
            ...(videoKeys.length ? { videoReferences: videoKeys } : {}),
            ...(audioKeys.length ? { audioReferences: audioKeys } : {}),
            quoteToken: quote.quoteToken,
            signal: options?.signal,
        });
    } catch (error) {
        if (!(error instanceof ApiError) || error.code !== "QUOTE_STALE") throw error;
        quote = await quoteGeneration({ model, capability: "video", params });
        response = await createVideoTask({
            model,
            prompt,
            ...params,
            ...(referencesKeys.length ? { references: referencesKeys } : {}),
            ...(videoKeys.length ? { videoReferences: videoKeys } : {}),
            ...(audioKeys.length ? { audioReferences: audioKeys } : {}),
            quoteToken: quote.quoteToken,
            signal: options?.signal,
        });
    }
    return { id: response.taskId, model, generationId: response.generationId };
}

/** 轮询一次任务状态：succeeded/failed 终态直接返回，pending 带下次建议轮询间隔。 */
export async function pollVideoGenerationTask(config: AiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    void config;
    const response = await getVideoTask(task.id, options?.signal);
    return toTaskState(response.status, response.video, response.error, response.pollAfterMs, task.generationId);
}

function toTaskState(status: VideoTaskStatus, video: { storageKey: string; bytes: number; mimeType: string } | undefined, error: { code: string; message: string } | undefined, pollAfterMs: number, generationId?: string): VideoGenerationTaskState {
    if (status === "succeeded") {
        if (!video?.storageKey) throw videoTaskFailed(apiText("noPlayableVideo"));
        return { status: "completed", result: { url: mediaUrl(video.storageKey), storageKey: video.storageKey, bytes: video.bytes, mimeType: video.mimeType, generationId } };
    }
    if (status === "failed") return { status: "failed", error: error?.message || apiText("videoGenerationFailed") };
    return { status: "pending", pollAfterMs };
}

function videoRequestParams(config: AiConfig) {
    const ratio = config.size.trim();
    const resolution = config.vquality.trim();
    const duration = normalizeDuration(config.videoSeconds);
    return {
        duration,
        // 模式必须传给服务端：不传会退化成 reference，与界面上选的「首尾帧」不符。
        mode: config.videoMode === "reference" ? "reference" : "frames",
        ...(ratio && ratio !== "auto" && ratio.includes(":") ? { ratio } : {}),
        ...(resolution && resolution !== "auto" ? { resolution: /p$/i.test(resolution) ? resolution : `${resolution}p` } : {}),
    };
}

function normalizeDuration(value: string) {
    const duration = Number(value);
    if (!Number.isFinite(duration) || duration <= 0) return 6;
    return Math.max(VIDEO_SECONDS_MIN, Math.min(VIDEO_SECONDS_MAX, Math.floor(duration)));
}

async function toStorageKeys(items: Array<{ storageKey?: string; url?: string; dataUrl?: string }>, prefix: string) {
    const keys: string[] = [];
    for (const item of items) {
        if (item.storageKey) {
            keys.push(item.storageKey);
            continue;
        }
        const source = item.dataUrl || item.url || "";
        if (!source) throw new Error(apiText(prefix === "video" ? "invalidReferenceVideo" : prefix === "audio" ? "invalidReferenceAudio" : "referenceImageReadFailed"));
        const uploaded = await uploadMediaFile(source, prefix);
        if (!uploaded.storageKey) throw new Error(apiText("localAssetReadFailed"));
        keys.push(uploaded.storageKey);
    }
    return keys;
}

function delay(ms: number, signal?: AbortSignal) {
    return new Promise<void>((resolve, reject) => {
        if (signal?.aborted) {
            reject(new DOMException("Aborted", "AbortError"));
            return;
        }
        const timer = setTimeout(resolve, ms);
        signal?.addEventListener(
            "abort",
            () => {
                clearTimeout(timer);
                reject(new DOMException("Aborted", "AbortError"));
            },
            { once: true },
        );
    });
}
