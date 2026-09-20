import { isSiteTool, SITE_TOOL_LABELS } from "@/lib/agent/agent-site-tools";
import i18n from "@/i18n";
import { summarizeCanvasAgentOps, type CanvasAgentOp } from "@/lib/canvas/canvas-agent-ops";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { randomId } from "@/lib/utils";
import { resolveAgentMessageAssetUrl } from "@/services/api/canvas-agent";
import { useAgentStore, type AgentAttachment, type AgentChatItem, type AgentEventLog, type AgentMessageAttachment, type AgentTokenUsage } from "@/stores/use-agent-store";
import type { AgentChatAttachment } from "./agent-chat-message";
/** 推理进行中的占位文案；用于区分「还没有真实推理摘要」和「已有内容」两种状态。 */
export const REASONING_PLACEHOLDER = i18n.t("agent.events.analyzing");

/**
 * 本地 Agent SSE 事件（agent_event / agent_error 等）的负载。
 * 字段做的是协议层容错：服务端可能下发 snake_case 或 camelCase，取值方需两者都兜。
 */
export type AgentEventPayload = {
    agent?: string;
    type?: string;
    threadId?: string;
    thread_id?: string;
    turnId?: string;
    turn_id?: string;
    sourceClientId?: string;
    replayed?: boolean;
    item?: AgentEventItem;
    error?: { message?: string };
    message?: string;
    status?: string;
    explanation?: unknown;
    plan?: unknown;
    usage?: Record<string, unknown>;
    duration_ms?: number;
};
/** 事件里单个 item（推理块 / 命令执行 / 工具调用 / 文件改动等）的原始字段，字段类型未收紧，读取方自行判型。 */
export type AgentEventItem = {
    id?: string;
    type?: string;
    text?: unknown;
    delta?: unknown;
    message?: unknown;
    server?: string;
    tool?: string;
    status?: string;
    arguments?: unknown;
    result?: unknown;
    error?: { message?: string };
    command?: unknown;
    cwd?: unknown;
    aggregatedOutput?: unknown;
    exitCode?: unknown;
    durationMs?: unknown;
    contentItems?: unknown;
    success?: unknown;
    changes?: unknown;
    summary?: unknown;
    query?: unknown;
    action?: unknown;
    path?: unknown;
    savedPath?: unknown;
    revisedPrompt?: unknown;
};
/** 工具/活动卡片的结构化详情：rows 为键值对摘要，files 为文件改动列表，output 为原始输出或错误信息。 */
export type AgentUserDetail = { kind: string; status: string; rows?: Array<{ label: string; value: string }>; output?: string; files?: Array<{ path: string; action?: string }>; tasks?: Array<{ step: string; status: string }>; explanation?: string };

/** 日志视图顶部展示的连接诊断上下文快照。 */
export type AgentLogContext = { endpoint: string; connected: boolean; enabled: boolean; activity: string; waiting: boolean; sending: boolean; messages: number; pendingTool?: string };

/** 把 store 里的会话条目转成 UI 渲染用的消息：非用户/助手消息保留 meta，附件地址换成带鉴权的资源 URL。 */
export function agentMessageToChatMessage(item: AgentChatItem, endpoint: string, token: string) {
    return { ...item, meta: item.role === "user" || item.role === "assistant" ? undefined : item.meta, attachments: item.attachments?.map((attachment) => agentAttachmentToChatAttachment(attachment, endpoint, token)) };
}

/** 附件地址统一走带 token 的资源接口解析，避免把原始本地路径直接暴露给 <img>。 */
export function agentAttachmentToChatAttachment(item: AgentMessageAttachment, endpoint: string, token: string): AgentChatAttachment {
    return { id: item.id, name: item.name, url: resolveAgentMessageAssetUrl(endpoint, token, item.dataUrl || item.url) };
}

/** 把事件折叠成正式助手回复；只有 item.completed 的 agent_message 会产出消息，其余事件返回 null 交给活动卡片处理。 */
export function formatAgentEvent(event: AgentEventPayload): Omit<AgentChatItem, "id"> | null {
    const item = event.item;
    if (event.type === "item.completed" && item?.type === "agent_message") return { role: "assistant", title: "Codex", text: stringText(item.text) };
    return null;
}

/**
 * 把 item.started / item.completed 事件格式化为时间线上的「活动卡片」消息。
 * 按 item.type 分派（推理 / 计划 / 命令 / 文件 / 搜索 / 图片 / MCP 工具等），
 * 完成且无内容的事件返回 null 以避免刷屏；失败信息优先取 item.error。
 */
