import localforage from "localforage";

import { createImageThumbnail } from "@/lib/image-thumbnail";
import { getMediaBlob } from "@/services/api/media";

// 画布图片预览：按原图 storageKey 另存一份 768px WebP，只放在本地 IndexedDB，
// 不写进节点数据、不参与导出。原图字节来自服务端媒体接口（Bearer + ic_media cookie），
// 上传链路手里已有原始字节时直接经 storeImagePreviewFromBlob 写入，不再回源拉取。
const previewStore = localforage.createInstance({ name: "infinite-canvas", storeName: "image_previews" });

type StoredImagePreview = { version: number; blob?: Blob };

const IMAGE_PREVIEW_VERSION = 1;

const previewUrls = new Map<string, string>();
const previewListeners = new Set<() => void>();
let previewRevision = 0;
let previewQueue: Promise<unknown> = Promise.resolve();

/** 取某张画布图片的本地预览 Blob URL；未生成过返回 undefined。 */
export function previewUrlFor(storageKey?: string) {
    return storageKey ? previewUrls.get(storageKey) : undefined;
}

// 预览在后台补，生成完成后再让用到它的界面重渲染一次（配合 useSyncExternalStore）。
export function subscribeImagePreviews(listener: () => void) {
    previewListeners.add(listener);
    return () => {
        previewListeners.delete(listener);
    };
}

export function getImagePreviewRevision() {
    return previewRevision;
}

/** 检查内存/本地缓存里的预览，没有则排队回源生成；立即返回值可能为 undefined，生成后经订阅通知。 */
export async function ensureImagePreview(storageKey?: string) {
    if (!storageKey) return undefined;
    const cached = previewUrls.get(storageKey);
    if (cached) return cached;
    const stored = await previewStore.getItem<StoredImagePreview>(storageKey).catch(() => null);
    if (stored?.version === IMAGE_PREVIEW_VERSION) return stored.blob ? cacheImagePreview(storageKey, stored.blob) : undefined;
    queueImagePreview(storageKey);
    return undefined;
}

// 预览生成排成一队，避免一次打开大量图片时并发回源与解码。
function queueImagePreview(storageKey: string) {
    previewQueue = previewQueue
        .then(async () => {
            const original = await getMediaBlob(storageKey).catch(() => null);
            if (original) await storeImagePreview(storageKey, original);
        })
        .catch(() => undefined);
}

export async function storeImagePreviewFromBlob(storageKey: string, original: Blob) {
    if (!storageKey) return;
    await storeImagePreview(storageKey, original).catch(() => undefined);
}

async function storeImagePreview(storageKey: string, original: Blob) {
    const preview = await createImageThumbnail(original).catch(() => undefined);
    await previewStore.setItem<StoredImagePreview>(storageKey, { version: IMAGE_PREVIEW_VERSION, blob: preview }).catch(() => undefined);
    return preview ? cacheImagePreview(storageKey, preview) : undefined;
}

function cacheImagePreview(storageKey: string, preview: Blob) {
    const url = URL.createObjectURL(preview);
    previewUrls.set(storageKey, url);
    previewRevision += 1;
    previewListeners.forEach((listener) => listener());
    return url;
}

// 素材/原图删除后同步清理对应预览，避免缓存里留孤儿。
export async function deleteImagePreview(storageKey?: string) {
    if (!storageKey) return;
    const url = previewUrls.get(storageKey);
    if (url) URL.revokeObjectURL(url);
    previewUrls.delete(storageKey);
    await previewStore.removeItem(storageKey).catch(() => undefined);
}
