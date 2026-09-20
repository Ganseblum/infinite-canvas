import { afterEach, describe, expect, it, vi } from "vitest";

import { EventsExpiredError, SseAuthError, openSse, type SseFrame } from "@/lib/office/sse";

const URL = "http://localhost/api/v1/office/sessions/s_1/runs/r_1/stream?lastSeq=0";

// 用 ReadableStream 按给定分块回放响应体，分块边界即 chunk 边界（半包/粘包测试的关键）。
function serveStream(chunks: (string | Uint8Array)[]) {
    const encoder = new TextEncoder();
    const queue = chunks.map((chunk) => (typeof chunk === "string" ? encoder.encode(chunk) : chunk));
    vi.stubGlobal(
        "fetch",
        vi.fn(
            async () =>
                new Response(
                    new ReadableStream<Uint8Array>({
                        pull(controller) {
                            const chunk = queue.shift();
                            if (chunk === undefined) controller.close();
                            else controller.enqueue(chunk);
                        },
                    }),
                    { status: 200, headers: { "Content-Type": "text/event-stream" } },
                ),
        ),
    );
}

async function drain(): Promise<SseFrame[]> {
    const stream = await openSse(URL, { token: "token-1" });
    const frames: SseFrame[] = [];
    for await (const frame of stream) frames.push(frame);
    return frames;
}

afterEach(() => {
    vi.unstubAllGlobals();
});

describe("sse 解析器", () => {
    it("解析单事件帧并注入 Bearer 头；注释行与空帧忽略", async () => {
        const fetchMock = vi.fn(async (_url: string | URL, _init?: RequestInit) => new Response('id: 7\nevent: delta\ndata: {"seq":7}\n\n: ping\n\n\n', { status: 200, headers: { "Content-Type": "text/event-stream" } }));
        vi.stubGlobal("fetch", fetchMock);
        const frames = await drain();
        expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ headers: { Authorization: "Bearer token-1" } });
        expect(frames).toEqual([{ id: "7", event: "delta", data: '{"seq":7}' }]);
    });

    it("跨 chunk 半包：单帧拆成多块（含 UTF-8 多字节断开）仍解析为一帧", async () => {
        // 字节级切点落在「办」的 3 字节编码中间，验证 TextDecoder(stream:true) 的跨块衔接。
        const bytes = new TextEncoder().encode('id: 3\nevent: delta\ndata: {"text":"办公助理，你好"}\n\n');
        serveStream([bytes.slice(0, 17), bytes.slice(17)]);
        expect(await drain()).toEqual([{ id: "3", event: "delta", data: '{"text":"办公助理，你好"}' }]);
    });

    it("粘包与跨 chunk 帧边界：多事件连发全部拆开；id 字段跨帧保持（SSE lastEventId 语义）", async () => {
        serveStream(['id: 1\ndata: {"seq":1}\n\nid: 2\ndata: {"seq":', '2}\n\ndata: {"seq":3}\n\n']);
        expect(await drain()).toEqual([
            { id: "1", event: "", data: '{"seq":1}' },
            { id: "2", event: "", data: '{"seq":2}' },
            { id: "2", event: "", data: '{"seq":3}' },
        ]);
    });

    it("多行 data 拼接为单事件 JSON；EOF 未被空行终结的残帧按规范丢弃", async () => {
        serveStream(['data: {"a":\ndata: 1}\n\ndata: {"tail":', '"no-newline"}']);
        expect(await drain()).toEqual([{ id: "", event: "", data: '{"a":\n1}' }]);
    });

    it("410 重放空洞抛 EventsExpiredError（spec §2.3 E11）", async () => {
        vi.stubGlobal(
            "fetch",
            vi.fn(async () => new Response('{"error":{"code":"events_expired"}}', { status: 410 })),
        );
        const error = await openSse(URL).then(
            () => null,
            (e: unknown) => e,
        );
        expect(error).toBeInstanceOf(EventsExpiredError);
        expect(error).toMatchObject({ code: "events_expired", status: 410 });
    });

    it("401 抛 SseAuthError 供调用方刷新后重试；其余状态抛 ApiError", async () => {
        vi.stubGlobal(
            "fetch",
            vi.fn(async () => new Response('{"error":{"code":"TOKEN_EXPIRED"}}', { status: 401 })),
        );
        await expect(openSse(URL)).rejects.toBeInstanceOf(SseAuthError);
        vi.stubGlobal(
            "fetch",
            vi.fn(async () => new Response('{"error":{"code":"run_conflict"}}', { status: 409 })),
        );
        await expect(openSse(URL)).rejects.toMatchObject({ code: "run_conflict", status: 409 });
    });
});
