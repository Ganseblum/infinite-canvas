import { unzipSync, zipSync } from "fflate";

type ZipFile = {
    name: string;
    data: BlobPart;
};

/** 把文件打包成 zip Blob。压缩级别取 0（仅存储）：内容多为已压缩的图片/视频，再压无收益。 */
export async function createZip(files: ZipFile[]) {
    const entries = await Promise.all(
        files.map(async (file) => {
            const data = new Uint8Array(await new Blob([file.data]).arrayBuffer());
            return [file.name, data] as const;
        }),
    );
    return new Blob([zipSync(Object.fromEntries(entries), { level: 0 })], { type: "application/zip" });
}

/** 解开 zip，返回「文件名 → Blob」映射，供导入画布包/插件包读取。 */
export async function readZip(file: Blob) {
    const entries = unzipSync(new Uint8Array(await file.arrayBuffer()));
    return new Map(Object.entries(entries).map(([name, data]) => [name, new Blob([data])]));
}
