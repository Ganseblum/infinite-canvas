import { nanoid } from "nanoid";

import i18n from "@/i18n";
import { getMediaBlob, mediaUrl, putMedia } from "@/services/api/media";
import { storeImagePreviewFromBlob } from "@/services/image-preview";
import { withLocalProxy } from "@/stores/use-config-store";

// 媒体写入统一入口：远端下载或本地文件先变成 Blob，再 PUT /api/media/{storageKey}，
// 返回的 url 就是可直接用于 <img>/<video> 的同源地址，浏览器负责缓存。
export type UploadedImage = {
    url: string;
    storageKey?: string;
    width: number;
    height: number;
    bytes: number;
    mimeType: string;
};

export type UploadedFile = { url: string; storageKey: string; bytes: number; mimeType: string; width?: number; height?: number; durationMs?: number };

type ReadOptions = { signal?: AbortSignal };

const IMAGE_DOWNLOAD_TIMEOUT_MS = 10 * 60_000;
const IMAGE_REMOTE_LOAD_TIMEOUT_MS = 10 * 60_000;
const IMAGE_DECODE_TIMEOUT_MS = 10_000;
const IMAGE_RESPONSE_ERROR = "ImageResponseError";
const IMAGE_TIMEOUT_ERROR = "ImageTimeoutError";

