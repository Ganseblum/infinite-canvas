import crypto from "node:crypto";
import type { NextFunction, Request, Response } from "express";

/**
 * 契约二内网密钥校验（spec §3：缺失或不匹配返回 401）。
 * 常量时间比较：先归一化为等长摘要再 timingSafeEqual，避免长度泄漏与长度不等抛错。
 */
export function requireInternalToken(req: Request, res: Response, next: NextFunction): void {
    const expected = process.env.OFFICE_INTERNAL_TOKEN || "";
    const provided = req.header("X-Office-Internal-Token") || "";
    const a = crypto.createHash("sha256").update(expected).digest();
    const b = crypto.createHash("sha256").update(provided).digest();
    if (expected.length > 0 && crypto.timingSafeEqual(a, b)) {
        next();
        return;
    }
    res.status(401).json({ code: "unauthorized", message: "缺少或不匹配 X-Office-Internal-Token" });
}
