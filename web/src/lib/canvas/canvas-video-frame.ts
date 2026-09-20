/** 视频抽帧：离屏 video 元素按 position 定位（首帧/末帧/当前时间），绘到 canvas 导出 PNG。 */
export type VideoFramePosition = "first" | "last" | "current";

/**
 * @param source 视频地址（需允许跨域绘制，否则 canvas 会被污染导出失败）。
 * @param currentTime position 为 current 时的取帧时间，超长自动钳到片尾。
 */
export async function captureVideoFrame(source: string, position: VideoFramePosition, currentTime: number) {
    const video = document.createElement("video");
    video.crossOrigin = "anonymous";
    video.muted = true;
    video.playsInline = true;
    video.preload = "auto";
    try {
        const metadataLoaded = waitForVideo(video, "loadedmetadata");
        video.src = source;
        video.load();

        await metadataLoaded;
        // 末帧取 duration - 0.001：部分编码的最后一帧恰好等于 duration，直接 seek 会失败。
        const endTime = Math.max(0, video.duration - 0.001);
        const time = position === "first" ? 0 : position === "last" ? endTime : Math.min(currentTime, endTime);
        if (time) {
            const seeked = waitForVideo(video, "seeked");
            video.currentTime = time;
            await seeked;
        } else if (video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA) {
            await waitForVideo(video, "loadeddata");
        }

        const canvas = document.createElement("canvas");
        canvas.width = video.videoWidth;
        canvas.height = video.videoHeight;
        canvas.getContext("2d")!.drawImage(video, 0, 0);
        return await new Promise<Blob>((resolve, reject) => canvas.toBlob((result) => (result ? resolve(result) : reject(new Error("Failed to capture video frame"))), "image/png"));
    } finally {
        video.removeAttribute("src");
        video.load();
    }
}

function waitForVideo(video: HTMLVideoElement, eventName: "loadedmetadata" | "loadeddata" | "seeked") {
    return new Promise<void>((resolve, reject) => {
        const finish = () => {
            video.removeEventListener(eventName, finish);
            video.removeEventListener("error", fail);
            resolve();
        };
        const fail = () => {
            video.removeEventListener(eventName, finish);
            video.removeEventListener("error", fail);
            reject(new Error("Failed to read video frame"));
        };
        video.addEventListener(eventName, finish);
        video.addEventListener("error", fail);
    });
}
