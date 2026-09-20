import { mediaUrl } from "@/services/api/media";
import type { AssetItem } from "@/services/data/types";

// 素材字段取值工具：data 是服务端保留的自由键值对象，这里统一兜底空值，
// 列表卡片、Agent 工具与导出都从这些取值函数读，避免直接戳 data 的内部键名。

function dataString(asset: AssetItem, key: string) {
    const value = asset.data?.[key];
    return typeof value === "string" ? value : "";
}

function dataNumber(asset: AssetItem, key: string) {
    return Number(asset.data?.[key]) || 0;
}

// 媒体地址：有 storageKey 就是服务端媒体地址，否则退回 data 里的远端 URL。
export function assetUrl(asset: AssetItem) {
    if (asset.storageKey) return mediaUrl(asset.storageKey);
    return asset.kind === "image" ? dataString(asset, "coverUrl") || dataString(asset, "url") : dataString(asset, "url");
}

// 列表卡片封面：图片/视频用自己的媒体地址，文本用 data.coverUrl。
export function assetCoverUrl(asset: AssetItem) {
    if (asset.kind === "text") return dataString(asset, "coverUrl");
    return assetUrl(asset);
}

export function assetText(asset: AssetItem) {
    return dataString(asset, "content");
}

export function assetSource(asset: AssetItem) {
    return dataString(asset, "source");
}

export function assetNote(asset: AssetItem) {
    return dataString(asset, "note");
}

export function assetWidth(asset: AssetItem) {
    return dataNumber(asset, "width");
}

export function assetHeight(asset: AssetItem) {
    return dataNumber(asset, "height");
}

export function assetMimeType(asset: AssetItem) {
    return dataString(asset, "mimeType");
}
