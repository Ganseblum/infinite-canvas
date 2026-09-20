/**
 * 把原始尺寸等比缩到 maxW×maxH 以内（画布节点摆放用），只缩小不放大，最小 1px。
 * @returns 等比缩放后的宽高，坐标系为画布世界坐标（px）。
 */
export function fitNodeSize(width: number, height: number, maxWidth = 640, maxHeight = 640) {
    const w = Math.max(1, width);
    const h = Math.max(1, height);
    const scale = Math.min(1, maxWidth / w, maxHeight / h);
    return { width: w * scale, height: h * scale };
}

/**
 * 按 "宽x高" 或 "宽:高" 字符串推导节点尺寸：等比填进 baseW×baseH 的内接矩形。
 * 比例过扁过窄（<0.25 或 >4）视为异常，退回基准尺寸，避免节点小到不可操作。
 */
export function nodeSizeFromRatio(size: string, baseWidth: number, baseHeight: number) {
    const match = size?.match(/^(\d+)(?:x|:)(\d+)/);
    if (!match) return null;
    const width = Number(match[1]);
    const height = Number(match[2]);
    const ratio = width / Math.max(1, height);
    if (ratio < 0.25 || ratio > 4) return { width: baseWidth, height: baseHeight };
    return ratio >= baseWidth / baseHeight ? { width: baseWidth, height: baseWidth / ratio } : { width: baseHeight * ratio, height: baseHeight };
}
