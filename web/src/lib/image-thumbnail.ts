// 画布图片预览的统一长边上限（px）：预览图按此缩放生成。
export const CANVAS_IMAGE_PREVIEW_MAX_EDGE = 768;

type ImageSourceChoice = {
    previewUrl?: string;
    originalUrl: string;
    naturalWidth?: number;
    naturalHeight?: number;
    renderedWidth: number;
    renderedHeight: number;
    scale: number;
    devicePixelRatio?: number;
    previewMaxEdge?: number;
};

// 小图预览让平移保持轻快，但节点放大后会发虚；
// 只有屏显尺寸需要的像素真正超过预览上限时才回退原图。
export function pickImageSource({ previewUrl, originalUrl, naturalWidth, naturalHeight, renderedWidth, renderedHeight, scale, devicePixelRatio = globalThis.devicePixelRatio || 1, previewMaxEdge = CANVAS_IMAGE_PREVIEW_MAX_EDGE }: ImageSourceChoice) {
    if (!previewUrl) return originalUrl;
    const naturalLongEdge = Math.max(naturalWidth || 0, naturalHeight || 0);
    const previewLongEdge = naturalLongEdge > 0 ? Math.min(previewMaxEdge, naturalLongEdge) : previewMaxEdge;
    const requiredLongEdge = Math.max(renderedWidth, renderedHeight) * scale * devicePixelRatio;
    return requiredLongEdge > previewLongEdge ? originalUrl : previewUrl;
}

export function getThumbnailDimensions(width: number, height: number, maxEdge = CANVAS_IMAGE_PREVIEW_MAX_EDGE) {
    const scale = Math.min(1, maxEdge / Math.max(width, height));
    return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

/**
 * 生成不超过 maxEdge 的等比缩略图，返回 WebP Blob。
 * 图片本身小于上限时不生成，返回 undefined 表示直接用原图。
 */
export async function createImageThumbnail(blob: Blob, maxEdge = CANVAS_IMAGE_PREVIEW_MAX_EDGE) {
    const bitmap = await createImageBitmap(blob);
    const { width, height } = getThumbnailDimensions(bitmap.width, bitmap.height, maxEdge);
    if (width === bitmap.width && height === bitmap.height) {
        bitmap.close();
        return undefined;
    }

    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) {
        bitmap.close();
        return undefined;
    }
    context.drawImage(bitmap, 0, 0, width, height);
    bitmap.close();
    return new Promise<Blob | undefined>((resolve) => canvas.toBlob((result) => resolve(result || undefined), "image/webp", 0.86));
}
