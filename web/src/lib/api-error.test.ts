import { describe, expect, it } from "vitest";

import { ApiError, getApiErrorMessage } from "./api-error";

describe("getApiErrorMessage", () => {
    it("点数不足按微元原值展示，不再缩小 100 万倍", () => {
        const message = getApiErrorMessage(new ApiError({ code: "INSUFFICIENT_CREDITS", shortfallMicros: 1_280_000 }));

        expect(message).toContain("1,280,000");
        expect(message).not.toContain("1.28");
    });

    it("限流带 Retry-After 时展示等待秒数，缺失时退回普通文案", () => {
        expect(getApiErrorMessage(new ApiError({ code: "RATE_LIMITED", retryAfter: 30 }))).toContain("30");
        expect(getApiErrorMessage(new ApiError({ code: "RATE_LIMITED" }))).toBe("操作过于频繁，请稍后重试");
    });
});
