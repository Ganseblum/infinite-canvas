// 标签页会话标识：存 sessionStorage，同一标签页刷新保持不变，新开标签页/新会话天然产生新 id。
const SESSION_ID_KEY = "ai_session_id";

export function getSessionId(): string {
    let sessionId = sessionStorage.getItem(SESSION_ID_KEY);
    if (!sessionId) {
        // crypto.randomUUID 仅在安全上下文（HTTPS/localhost）暴露，不可用时退化为时间戳+随机数拼接。
        sessionId =
            typeof crypto !== "undefined" && "randomUUID" in crypto
                ? crypto.randomUUID()
                : `sid-${Date.now()}-${Math.random().toString(36).slice(2)}`;
        sessionStorage.setItem(SESSION_ID_KEY, sessionId);
    }
    return sessionId;
}
