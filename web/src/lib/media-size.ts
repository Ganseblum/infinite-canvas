// 媒体尺寸档位与比例表：图片/视频共用「比例 + 档位 → 像素尺寸」的推导逻辑。
// 表中的像素值即直接发给生成接口的 size 参数，均为偶数边（编码器友好）。
export const mediaScaleOptions = ["1k", "2k", "4k", "auto"] as const;
export const mediaRatioOptions = [
    { value: "1:1", width: 1, height: 1 },
    { value: "2:3", width: 2, height: 3 },
    { value: "3:2", width: 3, height: 2 },
    { value: "4:3", width: 4, height: 3 },
    { value: "3:4", width: 3, height: 4 },
    { value: "16:9", width: 16, height: 9 },
    { value: "9:16", width: 9, height: 16 },
    { value: "21:9", width: 21, height: 9 },
    { value: "9:21", width: 9, height: 21 },
    { value: "auto", width: 0, height: 0 },
] as const;

// 每档比例对应的像素尺寸预设；auto 比例宽高为 0 表示跟随上游默认。
export const imageSizePresets: Record<string, Record<string, string>> = {
    "1k": { "1:1": "1024x1024", "2:3": "1024x1536", "3:2": "1536x1024", "4:3": "1024x768", "3:4": "768x1024", "16:9": "1536x864", "9:16": "864x1536", "21:9": "2016x864", "9:21": "864x2016" },
    "2k": { "1:1": "2048x2048", "2:3": "1360x2048", "3:2": "2048x1360", "4:3": "2048x1536", "3:4": "1536x2048", "16:9": "2048x1152", "9:16": "1152x2048", "21:9": "2688x1152", "9:21": "1152x2688" },
    "4k": { "1:1": "2880x2880", "2:3": "2336x3520", "3:2": "3520x2336", "4:3": "3312x2480", "3:4": "2480x3312", "16:9": "3840x2160", "9:16": "2160x3840", "21:9": "3840x1648", "9:21": "1648x3840" },
};

/** 解析 "1024x1024" 式像素尺寸，非法返回 null。 */
export function parsePixelSize(value: string) {
    const match = String(value || "").match(/^(\d+)x(\d+)$/i);
    if (!match) return null;
    return { width: Number(match[1]), height: Number(match[2]) };
}

/** 解析 "16:9" 式比例（支持小数），宽高为 0 时视为非法。 */
export function parseAspectRatio(value: string) {
    const match = String(value || "").match(/^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$/);
    if (!match) return null;
    const width = Number(match[1]);
    const height = Number(match[2]);
    if (!width || !height) return null;
    return { width, height };
}

export const videoRatioOptions = [
    { value: "1:1", width: 1, height: 1 },
    { value: "3:4", width: 3, height: 4 },
    { value: "4:3", width: 4, height: 3 },
    { value: "16:9", width: 16, height: 9 },
    { value: "9:16", width: 9, height: 16 },
    { value: "21:9", width: 21, height: 9 },
    { value: "auto", width: 0, height: 0 },
] as const;

// 视频生成允许的时长上下限（秒），表单与请求侧共用同一钳制。
export const VIDEO_SECONDS_MIN = 4;
export const VIDEO_SECONDS_MAX = 30;

/** 归一化图片档位：兼容 2048/3840 等旧写法，未知值回落 1k。 */
export function normalizeMediaScale(value: string | undefined) {
    const scale = String(value || "").trim().toLowerCase();
    if (scale === "2k" || scale === "2048") return "2k";
    if (scale === "4k" || scale === "3840") return "4k";
    if (scale === "auto") return "auto";
    if (mediaScaleOptions.includes(scale as (typeof mediaScaleOptions)[number])) return scale;
    return "1k";
}

/** 从已存的 size 推断所属档位：优先记忆值，其次查预设表，最后按长边阈值粗分。 */
export function inferMediaScale(size: string, storedScale?: string) {
    if (storedScale) return normalizeMediaScale(storedScale);
    if (!size || size === "auto") return "auto";
    if (parseAspectRatio(size)) return "auto";
    const pixels = parsePixelSize(size);
    if (!pixels) return "auto";
    const presetScale = Object.keys(imageSizePresets).find((scale) => Object.values(imageSizePresets[scale]).includes(`${pixels.width}x${pixels.height}`));
    if (presetScale) return presetScale;
    const longSide = Math.max(pixels.width, pixels.height);
    if (longSide >= 3072) return "4k";
    if (longSide >= 1536) return "2k";
    return "1k";
}

