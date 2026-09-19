import { describe, expect, it } from "vitest";

import { pickImageSource } from "./image-thumbnail";

const base = {
    previewUrl: "data:image/webp;base64,preview",
    originalUrl: "data:image/png;base64,original",
    naturalWidth: 4000,
    naturalHeight: 3000,
    renderedWidth: 420,
    renderedHeight: 315,
};

describe("pickImageSource", () => {
    it("预览像素足够时使用预览", () => {
        expect(pickImageSource({ ...base, scale: 1, devicePixelRatio: 1 })).toBe(base.previewUrl);
    });

    it("屏显尺寸超过预览上限时回退原图", () => {
        expect(pickImageSource({ ...base, scale: 3, devicePixelRatio: 2 })).toBe(base.originalUrl);
    });

    it("没有预览时使用原图", () => {
        expect(pickImageSource({ ...base, previewUrl: undefined, scale: 1, devicePixelRatio: 1 })).toBe(base.originalUrl);
    });
});
