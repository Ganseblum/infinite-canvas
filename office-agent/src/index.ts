import { createRequire } from "node:module";
import express from "express";
import { requireInternalToken } from "./auth.js";
import { RunManager, runRequestSchema } from "./run.js";
import type { NormalizedEvent } from "./events.js";
import { resolveWorkspace } from "./workspace.js";

const require = createRequire(import.meta.url);

function sdkVersion(): string {
    try {
        return (require("@anthropic-ai/claude-agent-sdk/package.json") as { version?: string }).version ?? "unknown";
    } catch {
        return "unknown";
    }
}

if (!process.env.OFFICE_INTERNAL_TOKEN) {
    console.error("OFFICE_INTERNAL_TOKEN 未设置，拒绝启动（契约二要求全部请求带内网密钥）");
    process.exit(1);
}

const manager = new RunManager();
const app = express();
app.use(express.json());

// B4 健康检查（供容器探活，不校验内网密钥；仅暴露 SDK 版本，无敏感信息）
app.get("/healthz", (_req, res) => {
    res.json({ ok: true, sdkVersion: sdkVersion() });
});

// B1 起 run：响应即 SSE，事件信封 {v:1,seq,type,runId,ts,payload}，seq 为本服务临时编号（Go 落库重编）
app.post("/v1/runs", requireInternalToken, async (req, res) => {
    const parsed = runRequestSchema.safeParse(req.body);
    if (!parsed.success) {
        res.status(400).json({ code: "invalid_param", message: "请求体不合法", detail: parsed.error.flatten() });
        return;
    }
    const { runId, sessionId, message, agent } = parsed.data;
    // sessionId 仅用于服务端推导工作区，正则不匹配直接 400（spec §3 B1 约束）
    if (!resolveWorkspace(sessionId)) {
        res.status(400).json({ code: "invalid_session_id", message: "sessionId 不匹配 ^[a-z0-9][a-z0-9-]{17,39}$" });
        return;
    }
    if (manager.status(runId).exists) {
        res.status(409).json({ code: "run_conflict", message: "同 runId 的 run 仍在执行" });
        return;
    }

    res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
        "X-Accel-Buffering": "no",
    });
    let seq = 0;
    let finished = false;
    const send = (event: NormalizedEvent) => {
        if (finished) return;
        seq += 1;
        const envelope = { v: 1, seq, type: event.type, runId, ts: Date.now(), payload: event.payload };
        console.log(JSON.stringify({ log: "event", runId, sessionId, seq, type: event.type }));
        res.write(`data: ${JSON.stringify(envelope)}\n\n`);
    };
    // Go 侧断连（契约二 SSE 断开 → Go 判 runtime_lost）：本地不再有事件消费者，中止 query 避免僵尸执行
    res.on("close", () => {
        if (!finished) {
            console.log(JSON.stringify({ log: "sse_disconnected", runId, sessionId }));
            manager.cancel(runId);
        }
    });

    try {
        await manager.start(
            {
                runId,
                sessionId,
                content: message.content,
                systemPrompt: agent?.systemPrompt,
                model: agent?.model,
                tools: agent?.tools,
                skills: agent?.skills,
            },
            send,
        );
    } finally {
        finished = true;
        res.end();
    }
});

// B2 取消：abort() 中断 query，事件流以 error(code=cancelled) 收尾；未知 runId 也返回 200（幂等）
app.post("/v1/runs/:id/cancel", requireInternalToken, (req, res) => {
    const runId = Array.isArray(req.params.id) ? req.params.id[0] : req.params.id;
    manager.cancel(runId);
    res.json({ ok: true });
});

// B3 状态对账（E13 孤儿回收用）
app.get("/v1/runs/:id", requireInternalToken, (req, res) => {
    const runId = Array.isArray(req.params.id) ? req.params.id[0] : req.params.id;
    res.json(manager.status(runId));
});

const port = Number(process.env.OFFICE_PORT) || 8902;
app.listen(port, () => {
    console.log(JSON.stringify({ log: "listening", port, workspaceDir: process.env.OFFICE_WORKSPACE_DIR || "./workspaces", sdkVersion: sdkVersion() }));
});

// 抑制未处理拒绝导致进程退出（SSE 中途客户端断开等异步路径）
process.on("unhandledRejection", (err) => console.error("[unhandledRejection]", err));
