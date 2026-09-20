import { saveAs } from "file-saver";

import { createZip, readZip } from "@/lib/zip";
import { getMediaBlob, putMedia } from "@/services/api/media";
import type { AssetItem, AssetKind } from "@/services/data/types";

/** 素材导出包的清单文件（assets.json）结构：元信息 + 素材记录 + 媒体文件索引。 */
type AssetExportFile = {
    app: "infinite-canvas";
    version: 1;
    exportedAt: string;
    assets: AssetItem[];
    files: AssetExportItem[];
};

/** 清单里的一条媒体文件记录：storageKey 与 zip 内路径的对应关系。 */
type AssetExportItem = {
    storageKey: string;
    path: string;
    mimeType: string;
    bytes: number;
};

/**
 * 把选中的素材打包成 zip 导出下载：非文本素材逐个拉取 blob 存进包内，
 * 清单 assets.json 记录原始 storageKey 与包内路径，便于导入时还原。
 * 拉取失败的单个素材直接跳过，不中断整体导出。
 * @param assets 要导出的素材记录列表
 * @param filename 保存到本地的 zip 文件名
 */
export async function exportAssets(assets: AssetItem[], filename: string) {
    const files: AssetExportItem[] = [];
    const zipFiles: { name: string; data: BlobPart }[] = [];

    await Promise.all(
        assets.map(async (asset) => {
            if (asset.kind === "text" || !asset.storageKey) return;
            const blob = await getMediaBlob(asset.storageKey).catch(() => null);
            if (!blob) return;
            const path = `files/${safeFileName(asset.storageKey)}.${fileExtension(blob.type, asset.kind)}`;
            files.push({ storageKey: asset.storageKey, path, mimeType: blob.type || "application/octet-stream", bytes: blob.size });
            zipFiles.push({ name: path, data: blob });
        }),
    );

    const data: AssetExportFile = { app: "infinite-canvas", version: 1, exportedAt: new Date().toISOString(), assets, files };
    const zip = await createZip([{ name: "assets.json", data: JSON.stringify(data, null, 2) }, ...zipFiles]);
    saveAs(zip, filename);
}

/**
 * 解析导入的素材包 zip：读取清单并按记录把媒体重新 PUT 到服务端。
 * @param file 用户选择的 zip 文件
 * @returns 可直接提交给 createAsset 的素材载荷列表
 */
// 导入返回可直接提交给 createAsset 的载荷：媒体先 PUT 到服务端，再落素材记录。
export async function readAssetPackage(file: File) {
    const zip = await readZip(file);
    const assetFile = zip.get("assets.json");
    if (!assetFile) throw new Error("missing assets.json");
    const data = JSON.parse(await assetFile.text()) as AssetExportFile;
    await Promise.all(
        data.files.map(async (item) => {
            const blob = zip.get(item.path);
            if (!blob) return;
            const typedBlob = blob.type ? blob : blob.slice(0, blob.size, item.mimeType);
            await putMedia(item.storageKey, typedBlob);
        }),
    );
    return data.assets.map((asset) => ({ kind: asset.kind, title: asset.title, tags: asset.tags, storageKey: asset.storageKey, bytes: asset.bytes, data: asset.data }));
}

// 把 storageKey 里的非法路径字符替换成下划线，保证 zip 内文件名安全。
function safeFileName(value: string) {
    return value.replace(/[\\/:*?"<>|]/g, "_");
}

// 按 MIME 类型推断 zip 内文件扩展名；无法识别时图片兜底 png、其余兜底 bin。
function fileExtension(mimeType: string, kind: AssetKind) {
    if (mimeType.includes("png")) return "png";
    if (mimeType.includes("jpeg")) return "jpg";
    if (mimeType.includes("webp")) return "webp";
    if (mimeType.includes("gif")) return "gif";
    if (mimeType.includes("mp4")) return "mp4";
    if (mimeType.includes("webm")) return "webm";
    return kind === "image" ? "png" : "bin";
}