export function formatAgentActivity(event: AgentEventPayload): Omit<AgentChatItem, "id"> | null {
    const item = event.item;
    if (!item || (event.type !== "item.started" && event.type !== "item.completed")) return null;
    const completed = event.type === "item.completed";
    const status = String(item.status || (completed ? "completed" : "inProgress"));
    const failed = Boolean(item.error?.message) || item.success === false || ["failed", "error"].includes(status);
    const itemStatus = failed ? "failed" : status;
    if (item.type === "reasoning") {
        const text = readableText(item.summary);
        if (completed && !text) return null;
        return { role: "tool", title: tr("reasoning"), text: text || activityPlaceholder(item.type), detail: { kind: "reasoning", status: itemStatus } };
    }
    if (item.type === "plan") {
        const text = stringText(item.text);
        if (completed && !text && !item.error?.message) return null;
        return { role: "tool", title: tr("plan"), text: item.error?.message || text || activityPlaceholder(item.type), detail: { kind: "plan", status: itemStatus, ...(item.error?.message ? { output: item.error.message } : {}) } };
    }
    if (item.type === "command_execution") {
        const command = stringText(item.command);
        const text = command || (completed ? tr(failed ? "commandFailed" : "commandCompleted") : activityPlaceholder(item.type));
        return { role: "tool", title: tr("executeCommand"), text, detail: commandActivityDetail(item, itemStatus) };
    }
    if (item.type === "file_change") {
        const files = activityFiles(item.changes);
        return { role: "tool", title: tr("editFiles"), text: item.error?.message || fileActivitySummary(files, completed), detail: { kind: "file", status: itemStatus, files, ...(item.error?.message ? { output: item.error.message } : {}) } };
    }
    if (item.type === "web_search") {
        return { role: "tool", title: tr("searchWeb"), text: item.error?.message || webSearchSummary(item), detail: { kind: "search", status: itemStatus, rows: webSearchDetailRows(item), ...(item.error?.message ? { output: item.error.message } : {}) } };
    }
    if (item.type === "image_view") return { role: "tool", title: tr("viewImage"), text: item.error?.message || stringText(item.path) || tr(completed ? "imageViewed" : "viewingImage"), detail: { kind: "image", status: itemStatus, ...(item.error?.message ? { output: item.error.message } : {}) } };
    if (item.type === "image_generation") {
        return { role: "tool", title: tr("imageGeneration"), text: item.error?.message || tr(completed ? failed ? "imageFailed" : "imageCompleted" : "generatingImage"), detail: { kind: "image", status: itemStatus, savedPath: item.savedPath, ...(item.error?.message ? { output: item.error.message } : {}) } };
    }
    if (item.type === "context_compaction") return { role: "tool", title: tr("compactContext"), text: item.error?.message || tr(completed ? "contextCompacted" : "compactingContext"), detail: { kind: "context", status: itemStatus, ...(item.error?.message ? { output: item.error.message } : {}) } };
    if (isMcpToolItem(item)) {
        const name = String(item.tool || "");
        return { role: "tool", title: toolName(name), text: completed ? item.error?.message || toolSummary(item) : tr("toolRunning", { action: toolAction(name) }), detail: toolDetail(item, itemStatus) };
    }
    if (item.type === "dynamic_tool_call") {
        const name = String(item.tool || "");
        const title = toolName(name);
        return {
            role: "tool",
            title,
            text: completed ? item.error?.message || readableText(item.contentItems) : tr("toolRunning", { action: toolAction(name) }),
            detail: toolDetail(item, itemStatus),
        };
    }
    if (item.type === "collab_tool_call") return { role: "tool", title: tr("collaboration"), text: item.error?.message || tr(completed ? failed ? "collaborationFailed" : "collaborationCompleted" : "collaborating"), detail: { kind: "tool", status: itemStatus, ...(item.error?.message ? { output: item.error.message } : {}) } };
    return null;
}

/** 把 plan.updated 事件格式化为「进度」卡片消息；计划为空时返回 null。 */
export function formatAgentPlan(event: AgentEventPayload): Omit<AgentChatItem, "id"> | null {
    const tasks = planTasks(event.plan);
    if (!tasks.length) return null;
    const completed = tasks.filter((item) => item.status === "completed").length;
    return {
        role: "tool",
        title: tr("progress"),
        text: tr("progressCount", { completed, total: tasks.length }),
        detail: { kind: "todo", status: completed === tasks.length ? "completed" : "inProgress", tasks, explanation: stringText(event.explanation) },
    };
}

/** 从事件负载中抽取计划任务列表（step + status），忽略无 step 的脏数据。 */
function planTasks(value: unknown) {
    return (Array.isArray(value) ? value : []).flatMap((item) => {
        const step = stringText(objectField(item, "step")).trim();
        return step ? [{ step, status: stringText(objectField(item, "status")) || "pending" }] : [];
    });
}

/**
 * 推导计划卡片的整体状态。
 * @param turnStatus 本轮 turn 的结束状态，failed / interrupted 优先于任务完成度。
 * @returns completed 表示全部任务完成，finished 表示 turn 正常结束但任务未全勾。
 */
export function turnPlanStatus(detail: unknown, turnStatus?: string) {
    const tasks = planTasks(objectField(detail, "tasks"));
    if (turnStatus === "failed") return "failed";
    if (turnStatus === "interrupted") return "interrupted";
    if (tasks.length && tasks.every((item) => item.status === "completed")) return "completed";
    return turnStatus === "completed" ? "finished" : "inProgress";
}