export async function uploadImage(input: string | Blob, options?: ReadOptions): Promise<UploadedImage> {
    if (typeof input !== "string") return storeImage(input, options);

    let blob: Blob;
    try {
        blob = await fetchImageBlob(input, options);
    } catch (error) {
        // 下载不到但浏览器能显示的远端图保持原地址，不写空 storageKey。
        if (options?.signal?.aborted || isNamedError(error, IMAGE_RESPONSE_ERROR) || isNamedError(error, IMAGE_TIMEOUT_ERROR) || !/^https?:\/\//i.test(input)) throw error;
        const meta = await loadImageMeta(input, options, IMAGE_REMOTE_LOAD_TIMEOUT_MS);
        if (!meta) throw error;
        return { url: input, width: meta.width, height: meta.height, bytes: 0, mimeType: "" };
    }
    return storeImage(blob, options);
}

async function storeImage(blob: Blob, options?: ReadOptions): Promise<UploadedImage> {
    const objectUrl = URL.createObjectURL(blob);
    try {
        const meta = await loadImageMeta(objectUrl, options);
        if (!meta) throw new Error(i18n.t("common.imageReadFailed"));
        throwIfAborted(options?.signal);
        const storageKey = `image:${nanoid()}`;
        await putMedia(storageKey, blob);
        throwIfAborted(options?.signal);
        // 上传时手里就有原始字节，顺手生成 768px WebP 预览（失败不影响上传主流程）。
        void storeImagePreviewFromBlob(storageKey, blob);
        return { url: mediaUrl(storageKey), storageKey, width: meta.width, height: meta.height, bytes: blob.size, mimeType: blob.type.startsWith("image/") ? blob.type : "" };
    } finally {
        URL.revokeObjectURL(objectUrl);
    }
}

export async function uploadMediaFile(input: string | Blob, prefix = "file"): Promise<UploadedFile> {
    const blob = typeof input === "string" ? await (await fetch(withLocalProxy(input))).blob() : input;
    const storageKey = `${prefix}:${nanoid()}`;
    const objectUrl = URL.createObjectURL(blob);
    try {
        const meta = blob.type.startsWith("video/") ? await readVideoMeta(objectUrl) : blob.type.startsWith("audio/") ? await readAudioMeta(objectUrl) : {};
        await putMedia(storageKey, blob);
        return { url: mediaUrl(storageKey), storageKey, bytes: blob.size, mimeType: blob.type || "application/octet-stream", ...meta };
    } finally {
        URL.revokeObjectURL(objectUrl);
    }
}

// 供仍走浏览器直连的 AI 调用层使用：把已保存的媒体读成 Data URL。
export async function imageToDataUrl(image: { url?: string; dataUrl?: string; storageKey?: string }, options?: ReadOptions) {
    if (image.dataUrl?.startsWith("data:")) return image.dataUrl;
    if (image.dataUrl) return blobToDataUrl(await fetchImageBlob(image.dataUrl, options));
    if (image.storageKey) {
        const blob = await getMediaBlob(image.storageKey, options?.signal);
        if (blob) return blobToDataUrl(blob);
    }
    const url = image.url || "";
    if (!url || url.startsWith("data:")) return url;
    return blobToDataUrl(await fetchImageBlob(url, options));
}

async function fetchImageBlob(url: string, options?: ReadOptions) {
    if (url.startsWith("data:")) return (await fetch(url)).blob();
    const controller = new AbortController();
    let timedOut = false;
    const abort = () => controller.abort();
    if (options?.signal?.aborted) abort();
    else options?.signal?.addEventListener("abort", abort, { once: true });
    const timer = window.setTimeout(() => {
        timedOut = true;
        controller.abort();
    }, IMAGE_DOWNLOAD_TIMEOUT_MS);
    try {
        const response = await fetch(withLocalProxy(url), { signal: controller.signal, credentials: "include" });
        if (!response.ok) throw namedError(IMAGE_RESPONSE_ERROR);
        return await response.blob();
    } catch (error) {
        if (timedOut) throw namedError(IMAGE_TIMEOUT_ERROR);
        if (options?.signal?.aborted) throw abortReason(options.signal);
        throw error;
    } finally {
        window.clearTimeout(timer);
        options?.signal?.removeEventListener("abort", abort);
    }
}

function loadImageMeta(url: string, options?: ReadOptions, timeoutMs = IMAGE_DECODE_TIMEOUT_MS) {
    return new Promise<{ width: number; height: number } | null>((resolve, reject) => {
        if (options?.signal?.aborted) return reject(abortReason(options.signal));
        const image = new Image();
        let settled = false;
        const finish = (value: { width: number; height: number } | null) => {
            if (settled) return;
            settled = true;
            window.clearTimeout(timer);
            options?.signal?.removeEventListener("abort", abort);
            image.onload = null;
            image.onerror = null;
            resolve(value);
        };
        const abort = () => {
            if (settled) return;
            settled = true;
            window.clearTimeout(timer);
            image.onload = null;
            image.onerror = null;
            reject(abortReason(options!.signal!));
        };
        const timer = window.setTimeout(() => finish(null), timeoutMs);
        options?.signal?.addEventListener("abort", abort, { once: true });
        image.onload = () => finish(image.naturalWidth && image.naturalHeight ? { width: image.naturalWidth, height: image.naturalHeight } : null);
        image.onerror = () => finish(null);
        image.src = url;
    });
}

function namedError(name: string) {
    const error = new Error(i18n.t("common.imageReadFailed"));
    error.name = name;
    return error;
}

function isNamedError(error: unknown, name: string) {
    return error instanceof Error && error.name === name;
}

function abortReason(signal: AbortSignal) {
    return signal.reason instanceof Error ? signal.reason : new DOMException("Aborted", "AbortError");
}

function throwIfAborted(signal?: AbortSignal) {
    if (signal?.aborted) throw abortReason(signal);
}

function blobToDataUrl(blob: Blob) {
    return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(new Error(i18n.t("common.imageReadFailed")));
        reader.readAsDataURL(blob);
    });
}

function readVideoMeta(url: string) {
    return new Promise<{ width: number; height: number; durationMs?: number }>((resolve) => {
        const video = document.createElement("video");
        const done = () => resolve({ width: video.videoWidth || 1280, height: video.videoHeight || 720, durationMs: Number.isFinite(video.duration) ? Math.round(video.duration * 1000) : undefined });
        video.onloadedmetadata = done;
        video.onerror = done;
        video.src = url;
    });
}

function readAudioMeta(url: string) {
    return new Promise<{ durationMs?: number }>((resolve) => {
        const audio = document.createElement("audio");
        const done = () => resolve({ durationMs: Number.isFinite(audio.duration) ? Math.round(audio.duration * 1000) : undefined });
        audio.onloadedmetadata = done;
        audio.onerror = done;
        audio.src = url;
    });
}
