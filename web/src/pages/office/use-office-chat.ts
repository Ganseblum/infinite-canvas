import { useEffect } from "react";

import { useOfficeStore } from "@/stores/office";

// 页面私有 hook（前端方案 §3）：生命周期管理 + rAF 节流（D2），状态读写全部走 store（D5）。
export function useOfficeChat(sessionId?: string) {
    const loadSessions = useOfficeStore((s) => s.loadSessions);
    const openSession = useOfficeStore((s) => s.openSession);

    useEffect(() => {
        void loadSessions();
    }, [loadSessions]);

    useEffect(() => {
        if (sessionId) void openSession(sessionId);
    }, [sessionId, openSession]);

    // D2：delta 进 buffer，每帧一次搬运到 displayText，避免逐 delta 触发 markdown 重渲染。
    useEffect(() => {
        let raf = 0;
        const loop = () => {
            useOfficeStore.getState().flushBuffer();
            raf = requestAnimationFrame(loop);
        };
        raf = requestAnimationFrame(loop);
        return () => cancelAnimationFrame(raf);
    }, []);

    // 离开页面断开 SSE 连接：run 在服务端继续，回来走快照 + 事件重放（前端方案 §5.2）。
    useEffect(
        () => () => {
            useOfficeStore.getState().abortStream();
        },
        [],
    );
}