/** item.updated 首条 delta 到达时构造的「进行中」活动消息兜底，避免在流式增量前出现空卡片。 */
export function activityDeltaFallback(item: AgentEventItem, delta: string): AgentChatItem {
    if (item.type === "command_execution") return { id: item.id || randomId(), role: "tool", title: tr("executeCommand"), text: activityPlaceholder(item.type), detail: { kind: "command", status: "inProgress", output: delta } };
    return { id: item.id || randomId(), role: "tool", title: item.type === "plan" ? tr("plan") : tr("reasoning"), text: delta, detail: { kind: activityKind(item.type), status: "inProgress" } };
}

/** 各类活动进行中的占位文案（正在分析 / 正在执行命令 / 正在制定计划）。 */
export function activityPlaceholder(type?: string) {
    if (type === "plan") return tr("planning");
    if (type === "command_execution") return tr("executingCommand");
    return tr("analyzing");
}

/** item.type → 活动卡片 kind 的映射（command / plan / reasoning），用于详情区渲染样式。 */
export function activityKind(type?: string) {
    if (type === "command_execution") return "command";
    if (type === "plan") return "plan";
    return "reasoning";
}

/** 以已知字段为白名单重建详情对象，防止事件里的未知字段透传进 UI。 */
export function activityDetail(value: unknown, kind: string, status: string): AgentUserDetail {
    const current = value && typeof value === "object" ? (value as Partial<AgentUserDetail>) : {};
    return { kind, status, rows: current.rows, output: current.output, files: current.files, tasks: current.tasks, explanation: current.explanation };
}

function commandActivityDetail(item: AgentEventItem, status: string): AgentUserDetail {
    const rows = [detailRow(tr("workingDirectory"), item.cwd), detailRow(tr("exitStatus"), item.exitCode), durationDetailRow(item.durationMs)].flatMap((row) => (row ? [row] : []));
    const commandStatus = typeof item.exitCode === "number" && item.exitCode !== 0 ? "failed" : status;
    return { kind: "command", status: commandStatus, rows, output: item.error?.message || stringText(item.aggregatedOutput) };
}

function activityFiles(value: unknown) {
    return (Array.isArray(value) ? value : []).flatMap((change) => {
        const path = stringText(objectField(change, "path"));
        return path ? [{ path, action: changeAction(objectField(change, "kind")) }] : [];
    });
}

function fileActivitySummary(files: Array<{ path: string; action?: string }>, completed: boolean) {
    if (!files.length) return tr(completed ? "filesCompleted" : "preparingFiles");
    if (files.length === 1) return tr(completed ? "fileCompleted" : "editingFile", { action: files[0].action || tr("edit"), path: files[0].path });
    const names = files.slice(0, 3).map((file) => file.path).join(", ");
    return tr(completed ? "filesEdited" : "editingFiles", { count: files.length, names, more: files.length > 3 ? tr("andMore") : "" });
}

function webSearchSummary(item: AgentEventItem) {
    const action = item.action;
    const type = stringText(objectField(action, "type"));
    if (type === "openPage") return tr("openPage", { url: stringText(objectField(action, "url")) });
    if (type === "findInPage") return tr("findInPage", { pattern: stringText(objectField(action, "pattern")) || tr("content") });
    return tr("search", { query: stringText(item.query) || stringText(objectField(action, "query")) || tr("relatedInfo") });
}

function webSearchDetailRows(item: AgentEventItem) {
    const action = item.action;
    return [detailRow(tr("keyword"), item.query || objectField(action, "query")), detailRow(tr("webpage"), objectField(action, "url"))].flatMap((row) => (row ? [row] : []));
}

function readableText(value: unknown): string {
    if (typeof value === "string") return value.trim();
    if (Array.isArray(value)) return value.map(readableText).filter(Boolean).join("\n");
    if (!value || typeof value !== "object") return "";
    return readableText(objectField(value, "text"));
}

function detailRow(label: string, value: unknown) {
    return value === undefined || value === null || value === "" ? null : { label, value: String(value) };
}

function durationDetailRow(value: unknown) {
    const duration = Number(value || 0);
    return duration > 0 ? { label: tr("duration"), value: tr("seconds", { value: (duration / 1000).toFixed(1) }) } : null;
}

function changeAction(value: unknown) {
    if (value === "add") return tr("add");
    if (value === "delete") return tr("delete");
    return tr("edit");
}

/** 解析 SSE MessageEvent 的 data 字段为 JSON；解析失败返回 null 而不是抛错，避免打断事件流。 */
export function parseEventData<T>(event: Event) {
    try {
        return JSON.parse((event as MessageEvent).data) as T;
    } catch {
        return null;
    }
}

/** 判断事件是否属于当前激活线程；无 threadId 的事件一律视为不属于，防止串线程。 */
export function isCurrentThreadEvent(event: { threadId?: string; thread_id?: string }) {
    const threadId = event.threadId || event.thread_id || "";
    return Boolean(threadId) && threadId === useAgentStore.getState().activeThreadId;
}

