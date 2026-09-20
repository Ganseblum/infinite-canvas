import i18n from "@/i18n";
import type { CanvasAgentSnapshot } from "@/lib/canvas/canvas-agent-ops";
import type { AgentReasoningEffort } from "@/stores/use-agent-store";

// 本地 Agent（桌面端代理进程）的 HTTP 客户端：与主站后端无关，
// endpoint 是 Agent 进程地址，鉴权走 URL 上的 token 参数。

type AgentConfigResponse = { ok?: boolean; protocolVersion?: number; url?: string; token?: string; hasToken?: boolean };
// 消息里内嵌素材的引用格式：agent-asset:<会话hash>/<文件hash>.<扩展名>。
const AGENT_MESSAGE_ASSET_PATTERN = /^agent-asset:([a-f0-9]{64})\/([a-f0-9]{64}\.(?:gif|jpe?g|png|webp))$/;

/** Agent 接口错误：status 为 HTTP 状态码，response 为服务端返回的错误结构。 */
export class AgentApiError<T = unknown> extends Error {
    constructor(readonly status: number, readonly response: T & { code?: string; error?: string; msg?: string }) {
        super(response.error || response.msg || i18n.t("agent.state.requestFailed"));
        this.name = "AgentApiError";
    }
}

export type AgentSkillScope = "user" | "repo" | "system" | "admin";
export type AgentSkillInterface = { displayName?: string | null; shortDescription?: string | null; defaultPrompt?: string | null };
export type AgentSkillSummary = {
    name: string;
    description: string;
    shortDescription?: string | null;
    interface?: AgentSkillInterface | null;
    dependencies?: unknown;
    path: string;
    scope: AgentSkillScope;
    enabled: boolean;
    managed: boolean;
};
export type AgentSkillDetail = {
    name: string;
    description: string;
    instructions: string;
    interface?: AgentSkillInterface | null;
    path: string;
    managed: true;
    revision: string;
};
export type AgentSkillInput = { name?: string; description: string; instructions: string; interface?: AgentSkillInterface | null; expectedRevision?: string };
export type AgentSkillDraft = { name: string; displayName: string; description: string; instructions: string; shortDescription: string; defaultPrompt: string };
export type AgentSkillDraftInput = { source: "conversation" | "canvas"; threadId: string; clientId: string; model?: string; effort?: AgentReasoningEffort };
export type AgentSkillsResponse = { ok?: boolean; data?: AgentSkillSummary[]; errors?: unknown[] };
export type AgentSkillResponse = { ok?: boolean; data?: AgentSkillDetail };
export type AgentSkillDraftResponse = { ok?: boolean; data?: AgentSkillDraft };

/** 上报画布快照给 Agent（Agent 据此感知画布内容）；失败静默，不打断对话。 */
export async function postState(endpoint: string, token: string, clientId: string, snapshot: CanvasAgentSnapshot | null) {
    try {
        const response = await fetch(`${endpoint}/canvas/state?token=${encodeURIComponent(token)}&clientId=${encodeURIComponent(clientId)}`, {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify(snapshot ? { ...snapshot, hasCanvas: true } : { hasCanvas: false }),
        });
        return response.ok;
    } catch {
        return false;
    }
}

export async function activateAgentClient(endpoint: string, token: string, clientId: string) {
    try {
        await fetch(`${endpoint}/canvas/activate?token=${encodeURIComponent(token)}&clientId=${encodeURIComponent(clientId)}`, { method: "POST" });
    } catch {}
}

