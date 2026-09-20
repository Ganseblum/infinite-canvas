import fs from "node:fs";
import path from "node:path";

// sessionId 仅用于服务端推导工作区路径，正则强校验（spec §3 B1 约束），天然排除路径穿越。
// 允许下划线：Go 侧会话主键为 s_ 前缀 + 22 位随机串（spec §4），不含下划线会被 400 全量拒掉。
export const SESSION_ID_RE = /^[a-z0-9][a-z0-9_-]{17,39}$/;

const workspaceRoot = path.resolve(process.env.OFFICE_WORKSPACE_DIR || "./workspaces");
// transcripts 与工作区根目录同级（spec §3：工作区 ../transcripts/）。
const transcriptsDir = path.join(path.dirname(workspaceRoot), "transcripts");

export type Workspace = { dir: string; transcriptFile: string };

/** 按 sessionId 推导工作区目录与 transcript 文件路径；不匹配返回 null。 */
export function resolveWorkspace(sessionId: string): Workspace | null {
    if (!SESSION_ID_RE.test(sessionId)) return null;
    return {
        dir: path.join(workspaceRoot, sessionId),
        transcriptFile: path.join(transcriptsDir, `${sessionId}.json`),
    };
}

/** 创建工作区目录（幂等）；transcripts 目录按需创建。 */
export function ensureWorkspace(ws: Workspace): void {
    fs.mkdirSync(ws.dir, { recursive: true });
    fs.mkdirSync(path.dirname(ws.transcriptFile), { recursive: true });
}

/** 读取该 office sessionId 上次 run 落盘的 SDK session_id（resume-per-turn 依据）。 */
export function readSdkSessionId(ws: Workspace): string | null {
    try {
        const data = JSON.parse(fs.readFileSync(ws.transcriptFile, "utf8")) as { sdkSessionId?: string };
        return typeof data.sdkSessionId === "string" && data.sdkSessionId.length > 0 ? data.sdkSessionId : null;
    } catch {
        return null;
    }
}

/** 每次 system/init（含 resume 后）都会 upsert，兼容 SDK 端 fork 出新 session_id 的情况。 */
export function writeSdkSessionId(ws: Workspace, sdkSessionId: string): void {
    fs.writeFileSync(ws.transcriptFile, JSON.stringify({ sdkSessionId }, null, 2));
}

/** resume 降级时清掉失效映射，避免下次继续命中坏 transcript。 */
export function clearSdkSessionId(ws: Workspace): void {
    fs.rmSync(ws.transcriptFile, { force: true });
}