/**
 * 消息归属与去重的关键闸门：决定一条（可能是历史回放的事件）是否要处理。
 * 已被服务端确认为权威历史（settled）的 turn，其 replayed 事件直接丢弃，避免与快照重复合并；
 * 其余事件把 turnId 记入 liveTurns，表示「该 turn 当前由实时流负责」。
 * @param authoritativeTurns 服务端已结算的 threadId\0turnId 集合
 * @param liveTurns 实时流正在产出的 turn 集合，随连接生命周期维护
 * @returns true 表示该事件需要继续处理
 */
export function registerLiveAgentTurn(
    event: { replayed?: boolean; threadId?: string; thread_id?: string; turnId?: string; turn_id?: string },
    authoritativeTurns: ReadonlySet<string>,
    liveTurns: Set<string>,
) {
    const threadId = event.threadId || event.thread_id || "";
    const turnId = event.turnId || event.turn_id || "";
    const key = threadId && turnId ? `${threadId}\0${turnId}` : "";
    if (event.replayed && key && authoritativeTurns.has(key)) return false;
    if (key) liveTurns.add(key);
    return true;
}

/** 把日志列表格式化为人读的诊断文本（头部连接信息 + 逐行日志），用于日志视图的文本模式与复制。 */
export function formatLogText(logs: AgentEventLog[], context: AgentLogContext) {
    const head = [
        tr("diagnostics"),
        tr("address", { endpoint: context.endpoint }),
        tr("connection", { connection: tr(context.connected ? "online" : context.enabled ? "connecting" : "disabled"), status: context.activity }),
        tr("messageTool", { messages: context.messages, tool: context.pendingTool ? toolName(context.pendingTool) : tr("none") }),
    ].join("\n");
    const body = logs.map((item) => `${item.time} ${item.title}${item.text && item.text !== item.title ? ` · ${item.text}` : ""}`).join("\n");
    return [head, body || tr("noLogs")].join("\n\n");
}

/** 日志的 JSON 导出格式：保留原始 raw 字段，供粘贴给他人排查问题时还原完整上下文。 */
export function formatLogJson(logs: AgentEventLog[], context: AgentLogContext) {
    return JSON.stringify({ context, logs: logs.map(({ time, title, text, raw }) => ({ time, title, text, raw })) }, null, 2);
}

/** 把核心事件（thread/turn/plan/item 级）折叠成一行事件日志；返回 null 表示该事件不进日志。 */
export function formatAgentEventLog(event: AgentEventPayload) {
    const item = event.item;
    if (event.type === "thread.started") return { title: tr("threadCreated"), text: shortId(event.thread_id) };
    if (event.type === "turn.started") return { title: tr("turnStarted"), text: shortId(event.turn_id) };
    if (event.type === "plan.updated") {
        const tasks = planTasks(event.plan);
        return { title: tr("progressUpdated"), text: tr("progressCount", { completed: tasks.filter((item) => item.status === "completed").length, total: tasks.length }) };
    }
    if (event.type === "turn.completed" && event.status === "failed") return { title: tr("turnFailed"), text: agentErrorView(event.error?.message).text };
    if (event.type === "turn.completed") return { title: tr(event.status === "interrupted" ? "turnStopped" : "turnCompleted"), text: turnSummary(event) };
    if (event.type === "turn.failed" || event.type === "error") return { title: tr("turnFailed"), text: agentErrorView(event.message || event.error?.message).text };
    if (event.type === "item.started" && isMcpToolItem(item)) return { title: tr("toolCalled"), text: toolName(String(item?.tool || "")) };
    if (event.type === "item.completed" && isMcpToolItem(item)) return { title: tr(item.error ? "toolFailed" : "toolCompleted"), text: `${toolName(String(item?.tool || ""))}${item.error?.message ? ` · ${item.error.message}` : ""}` };
    if (event.type === "item.completed" && item?.type === "agent_message") return { title: tr("replyReceived"), text: compactText(stringText(item.text)) };
    return null;
}

function turnSummary(event: AgentEventPayload) {
    return event.duration_ms ? tr("seconds", { value: (event.duration_ms / 1000).toFixed(1) }) : tr("completed");
}

/** 把任意错误值转成用户可读的「标题 + 描述」；对模型容量类错误给出专门文案。 */
export function agentErrorView(value: unknown) {
    const text = normalizeText(value);
    if (/selected model is at capacity/i.test(text)) return { title: tr("modelBusy"), text: tr("modelBusyDescription") };
    return { title: tr("taskFailed"), text: text || tr("taskFailedDescription") };
}

/** 从 usage.updated 事件提取 token 用量；缺失字段按 0 处理。 */
export function eventUsage(event: AgentEventPayload): AgentTokenUsage {
    return {
        input: numberField(event.usage, "input_tokens"),
        cached: numberField(event.usage, "cached_input_tokens"),
        output: numberField(event.usage, "output_tokens"),
    };
}

function shortId(value?: string) {
    return value ? value.slice(0, 8) : "";
}

/** 压缩为单行摘要文本，超长截断加省略号；用于日志与事件日志展示。 */
export function compactText(value: string, maxLength = 120) {
    const text = value.replace(/\s+/g, " ").trim();
    return text.length > maxLength ? `${text.slice(0, maxLength)}…` : text;
}