/** 把工具调用的执行结果回传给 Agent。 */
export async function postToolResult(endpoint: string, token: string, clientId: string, body: { requestId: string; result?: unknown; error?: string }) {
    await fetchAgentJson(endpoint, token, `/canvas/result?clientId=${encodeURIComponent(clientId)}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
}

/** 回应 Agent 的权限审批请求（接受/本次会话内接受/拒绝）。 */
export async function postCodexApproval(endpoint: string, token: string, requestId: string, decision: "accept" | "acceptForSession" | "decline") {
    await fetchAgentJson(endpoint, token, "/agent/codex/approval", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ requestId, decision }) });
}

/** 中断 Agent 当前一轮对话。 */
export async function interruptCodexTurn(endpoint: string, token: string, threadId?: string) {
    await fetchAgentJson(endpoint, token, "/agent/codex/interrupt", jsonPost({ threadId }));
}

/** 确认历史 turn 已在面板展示，Agent 侧不再重复推送。 */
export async function acknowledgeCodexHistory(endpoint: string, token: string, threadId: string, turnIds: string[]) {
    await fetchAgentJson(endpoint, token, "/agent/codex/history/ack", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ threadId, turnIds }) });
}

/** 让 Agent 进程在本机文件管理器里打开指定文件。 */
export async function revealAgentLocalFile(endpoint: string, token: string, path: string) {
    await fetchAgentJson(endpoint, token, "/agent/local-file/reveal", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ path }) });
}

/** 把消息里的 agent-asset 引用解析成可访问的完整 URL；无法解析返回空串。 */
export function resolveAgentMessageAssetUrl(endpoint: string, token: string, value: string) {
    const match = AGENT_MESSAGE_ASSET_PATTERN.exec(value);
    if (!match) return value.startsWith("agent-asset:") ? "" : value;
    const baseUrl = endpoint.trim().replace(/\/$/, "");
    return baseUrl && token ? `${baseUrl}/agent/message-assets/${match[1]}/${match[2]}?token=${encodeURIComponent(token)}` : "";
}

/** 技能列表；forceReload 让 Agent 侧跳过缓存重读磁盘。 */
export function fetchCodexSkills(endpoint: string, token: string, forceReload = false) {
    return fetchAgentJson<AgentSkillsResponse>(endpoint, token, `/agent/codex/skills${forceReload ? "?forceReload=1" : ""}`);
}

/** 读取技能全文（instructions/revision），managed 恒为 true 表示由 Agent 托管。 */
export function fetchCodexSkill(endpoint: string, token: string, name: string) {
    return fetchAgentJson<AgentSkillResponse>(endpoint, token, `/agent/codex/skills/${encodeURIComponent(name)}`);
}

export function createCodexSkill(endpoint: string, token: string, input: AgentSkillInput) {
    return fetchAgentJson<AgentSkillResponse>(endpoint, token, "/agent/codex/skills", jsonPost(input));
}

export function createCodexSkillDraft(endpoint: string, token: string, input: AgentSkillDraftInput) {
    return fetchAgentJson<AgentSkillDraftResponse>(endpoint, token, "/agent/codex/skills/draft", jsonPost(input));
}

export function updateCodexSkill(endpoint: string, token: string, name: string, input: AgentSkillInput) {
    return fetchAgentJson<AgentSkillResponse>(endpoint, token, `/agent/codex/skills/${encodeURIComponent(name)}`, jsonPost(input));
}

/** 删除技能；expectedRevision 用于并发冲突检测。 */
export function deleteCodexSkill(endpoint: string, token: string, name: string, expectedRevision: string) {
    return fetchAgentJson<{ ok?: boolean }>(endpoint, token, `/agent/codex/skills/${encodeURIComponent(name)}/delete`, jsonPost({ expectedRevision }));
}

export function setCodexSkillEnabled(endpoint: string, token: string, skill: Pick<AgentSkillSummary, "name" | "path">, enabled: boolean) {
    return fetchAgentJson<{ ok?: boolean }>(endpoint, token, `/agent/codex/skills/${encodeURIComponent(skill.name)}/enabled`, jsonPost({ ...skill, enabled }));
}

/** Agent 接口统一请求：token 拼在 URL 上（Agent 进程约定），非 2xx 抛 AgentApiError。 */
export async function fetchAgentJson<T>(endpoint: string, token: string, path: string, init?: RequestInit) {
    const url = `${endpoint}${path}${path.includes("?") ? "&" : "?"}token=${encodeURIComponent(token)}`;
    const res = await fetch(url, init);
    const data = (await res.json().catch(() => ({}))) as T & { error?: string; msg?: string };
    if (!res.ok) throw new AgentApiError(res.status, data);
    return data;
}

/** 探测 Agent 进程的 /config，返回连接配置（含协议版本），不可达返回 null。 */
export async function discoverAgentConfig(endpoint: string) {
    try {
        const res = await fetch(`${endpoint}/config`);
        if (!res.ok) return null;
        const data = (await res.json()) as AgentConfigResponse;
        return data.ok ? data : null;
    } catch {
        return null;
    }
}

function jsonPost(body: unknown): RequestInit {
    return { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
}
