import { query, type Options, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { z } from "zod";
import { mapSdkMessage, type NormalizedEvent } from "./events.js";
import { ensureWorkspace, clearSdkSessionId, readSdkSessionId, resolveWorkspace, writeSdkSessionId, type Workspace } from "./workspace.js";

/** 契约二 B1 请求体（spec §3；attachments 本期透传校验、暂不消费）。sessionId 格式错误由 workspace 层判 invalid_session_id。
 *  可选字段一律 nullish：Go 结构体未带 omitempty 时会把空切片/空串序列化成显式 null，仅 optional 会 400。 */
export const runRequestSchema = z.object({
    runId: z.string().min(1),
    sessionId: z.string().min(1),
    message: z.object({
        role: z.literal("user"),
        content: z.string().min(1),
        attachments: z.array(z.object({ key: z.string(), mime: z.string(), name: z.string() })).nullish(),
    }),
    agent: z
        .object({
            systemPrompt: z.string().nullish(),
            model: z.string().nullish(),
            tools: z.array(z.string()).nullish(),
            skills: z.array(z.string()).nullish(),
        })
        .nullish(),
});

export type StartRunParams = {
    runId: string;
    sessionId: string;
    content: string;
    systemPrompt?: string;
    model?: string;
    tools?: string[];
    skills?: string[];
};

type RunEntry = { sessionId: string; controller: AbortController; phase: "starting" | "running" };

/** 携带 sawInit 的失败：用于判断 resume 降级（init 都没收到说明会话没建起来）。 */
class RunAttemptError extends Error {
    sawInit: boolean;
    constructor(err: unknown, sawInit: boolean) {
        super(err instanceof Error ? err.message : String(err));
        this.sawInit = sawInit;
    }
}

export class RunManager {
    private runs = new Map<string, RunEntry>();

    status(runId: string): { exists: boolean; phase: "idle" | "starting" | "running"; sessionId: string | null } {
        const entry = this.runs.get(runId);
        if (!entry) return { exists: false, phase: "idle", sessionId: null };
        return { exists: true, phase: entry.phase, sessionId: entry.sessionId };
    }

    /** 幂等取消：未知 runId 静默忽略（B2 约定返回 200）。 */
    cancel(runId: string): void {
        this.runs.get(runId)?.controller.abort();
    }

    /**
     * 起 run 并跑完整个 query() 生命周期；每个归一化事件经 emit 回调写出。
     * run_started（system/init）后 phase=running，终态（done/error 发出后）注销。
     */
    async start(params: StartRunParams, emit: (event: NormalizedEvent) => void): Promise<void> {
        if (this.runs.has(params.runId)) throw new Error(`run ${params.runId} already active`);
        const ws = resolveWorkspace(params.sessionId);
        if (!ws) throw new Error("invalid session id");
        ensureWorkspace(ws);
        this.runs.set(params.runId, { sessionId: params.sessionId, controller: new AbortController(), phase: "starting" });
        console.log(JSON.stringify({ log: "run_start", runId: params.runId, sessionId: params.sessionId }));
        try {
            if (params.skills?.length) {
                // skills 接入（SKILL.md 扫描加载）属后续批次，显式提示而非静默丢弃。
                emit({ type: "notice", payload: { level: "info", text: `skills 本期未接入 Runtime，已忽略：${params.skills.join(", ")}` } });
            }
            const resumeId = readSdkSessionId(ws);
            if (resumeId) {
                emit({ type: "notice", payload: { level: "info", text: `resume 分支：续接 SDK 会话 ${resumeId}` } });
            }
            try {
                await this.attempt(params, ws, emit, resumeId);
            } catch (err) {
                const attemptErr = err instanceof RunAttemptError ? err : null;
                const aborted = this.runs.get(params.runId)?.controller.signal.aborted ?? false;
                if (resumeId && !aborted && !(attemptErr?.sawInit ?? true)) {
                    // resume 降级：transcript 失效（如 Runtime 重启丢了 CLI 会话文件），改用新会话重跑一次。
                    emit({ type: "notice", payload: { level: "warn", text: `resume 失败，已降级为新会话：${attemptErr?.message ?? err}` } });
                    clearSdkSessionId(ws);
                    await this.attempt(params, ws, emit, null);
                } else {
                    throw err;
                }
            }
        } catch (err) {
            const aborted = this.runs.get(params.runId)?.controller.signal.aborted ?? false;
            emit(
                aborted
                    ? { type: "error", payload: { code: "cancelled", retryable: false } }
                    : { type: "error", payload: { code: "upstream_error", message: err instanceof Error ? err.message : String(err), retryable: true } },
            );
        } finally {
            this.runs.delete(params.runId);
            console.log(JSON.stringify({ log: "run_end", runId: params.runId, sessionId: params.sessionId }));
        }
    }

    private async attempt(params: StartRunParams, ws: Workspace, emit: (event: NormalizedEvent) => void, resumeId: string | null): Promise<void> {
        const controller = new AbortController();
        this.runs.get(params.runId)!.controller = controller;
        // SDK 实际 API 以 node_modules 内 .d.ts 为准（0.1.77）：
        // - systemPrompt 原生支持自定义字符串，直接透传 agent.systemPrompt，无需拼进 prompt；
        // - 自动放行用 permissionMode: 'bypassPermissions' + allowDangerouslySkipPermissions: true（headless 无人审批）；
        // - resume 传上次 SDK session_id；同会话串行由 Go 侧保证。
        const options: Options = {
            model: params.model || process.env.ANTHROPIC_MODEL || undefined,
            cwd: ws.dir,
            abortController: controller,
            permissionMode: "bypassPermissions",
            allowDangerouslySkipPermissions: true,
            includePartialMessages: true,
            // 网关 glm-5.3 默认开启思考，给足 thinking 预算（输出上限走 CLI 默认，未额外设 CLAUDE_CODE_MAX_OUTPUT_TOKENS）
            maxThinkingTokens: 8192,
            env: { ...process.env, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1" },
            stderr: (data) => console.error(`[sdk][${params.runId}] ${data.trimEnd()}`),
        };
        if (params.systemPrompt) options.systemPrompt = params.systemPrompt;
        if (params.tools) options.tools = params.tools;
        if (resumeId) options.resume = resumeId;

        let sawInit = false;
        try {
            const q = query({ prompt: params.content, options });
            for await (const msg of q as AsyncIterable<SDKMessage>) {
                if (msg.type === "system" && msg.subtype === "init") {
                    sawInit = true;
                    this.runs.get(params.runId)!.phase = "running";
                    writeSdkSessionId(ws, msg.session_id);
                }
                for (const event of mapSdkMessage(msg, controller.signal.aborted)) emit(event);
                if (msg.type === "result") return;
            }
            // 事件流结束但没有 result 消息：按异常收尾
            if (controller.signal.aborted) {
                emit({ type: "error", payload: { code: "cancelled", retryable: false } });
            } else {
                throw new Error("SDK 事件流在 result 前提前结束");
            }
        } catch (err) {
            if (controller.signal.aborted) {
                emit({ type: "error", payload: { code: "cancelled", retryable: false } });
                return;
            }
            throw new RunAttemptError(err, sawInit);
        }
    }
}