/** 识别「重连自动补的连接失败提示」这类可安全清除的本地错误消息，重连成功时统一移除。 */
export function isConnectionErrorMessage(item: AgentChatItem) {
    return item.role === "error" && /连接失败|无法连接本地 Agent|本地 Agent 连接失败/.test(item.text);
}

/** 内置/MCP 工具名 → 中文显示名；MCP 工具名可能带 `__server` 后缀，统一兜底匹配。 */
export function toolName(name: string) {
    if (name === "imagegen" || name.endsWith("__imagegen")) return tr("tools.generateImage");
    if (name === "view_image" || name.endsWith("__view_image")) return tr("tools.viewImage");
    if (name === "exec" || name === "exec_command" || name.endsWith("__exec_command")) return tr("tools.executeCommand");
    if (name === "apply_patch" || name.endsWith("__apply_patch")) return tr("tools.editFiles");
    if (name === "web__run" || name.endsWith("__web__run")) return tr("tools.searchWeb");
    if (toolTranslationKeys[name]) return tr(`tools.${toolTranslationKeys[name]}`);
    if (isSiteTool(name)) return SITE_TOOL_LABELS[name];
    return name ? tr("toolNamed", { name }) : tr("toolOperation");
}

// 画布与站点内置工具名 → i18n key 映射，key 对应 agent.events.tools.* 文案。
const toolTranslationKeys: Record<string, string> = {
    canvas_apply_ops: "canvasOps", canvas_get_state: "readCanvas", canvas_get_selection: "readSelection", canvas_export_snapshot: "exportSnapshot", canvas_create_node: "createNode", canvas_create_attachment_nodes: "addAttachments", canvas_create_text_node: "createText", canvas_create_text_nodes: "createTexts", canvas_create_config_node: "createConfig", canvas_create_image_prompt_flow: "createImageFlow", canvas_create_generation_flow: "createGenerationFlow", canvas_generate_text: "generateText", canvas_generate_image: "generateImage", canvas_generate_video: "generateVideo", canvas_generate_audio: "generateAudio", canvas_update_node: "updateNode", canvas_update_node_text: "updateText", canvas_move_nodes: "moveNodes", canvas_resize_node: "resizeNode", canvas_delete_nodes: "deleteNodes", canvas_connect_nodes: "connectNodes", canvas_select_nodes: "selectNodes", canvas_set_viewport: "setViewport", canvas_run_generation: "runGeneration", site_navigate: "openPage",
};

/** 站点工具执行结果的中文摘要（打开路由 / 各列表条数 / 生成状态等），未知工具返回空串。 */
function siteToolSummary(name: string, result: unknown, input: unknown) {
    const data = result && typeof result === "object" ? (result as Record<string, unknown>) : {};
    if (name === "site_navigate") return tr("openedRoute", { route: routeName(stringText(objectField(input, "path")) || "/") });
    if (name === "canvas_list_projects") return tr("canvasCount", { count: numberField(data, "total") });
    if (name === "prompts_search") return tr("promptCount", { count: numberField(data, "total") });
    if (name === "assets_list") return tr("assetCount", { count: numberField(data, "total") });
    if (name === "assets_add") return tr("assetAdded");
    if (name === "generation_get_status") {
        const summary = data.summary && typeof data.summary === "object" ? (data.summary as Record<string, unknown>) : {};
        return tr("generationStatus", { total: numberField(data, "total"), queued: numberField(summary, "queued"), running: numberField(summary, "running"), succeeded: numberField(summary, "succeeded"), failed: numberField(summary, "failed") });
    }
    if (name === "workbench_image_generate" || name === "workbench_video_generate") return typeof data.note === "string" ? data.note : tr("workbenchExecuted");
    if (name === "workbench_image_get_config" || name === "workbench_video_get_config") return tr("workbenchConfigRead");
    return "";
}

/** MCP 工具调用 item 的类型收窄工具。 */
function isMcpToolItem(item?: AgentEventItem): item is AgentEventItem & { type: "mcp_tool_call" } {
    return item?.type === "mcp_tool_call";
}

/** 工具事件 → 详情卡片：从 arguments 抽取人类可读的键值行，错误信息放进 output。 */
export function toolDetail(item: AgentEventItem | undefined, status: string): AgentUserDetail {
    const name = String(item?.tool || "");
    return { kind: "tool", status, rows: toolInputRows(name, item?.arguments), ...(item?.error?.message ? { output: item.error.message } : {}) };
}

/** 与 toolDetail 类似，但输入来自待确认工具调用（tool_call 事件）而非事件 item。 */
export function toolCallDetail(name: string, input: unknown, status: string, error = ""): AgentUserDetail {
    return { kind: "tool", status, rows: toolInputRows(name, input), ...(error ? { output: error } : {}) };
}

