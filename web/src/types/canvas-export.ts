// 画布导出包的结构（与 lib/canvas/canvas-export.ts 的写出口径对应）：
// projects.json 是清单，媒体文件按 files[].path 相对路径存放在包内。
import type { CanvasDetail } from "@/services/data/types";

/** 导出包根对象：version=3 为当前格式，导入侧据此校验。 */
export type CanvasExportFile = {
    app: "infinite-canvas";
    version: 3;
    exportedAt: string;
    projects: CanvasProjectExportItem[];
};

/** 一个画布项目及其媒体文件清单。 */
export type CanvasProjectExportItem = {
    project: CanvasDetail;
    files: CanvasExportAsset[];
};

/** 包内一个媒体文件：path 为 zip 内相对路径，storageKey 用于导入时还原引用。 */
export type CanvasExportAsset = {
    storageKey: string;
    path: string;
    mimeType: string;
    bytes: number;
};
