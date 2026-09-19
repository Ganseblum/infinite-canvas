// 社区复刻来源的跨页传递：工作台读取 ?remix= 参数并加载原作后写入会话存储，
// 「我的素材」发布弹窗打开时读取展示，发布成功后清除；会话结束自然失效。
const KEY = "infinite-canvas:remix_source";

export type RemixSource = { workId: string; title: string };

export function peekRemixSource(): RemixSource | null {
    try {
        const raw = sessionStorage.getItem(KEY);
        if (!raw) return null;
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed.workId === "string") {
            return { workId: parsed.workId, title: typeof parsed.title === "string" ? parsed.title : "" };
        }
    } catch {
        // 损坏数据按无来源处理
    }
    return null;
}

export function setRemixSource(source: RemixSource | null) {
    if (source) sessionStorage.setItem(KEY, JSON.stringify(source));
    else sessionStorage.removeItem(KEY);
}