/** 按工具名挑选值得展示的输入参数行（目标页面 / 搜索内容 / 文本 / 画布操作摘要等）。 */
function toolInputRows(name: string, input: unknown) {
    input = parseToolArguments(input);
    if (name === "site_navigate") return [detailRow(tr("targetPage"), routeName(stringText(objectField(input, "path")) || "/"))].flatMap((row) => (row ? [row] : []));
    if (name === "prompts_search") return [detailRow(tr("searchContent"), objectField(input, "query"))].flatMap((row) => (row ? [row] : []));
    if (name === "canvas_create_text_node") return [detailRow(tr("textContent"), objectField(input, "text"))].flatMap((row) => (row ? [row] : []));
    if (name === "canvas_apply_ops") return [detailRow(tr("operationContent"), summarizeCanvasAgentOps((objectField(input, "ops") as CanvasAgentOp[] | undefined) || []))].flatMap((row) => (row ? [row] : []));
    if (name === "canvas_create_attachment_nodes") return [detailRow(tr("imageCount"), Array.isArray(objectField(input, "attachmentIds")) ? (objectField(input, "attachmentIds") as unknown[]).length : 0)].flatMap((row) => (row ? [row] : []));
    return [];
}

/** 工具完成事件的结果摘要（画布内容统计 / 选择读取等）；无法总结的工具返回空串。 */
export function toolSummary(item?: AgentEventItem) {
    const result = parseToolResult(item?.result);
    const name = String(item?.tool || "");
    if (name === "site_navigate" || isSiteTool(name)) return siteToolSummary(name, result, parseToolArguments(item?.arguments));
    const nodeField = objectField(result, "nodes");
    const connectionField = objectField(result, "connections");
    const nodes = Array.isArray(nodeField) ? nodeField : [];
    const connections = Array.isArray(connectionField) ? connectionField : [];
    if (name === "canvas_get_state") return Array.isArray(nodeField) || Array.isArray(connectionField) ? canvasContentSummary(nodes, connections.length) : tr("canvasRead");
    if (name === "canvas_get_selection") return tr("selectionRead");
    return "";
}

// 按节点类型汇总画布内容，用于 canvas_get_state 结果的一行摘要。
function canvasContentSummary(nodes: unknown[], connections: number) {
    const counts = nodes.reduce<Record<string, number>>((result, node) => {
        const type = stringText(objectField(node, "type")) || "other";
        result[type] = (result[type] || 0) + 1;
        return result;
    }, {});
    const known = new Set(["text", "image", "config", "video", "audio", "group"]);
    const other = Object.entries(counts).reduce((total, [type, count]) => total + (known.has(type) ? 0 : count), 0);
    const parts = [
        counts.text ? tr("textNodes", { count: counts.text }) : "",
        counts.image ? tr("imageNodes", { count: counts.image }) : "",
        counts.config ? tr("configNodes", { count: counts.config }) : "",
        counts.video ? tr("videoNodes", { count: counts.video }) : "",
        counts.audio ? tr("audioNodes", { count: counts.audio }) : "",
        counts.group ? tr("groupNodes", { count: counts.group }) : "",
        other ? tr("otherNodes", { count: other }) : "",
        connections ? tr("connections", { count: connections }) : "",
    ].filter(Boolean);
    return parts.length ? parts.join(i18n.language === "zh-CN" ? "、" : ", ") : tr("emptyCanvas");
}

/** 「正在执行 XX」的活动文案。 */
export function toolAction(name: string) {
    const label = toolName(name);
    return tr("executeTool", { tool: label });
}

/** 站内路径 → 中文名称；识别不了的路径原样返回。 */
export function routeName(path: string) {
    if (path === "/") return tr("routes.home");
    if (path === "/canvas") return tr("routes.canvas");
    if (path.startsWith("/canvas/")) return tr("routes.canvasProject");
    if (path.startsWith("/image")) return tr("routes.image");
    if (path.startsWith("/video")) return tr("routes.video");
    if (path.startsWith("/prompts")) return tr("routes.prompts");
    if (path.startsWith("/assets")) return tr("routes.assets");
    if (path.startsWith("/config")) return tr("routes.config");
    return path;
}

/**
 * 计算「Agent 正在忙什么」的状态文案。
 * @returns key 用于识别内容是否变化（驱动计时器重置），text 为展示文案。
 */
export function workingActivity(item?: AgentChatItem) {
    const status = String(objectField(item?.detail, "status") || "");
    const output = stringText(objectField(item?.detail, "output"));
    const key = `${item?.id || "waiting"}-${status}-${item?.text || ""}-${output.length}`;
    if (item?.role !== "tool") return { key, text: tr("thinking") };
    if (["inProgress", "in_progress", "running", "pending"].includes(status)) return { key, text: tr("operationRunning", { operation: item.title || tr("toolOperation") }) };
    if (item.title === toolName("canvas_get_state")) return { key, text: tr("organizingCanvas") };
    return { key, text: tr("operationCompleted", { operation: item.title || tr("toolOperation") }) };
}

/** 从后往前找最近一条计划消息，遇到用户消息即停止（计划只可能属于当前用户请求）。 */
export function currentPlanMessage(messages: AgentChatItem[]) {
    for (let index = messages.length - 1; index >= 0; index--) {
        const message = messages[index];
        if (isPlanMessage(message)) return message;
        if (message.role === "user") return;
    }
}

