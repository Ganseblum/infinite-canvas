/** 视频参考素材：bytes/durationMs 在读取元信息后补齐。 */
export type ReferenceVideo = {
    id: string;
    name: string;
    type: string;
    url: string;
    storageKey?: string;
    bytes?: number;
    width?: number;
    height?: number;
    durationMs?: number;
};

/** 音频参考素材（视频生成/语音克隆类输入）。 */
export type ReferenceAudio = {
    id: string;
    name: string;
    type: string;
    url: string;
    storageKey?: string;
    durationMs?: number;
};
