/** 生成参考图：dataUrl 为 base64（本地预览/上传用），storageKey 指向服务端媒体（图生图传参用）。 */
export type ReferenceImage = {
    id: string;
    name: string;
    type: string;
    dataUrl: string;
    url?: string;
    storageKey?: string;
};