/** 从后往前找最近一条计划消息，不区分归属，用于空闲态展示最后一轮进度。 */
export function latestPlanMessage(messages: AgentChatItem[]) {
    for (let index = messages.length - 1; index >= 0; index--) {
        if (isPlanMessage(messages[index])) return messages[index];
    }
}

/** 计划消息的判别：tool 角色且 detail.kind === "todo"。 */
export function isPlanMessage(message: AgentChatItem) {
    return message.role === "tool" && objectField(message.detail, "kind") === "todo";
}

// MCP 工具结果常包一层 { content: [{ text: "...JSON..." }] }，这里先拼出内层文本再尝试 JSON 解析。
function parseToolResult(result: unknown) {
    const content = objectField(result, "content");
    const text = Array.isArray(content)
        ? content
              .map((item) => objectField(item, "text"))
              .filter((item): item is string => typeof item === "string")
              .join("\n")
        : "";
    try {
        return text ? JSON.parse(text) : result;
    } catch {
        return text || result;
    }
}

/** 把任意值转成可展示文本：字符串 trim、Error 取 message、其余 JSON 序列化。 */
export function normalizeText(value: unknown) {
    if (typeof value === "string") return value.trim();
    if (value instanceof Error) return value.message;
    if (value == null) return "";
    return JSON.stringify(value, null, 2);
}

/** 严格取字符串字段，非字符串一律返回空串（区别于 normalizeText 的兜底序列化）。 */
export function stringText(value: unknown) {
    return typeof value === "string" ? value : "";
}

/** 安全读取对象字段；非对象（含数组以外的原始值）返回 undefined。 */
export function objectField(value: unknown, key: string) {
    return value && typeof value === "object" ? (value as Record<string, unknown>)[key] : undefined;
}

function numberField(value: unknown, key: string) {
    const field = objectField(value, key);
    return typeof field === "number" ? field : 0;
}

/** 无文字时用附件生成一条兜底 prompt，避免发送空请求。 */
export function promptWithAttachments(text: string, attachments: AgentAttachment[]) {
    return text || (attachments.length ? tr("attachmentPrompt") : "");
}

/**
 * 把画布引用（@mention 的节点）展开成追加在 prompt 末尾的结构化清单，
 * 明确告知模型 nodeId 的用法并要求先 canvas_get_state 定位，禁止按标题猜节点。
 */
export function promptWithCanvasReferences(text: string, references: CanvasResourceReference[]) {
    if (!references.length) return text;
    const task = text || "请处理引用的画布素材。";
    const list = references.map((item, index) => `${index + 1}. mention=${JSON.stringify(`@${item.label}`)}, nodeId=${JSON.stringify(item.nodeId)}, title=${JSON.stringify(item.title)}, type=${item.kind}`).join("\n");
    const imageHint = references.some((item) => item.kind === "image") ? "\n其中图片素材的实际内容已作为本轮图片附件提供，可直接查看；nodeId 用于后续画布操作。" : "";
    return `${task}\n\n本轮引用的当前画布素材：\n${list}${imageHint}\n需要读取或操作这些素材时，先调用 canvas_get_state，并使用上面的 nodeId 精确定位，不要按标题猜测节点。`;
}

/** 附件总负载估算：直接累加 dataUrl 字符串长度（base64 体积的上界近似）。 */
export function attachmentPayloadBytes(attachments: AgentAttachment[]) {
    return attachments.reduce((total, item) => total + item.dataUrl.length, 0);
}