/** 从 size（比例或像素）推断最接近的展示比例，找不到时用 fallback。 */
export function inferMediaRatio(size: string, fallback = "1:1") {
    if (!size || size === "auto") return "auto";
    if (mediaRatioOptions.some((item) => item.value === size)) return size;
    const pixels = parsePixelSize(size) || parseAspectRatio(size);
    if (!pixels) return fallback;
    const target = pixels.width / pixels.height;
    return mediaRatioOptions
        .filter((item) => item.value !== "auto")
        .reduce((best, item) => {
            const current = item.width / item.height;
            const bestOption = mediaRatioOptions.find((option) => option.value === best);
            const bestRatio = (bestOption?.width || 1) / Math.max(1, bestOption?.height || 1);
            return Math.abs(current - target) < Math.abs(bestRatio - target) ? item.value : best;
        }, fallback);
}

/** 档位 × 比例 → 发给接口的 size；任一为 auto 时不限定尺寸。 */
export function computeMediaSize(scale: string, ratio: string) {
    if (ratio === "auto" || !ratio) return "auto";
    const normalizedScale = normalizeMediaScale(scale);
    if (normalizedScale === "auto") return ratio;
    return imageSizePresets[normalizedScale][ratio];
}

export function readMediaDimensions(size: string, scale: string, ratio: string) {
    const pixels = parsePixelSize(size);
    if (pixels) return pixels;
    const computed = computeMediaSize(scale === "auto" ? "1k" : scale, ratio === "auto" ? "1:1" : ratio);
    return parsePixelSize(computed) || { width: 0, height: 0 };
}

/** 视频时长字符串钳制到 [4, 30] 秒并取整，非法值回落 6。 */
export function clampVideoSeconds(value: string) {
    const seconds = Math.floor(Number(value) || 6);
    return String(Math.max(VIDEO_SECONDS_MIN, Math.min(VIDEO_SECONDS_MAX, seconds)));
}

/** 视频分辨率归一化为 "720"/"1080" 数字串；low/auto/high/medium 等别名映射到对应档位。 */
export function parseVideoResolution(value: string | undefined) {
    const raw = String(value || "").trim().toLowerCase();
    if (raw === "low") return "480";
    if (raw === "auto" || raw === "high" || raw === "medium") return "720";
    const number = raw.replace(/p$/i, "");
    return /^\d+$/.test(number) && Number(number) > 0 ? number : "720";
}

export function inferVideoRatio(size: string) {
    if (!size || size === "auto") return "auto";
    if (videoRatioOptions.some((item) => item.value === size)) return size;
    const pixels = parsePixelSize(size) || parseAspectRatio(size);
    if (!pixels) return "16:9";
    const target = pixels.width / pixels.height;
    return videoRatioOptions
        .filter((item) => item.value !== "auto")
        .reduce((best, item) => {
            const current = item.width / item.height;
            const bestOption = videoRatioOptions.find((option) => option.value === best);
            const bestRatio = (bestOption?.width || 16) / (bestOption?.height || 9);
            return Math.abs(current - target) < Math.abs(bestRatio - target) ? item.value : best;
        }, "16:9");
}

/** 分辨率（短边 px）× 比例 → 视频像素尺寸；宽高都取偶数（视频编码要求），非法入参时为 auto。 */
export function computeVideoSize(resolution: string, ratio: string) {
    if (ratio === "auto" || !ratio) return "auto";
    const parsed = parseAspectRatio(ratio);
    if (!parsed) return "auto";
    const p = Math.max(1, Math.floor(Number(parseVideoResolution(resolution)) || 720));
    const landscape = parsed.width >= parsed.height;
    const width = evenRound(landscape ? (p * parsed.width) / parsed.height : p);
    const height = evenRound(landscape ? p : (p * parsed.height) / parsed.width);
    return `${width}x${height}`;
}

export function readVideoDimensions(size: string, resolution: string, ratio: string) {
    const pixels = parsePixelSize(size);
    if (pixels) return pixels;
    const computed = computeVideoSize(resolution, ratio === "auto" ? "16:9" : ratio);
    return parsePixelSize(computed) || { width: 0, height: 0 };
}

function evenRound(value: number) {
    return Math.max(2, Math.round(value / 2) * 2);
}
