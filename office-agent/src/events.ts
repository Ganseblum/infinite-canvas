import { z } from "zod";
import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";

// 归一化事件（契约二 SSE payload），字段与 spec §2.2 一一对应；seq 由本服务临时编号。
export const runStartedPayload = z.object({ model: z.string(), agentName: z.string() });
export const deltaPayload = z.object({ text: z.string() });
export const toolCallPayload = z.object({
    toolCallId: z.string(),
    name: z.string(),
    inputPreview: z.string().optional(),
    status: z.literal("running"),
});
export const toolResultPayload = z.object({
    toolCallId: z.string(),
    status: z.enum(["ok", "error"]),
    isError: z.boolean(),
    outputPreview: z.string().optional(),
});
export const noticePayload = z.object({ level: z.enum(["info", "warn"]), text: z.string() });
export const donePayload = z.object({ inputTokens: z.number(), outputTokens: z.number(), credits: z.number() });
export const errorPayload = z.object({ code: z.string(), message: z.string().optional(), retryable: z.boolean() });

export type RunStartedEvent = { type: "run_started"; payload: z.infer<typeof runStartedPayload> };
export type DeltaEvent = { type: "delta"; payload: z.infer<typeof deltaPayload> };
export type ToolCallEvent = { type: "tool_call"; payload: z.infer<typeof toolCallPayload> };
export type ToolResultEvent = { type: "tool_result"; payload: z.infer<typeof toolResultPayload> };
export type NoticeEvent = { type: "notice"; payload: z.infer<typeof noticePayload> };
export type DoneEvent = { type: "done"; payload: z.infer<typeof donePayload> };
export type ErrorEvent = { type: "error"; payload: z.infer<typeof errorPayload> };
export type NormalizedEvent =
    | RunStartedEvent
    | DeltaEvent
    | ToolCallEvent
    | ToolResultEvent
    | NoticeEvent
    | DoneEvent
    | ErrorEvent;

export const envelopeSchema = z.object({
    v: z.literal(1),
    seq: z.number().int().positive(),
    type: z.string(),
    runId: z.string(),
    ts: z.number(),
    payload: z.unknown(),
});

export const AGENT_NAME = "office-agent";
const PREVIEW_LIMIT = 200;

/** 预览字段统一截断 200 字符（spec §2.2 inputPreview/outputPreview）。 */
export function truncate(text: string, limit = PREVIEW_LIMIT): string {
    return text.length <= limit ? text : text.slice(0, limit) + "…";
}

function blockText(content: unknown): string {
    if (typeof content === "string") return content;
    if (Array.isArray(content)) {
        return content
            .map((b: any) => (typeof b === "string" ? b : b?.type === "text" ? b.text : ""))
            .join("");
    }
    return "";
}

/**
 * SDKMessage → 归一化事件（design §2.3 映射表）。
 * 显式忽略：thinking 块 / thinking_delta（国产模型 thinking 行为不一，本期不透传，预留 reasoning 事件）、
 * assistant 汇总 text 块（delta 已覆盖，避免重复）、usage 增量（M3）、其余 system/stream 子类型。
 */
export function mapSdkMessage(msg: SDKMessage, aborted: boolean): NormalizedEvent[] {
    const events: NormalizedEvent[] = [];
    switch (msg.type) {
        case "system": {
            if (msg.subtype === "init") {
                events.push({ type: "run_started", payload: { model: msg.model, agentName: AGENT_NAME } });
            }
            break;
        }
        case "stream_event": {
            const ev: any = msg.event;
            if (ev?.type === "content_block_delta" && ev.delta?.type === "text_delta" && ev.delta.text) {
                events.push({ type: "delta", payload: { text: ev.delta.text } });
            }
            break;
        }
        case "assistant": {
            const blocks = (msg.message as any)?.content;
            if (Array.isArray(blocks)) {
                for (const block of blocks) {
                    if (block?.type === "tool_use") {
                        events.push({
                            type: "tool_call",
                            payload: {
                                toolCallId: block.id,
                                name: block.name,
                                inputPreview: truncate(JSON.stringify(block.input ?? {})),
                                status: "running",
                            },
                        });
                    }
                }
            }
            break;
        }
        case "user": {
            const blocks = (msg.message as any)?.content;
            if (Array.isArray(blocks)) {
                for (const block of blocks) {
                    if (block?.type === "tool_result") {
                        const isError = Boolean(block.is_error);
                        events.push({
                            type: "tool_result",
                            payload: {
                                toolCallId: block.tool_use_id,
                                status: isError ? "error" : "ok",
                                isError,
                                outputPreview: truncate(blockText(block.content)),
                            },
                        });
                    }
                }
            }
            break;
        }
        case "result": {
            if (msg.subtype === "success" && !msg.is_error) {
                const usage: any = (msg as any).usage ?? {};
                events.push({
                    type: "done",
                    payload: {
                        inputTokens: Number(usage.input_tokens ?? usage.inputTokens ?? 0),
                        outputTokens: Number(usage.output_tokens ?? usage.outputTokens ?? 0),
                        credits: 0,
                    },
                });
            } else {
                // 终态失败：abort 触发的终止统一归 cancelled（retryable=false），其余归 upstream_error。
                const errors: string[] = ((msg as any).errors ?? []).map(String);
                events.push({
                    type: "error",
                    payload: aborted
                        ? { code: "cancelled", retryable: false }
                        : { code: "upstream_error", message: truncate(errors.join("; ") || msg.subtype), retryable: true },
                });
            }
            break;
        }
        default:
            break;
    }
    return events;
}