/** 字节数人性化展示：>1MB 显示一位小数 MB，否则向上取整 KB。 */
export function formatBytes(bytes: number) {
    return bytes > 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)}MB` : `${Math.ceil(bytes / 1024)}KB`;
}

/** 会改动画布内容、需要用户手动确认的画布工具；只读工具不在此列。 */
export function isCanvasWriteTool(name: string) {
    return name === "canvas_apply_ops" || name === "canvas_create_attachment_nodes";
}

// 工具调用参数可能是 JSON 字符串，解析失败按空对象处理，避免展示原始转义串。
function parseToolArguments(value: unknown) {
    if (typeof value !== "string") return value;
    try {
        return JSON.parse(value) as unknown;
    } catch {
        return {};
    }
}

/**
 * 消息唯一 ID：按 threadId/turnId/itemId 三级归属拼接。
 * 同一 itemId 在实时流与历史快照中会得到相同 ID，是 upsert 去重的依据。
 */
export function agentMessageId(threadId: string, turnId: string, itemId: string) {
    return `${threadId}:${turnId}:${itemId}`;
}

/**
 * 把消息归一到指定的 thread/turn 作用域并生成规范 ID。
 * turnId 为空时按 "pending" 归档（用户消息发送后、turn 开始前的暂存态）；
 * 用户消息固定使用 "synthetic:user" 作为 itemId，保证同一轮只保留一条本地用户消息。
 */
export function scopeChatItem(item: AgentChatItem, threadId: string, turnId: string) {
    const scopeTurnId = turnId || "pending";
    const prefix = `${threadId || "local"}:${scopeTurnId}:`;
    const sourceItemId = item.itemId || (item.id.startsWith(prefix) ? item.id.slice(prefix.length) : item.id);
    const itemId = item.role === "user" ? "synthetic:user" : sourceItemId;
    return { ...item, id: agentMessageId(threadId || "local", scopeTurnId, itemId), itemId, threadId, turnId };
}

/**
 * turn 开始时把最新的「待归档」用户消息绑定到真实 turnId 上，
 * 使其 ID 与后续服务端事件对齐，完成从 pending 到正式 turn 的迁移。
 */
export function bindPendingTurnMessages(messages: AgentChatItem[], threadId: string, turnId: string) {
    const index = messages.findLastIndex((item) => item.role === "user" && item.threadId === threadId && !item.turnId);
    if (index < 0) return messages;
    return messages.map((item, itemIndex) => itemIndex === index ? scopeChatItem(item, threadId, turnId) : item);
}

/** 按 ID upsert 一条消息：已存在则合并（保留旧字段并叠加新值），否则追加到末尾。 */
export function upsertAgentMessage(messages: AgentChatItem[], item: AgentChatItem) {
    const index = messages.findIndex((current) => current.id === item.id);
    if (index < 0) return [...messages, item];
    const current = messages[index];
    const next = { ...current, ...item, ...mergeLocalMessageMetadata(current, item) };
    return messages.map((message, itemIndex) => itemIndex === index ? next : message);
}

/**
 * 服务端历史快照（权威）与本地实时消息的合并。
 * 只合并同 thread 的本地消息：历史快照成为权威后，未在 liveTurns 中且无 turnId 的本地消息被丢弃，
 * 防止同一条消息被重复合并；live turn 的实时内容覆盖历史内容。
 */
export function mergeAgentMessages(snapshot: AgentChatItem[], current: AgentChatItem[], threadId: string, liveTurnKeys: ReadonlySet<string>) {
    let messages = [...snapshot];
    current.filter((item) => item.threadId === threadId).forEach((item) => {
        const live = Boolean(item.turnId && liveTurnKeys.has(`${threadId}\0${item.turnId}`));
        const index = messages.findIndex((message) => message.id === item.id);
        if (index < 0) {
            if (!item.turnId || live) messages = upsertAgentMessage(messages, item);
            return;
        }
        const history = messages[index];
        const metadata = mergeLocalMessageMetadata(history, item);
        const next = live ? { ...history, ...item, ...metadata } : { ...history, ...metadata };
        messages = messages.map((message, itemIndex) => itemIndex === index ? next : message);
    });
    return messages;
}

/**
 * 清洗历史快照消息：丢弃无内容或缺少 thread/turn/item 归属的条目，
 * 并统一 scope 成规范 ID，保证与实时消息可互相去重。
 */
export function normalizeHistoryMessages(messages: AgentChatItem[]) {
    return messages
        .filter((item) => (normalizeText(item.text) || item.role === "tool") && item.itemId && item.threadId && item.turnId)
        .map(({ streamId: _streamId, ...item }) => scopeChatItem({ ...item, text: normalizeText(item.text) } as AgentChatItem, item.threadId!, item.turnId!));
}

// 合并消息时保留本地独有的元数据（附件、画布引用、技能）：历史快照往往不含这些仅前端拥有的字段。
function mergeLocalMessageMetadata(current: AgentChatItem, incoming: AgentChatItem) {
    return {
        attachments: incoming.attachments || current.attachments,
        canvasReferences: incoming.canvasReferences || current.canvasReferences,
        skill: incoming.skill || current.skill,
    };
}

/** 汇总各 item 的推理摘要为一段文本；全部无效时回退到占位文案。 */
export function reasoningActivityText(items: Record<string, string>, fallback = "") {
    const summaries = Object.values(items).map((item) => item.trim()).filter(isReasoningSummary);
    return summaries.join("\n\n") || fallback || REASONING_PLACEHOLDER;
}

/** 判断是否为有效推理摘要：排除空串和「正在分析 / 已完成分析」这类占位文案。 */
export function isReasoningSummary(value = "") {
    const text = value.trim();
    return Boolean(text && text !== REASONING_PLACEHOLDER && text !== "已完成分析");
}

/**
 * 合并流式文本：delta 模式下服务端可能整段重发（前缀重叠），
 * 取两者中较长的那个即可正确续写，避免重复拼接或丢字。
 */
export function mergeStreamText(prefix: string, incoming: string) {
    if (!prefix || incoming.startsWith(prefix)) return incoming || prefix;
    if (!incoming || prefix.startsWith(incoming)) return prefix;
    return incoming.length >= prefix.length ? incoming : prefix;
}

/** agent.events.* → agent.eventExtra.* → agent.eventMore.* 三级兜底取翻译，避免新键缺失时显示 key 本身。 */
function tr(key: string, options?: Record<string, unknown>) {
    const path = `agent.events.${key}`;
    const extraPath = `agent.eventExtra.${key}`;
    return i18n.t(i18n.exists(path) ? path : i18n.exists(extraPath) ? extraPath : `agent.eventMore.${key}`, options);
}
