import { useEffect, useMemo, useRef, useState } from "react";
import { App, Button, Collapse, Dropdown, Form, Input, Modal, Select, Switch, Tooltip } from "antd";
import type { MenuProps } from "antd";
import { Check, ChevronDown, CircleAlert, FilePenLine, LoaderCircle, LockKeyhole, MessageSquareText, Plus, RefreshCw, Search, Sparkles, Trash2, Workflow } from "lucide-react";
import { useTranslation } from "react-i18next";

import { canvasThemes } from "@/lib/canvas-theme";
import { createCodexSkill, createCodexSkillDraft, deleteCodexSkill, fetchCodexSkill, postState, setCodexSkillEnabled, updateCodexSkill, type AgentSkillDetail, type AgentSkillDraft, type AgentSkillInterface, type AgentSkillScope, type AgentSkillSummary } from "@/services/api/canvas-agent";
import { useAgentSkillStore } from "@/stores/use-agent-skill-store";
import { useAgentStore, type AgentChatItem } from "@/stores/use-agent-store";
import { useThemeStore } from "@/stores/use-theme-store";

type ScopeFilter = "all" | AgentSkillScope;
// 技能草稿的来源：从当前会话内容归纳，或从画布内容归纳。
type SkillDraftSource = "conversation" | "canvas";
// 编辑器状态：新建（可带草稿初值）或编辑（携带服务端详情与乐观锁 revision）。
type SkillEditor = { mode: "create"; values?: SkillFormValues } | { mode: "edit"; detail: AgentSkillDetail };
type SkillFormValues = { name: string; description: string; instructions: string; displayName?: string; shortDescription?: string; defaultPrompt?: string };

// 技能标识只允许小写字母数字与中划线，中划线不能连续或出现在首尾。
const skillNamePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

/**
 * 技能管理页：技能列表（搜索/来源筛选/启停/选用）与新建/编辑/删除流程。
 * 新建支持从会话或画布让 Codex 生成草稿，也可手动空白创建；
 * 所有写操作都经 connectionIsCurrent 校验，防止请求返回时已切换连接导致状态错乱。
 */
export function AgentSkillsView({ clientId }: { clientId: string }) {
    const { t } = useTranslation();
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const { message, modal } = App.useApp();
    const connected = useAgentStore((state) => state.connected);
    const url = useAgentStore((state) => state.url);
    const token = useAgentStore((state) => state.token);
    const activeThreadId = useAgentStore((state) => state.activeThreadId);
    const hasConversation = useAgentStore((state) => hasSettledConversation(state.messages, state.activeThreadId));
    const hasCanvas = useAgentStore((state) => Boolean(state.canvasContext));
    const sending = useAgentStore((state) => state.sending);
    const waiting = useAgentStore((state) => state.waiting);
    const setAgentState = useAgentStore((state) => state.setAgentState);
    const skills = useAgentSkillStore((state) => state.skills);
    const selectedSkill = useAgentSkillStore((state) => state.selectedSkill);
    const loading = useAgentSkillStore((state) => state.loading);
    const loaded = useAgentSkillStore((state) => state.loaded);
    const errors = useAgentSkillStore((state) => state.errors);
    const draft = useAgentSkillStore((state) => state.draft);
    const generatingSource = useAgentSkillStore((state) => state.generatingSource);
    const loadSkills = useAgentSkillStore((state) => state.loadSkills);
    const selectSkill = useAgentSkillStore((state) => state.selectSkill);
    const clearSelection = useAgentSkillStore((state) => state.clearSelection);
    const setDraft = useAgentSkillStore((state) => state.setDraft);
    const setGeneratingSource = useAgentSkillStore((state) => state.setGeneratingSource);
    const [query, setQuery] = useState("");
    const [scope, setScope] = useState<ScopeFilter>("all");
    const [editor, setEditor] = useState<SkillEditor | null>(null);
    const [saving, setSaving] = useState(false);
    const [advancedOpen, setAdvancedOpen] = useState(false);
    const [createMenuOpen, setCreateMenuOpen] = useState(false);
    const [busySkill, setBusySkill] = useState("");
    const [errorsOpen, setErrorsOpen] = useState(false);
    // 持有删除确认弹窗实例：断线时需要主动 destroy，避免确认后作用于已失效的连接。
    const confirmRef = useRef<{ destroy: () => void } | null>(null);
    const [form] = Form.useForm<SkillFormValues>();
    const endpoint = url.trim().replace(/\/$/, "");
    const filteredSkills = useMemo(() => {
        const keyword = query.trim().toLowerCase();
        return skills.filter((skill) => {
            if (scope !== "all" && skill.scope !== scope) return false;
            return !keyword || [skill.name, skill.description, skill.interface?.displayName, skill.interface?.shortDescription, skill.shortDescription].some((value) => value?.toLowerCase().includes(keyword));
        });
    }, [query, scope, skills]);

    const editorValues = editor?.mode === "edit" ? skillFormValues(editor.detail) : editor?.values;

    const refresh = (forceReload = true) => loadSkills(endpoint, token, forceReload);
    // 异步结果的时效校验：connectionRevision 在每次重连后递增，配合当前地址/token 判断
    // 请求发出时的连接是否仍然有效，失效则丢弃结果且不弹提示。
    const connectionIsCurrent = (revision: number) => {
        const agent = useAgentStore.getState();
        const skillsState = useAgentSkillStore.getState();
        return skillsState.connectionRevision === revision && agent.connected && agent.url.trim().replace(/\/$/, "") === endpoint && agent.token === token;
    };
    useEffect(() => {
        // 服务端生成的技能草稿到达后自动打开新建弹窗；已打开时不覆盖用户正在编辑的内容。
        if (draft) setEditor((current) => current || { mode: "create", values: draftFormValues(draft) });
    }, [draft]);
    useEffect(() => {
        // 断线后关闭所有弹窗/确认框并复位表单，避免基于失效连接继续操作。
        if (connected) return;
        confirmRef.current?.destroy();
        confirmRef.current = null;
        setEditor(null);
        setSaving(false);
        setAdvancedOpen(false);
        setCreateMenuOpen(false);
        setBusySkill("");
        setErrorsOpen(false);
        form.resetFields();
    }, [connected, form]);
    /** 选用技能：写入技能 store 并切回聊天页，由输入框以 `/` mention 形式生效。 */
    const useSkill = (skill: AgentSkillSummary) => {
        selectSkill(skill);
        setAgentState({ activeTab: "chat" });
    };
    /** 让 Codex 从会话/画布生成技能草稿；画布来源需先同步一次画布快照。 */
    const generateDraft = async (source: SkillDraftSource) => {
        const agent = useAgentStore.getState();
        if (agent.sending || agent.waiting) return message.warning(t("agent.skillManager.codexBusy"));
        if (source === "conversation" && !hasSettledConversation(agent.messages, agent.activeThreadId)) return message.warning(t("agent.skillManager.noConversation"));
        if (source === "canvas" && !agent.canvasContext) return message.warning(t("agent.skillManager.noCanvas"));
        if (!clientId) return message.warning(t("agent.skillManager.connecting"));
        const connectionRevision = useAgentSkillStore.getState().connectionRevision;
        setGeneratingSource(source);
        try {
            // 画布草稿依赖最新快照：生成前强制上报一次画布状态，失败直接终止。
            if (source === "canvas") {
                const synced = await postState(endpoint, token, clientId, agent.canvasContext?.snapshot || null);
                if (!synced) throw new Error(t("agent.skillManager.syncFailed"));
            }
            if (!connectionIsCurrent(connectionRevision)) return;
            const response = await createCodexSkillDraft(endpoint, token, {
                source,
                threadId: agent.activeThreadId,
                clientId,
                ...(agent.model ? { model: agent.model } : {}),
                ...(agent.reasoningEffort ? { effort: agent.reasoningEffort } : {}),
            });
            if (!connectionIsCurrent(connectionRevision)) return;
            if (!response.data) throw new Error(t("agent.skillManager.noDraft"));
            setDraft(response.data);
            message.success(t("agent.skillManager.draftCreated"));
        } catch (error) {
            if (connectionIsCurrent(connectionRevision)) message.error(error instanceof Error ? error.message : t("agent.skillManager.draftFailed"));
        } finally {
            if (connectionIsCurrent(connectionRevision)) setGeneratingSource(null);
        }
    };
    /** 打开编辑弹窗：先拉取服务端技能详情（含乐观锁 revision）；外部技能只读不可编辑。 */
    const openEdit = async (skill: AgentSkillSummary) => {
        if (!skill.managed || busySkill || useAgentSkillStore.getState().generatingSource) return;
        const connectionRevision = useAgentSkillStore.getState().connectionRevision;
        setBusySkill(skill.path);
        try {
            const response = await fetchCodexSkill(endpoint, token, skill.name);
            if (!connectionIsCurrent(connectionRevision)) return;
            if (!response.data) throw new Error(t("agent.skillManager.contentMissing"));
            setEditor({ mode: "edit", detail: response.data });
        } catch (error) {
            if (connectionIsCurrent(connectionRevision)) message.error(error instanceof Error ? error.message : t("agent.skillManager.readFailed"));
        } finally {
            if (connectionIsCurrent(connectionRevision)) setBusySkill("");
        }
    };
    /** 保存技能（新建或更新）：校验表单、确保 defaultPrompt 引用了技能本身，成功后关闭弹窗并刷新列表。 */
    const saveSkill = async () => {
        if (!editor) return;
        let values: SkillFormValues;
        try {
            values = await form.validateFields();
        } catch {
            // 校验失败时展开高级折叠区（错误可能藏在里面）并滚动到第一个出错字段。
            const firstError = form.getFieldsError().find((field) => field.errors.length);
            if (firstError?.name.some((name) => name === "shortDescription" || name === "defaultPrompt")) setAdvancedOpen(true);
            if (firstError) requestAnimationFrame(() => form.scrollToField(firstError.name, { block: "center" }));
            return;
        }
        const name = editor.mode === "edit" ? editor.detail.name : values.name.trim();
        const skillInterface = compactInterface(values);
        // defaultPrompt 会被原样发给模型，必须包含 $技能名 引用，否则模型不知道这是技能入口。
        if (skillInterface?.defaultPrompt && !mentionsSkill(skillInterface.defaultPrompt, name)) {
            form.setFields([{ name: "defaultPrompt", errors: [t("agent.skillManager.defaultPromptMention", { name })] }]);
            setAdvancedOpen(true);
            requestAnimationFrame(() => form.scrollToField("defaultPrompt", { block: "center" }));
            return;
        }
        const connectionRevision = useAgentSkillStore.getState().connectionRevision;
        if (!connectionIsCurrent(connectionRevision)) return;
        setSaving(true);
        try {
            const input = { description: values.description.trim(), instructions: values.instructions.trim(), interface: skillInterface || null };
            if (editor.mode === "create") await createCodexSkill(endpoint, token, { name, ...input });
            // 更新带 expectedRevision 乐观锁：服务端内容已被他端修改时请求会失败。
            else await updateCodexSkill(endpoint, token, name, { ...input, expectedRevision: editor.detail.revision });
            if (!connectionIsCurrent(connectionRevision)) return;
            setDraft(null);
            setEditor(null);
            setAdvancedOpen(false);
            await refresh();
            if (!connectionIsCurrent(connectionRevision)) return;
            message.success(t(editor.mode === "create" ? "agent.skillManager.created" : "agent.skillManager.updated"));
        } catch (error) {
            if (connectionIsCurrent(connectionRevision)) message.error(error instanceof Error ? error.message : t("agent.skillManager.saveFailed"));
        } finally {
            if (connectionIsCurrent(connectionRevision)) setSaving(false);
        }
    };
    /** 删除技能：先拉详情拿最新 revision 再删除（乐观锁），删除的是当前选用技能时清空选用。 */
    const confirmDelete = (skill: AgentSkillSummary) => {
        const connectionRevision = useAgentSkillStore.getState().connectionRevision;
        confirmRef.current = modal.confirm({
            title: t("agent.skillManager.deleteTitle", { name: skill.interface?.displayName || skill.name }),
            content: t("agent.skillManager.deleteDescription"),
            okText: t("agent.skillManager.delete"),
            okType: "danger",
            cancelText: t("common.cancel"),
            onOk: async () => {
                if (!connectionIsCurrent(connectionRevision)) return;
                setBusySkill(skill.path);
                try {
                    const response = await fetchCodexSkill(endpoint, token, skill.name);
                    if (!connectionIsCurrent(connectionRevision)) return;
                    if (!response.data) throw new Error(t("agent.skillManager.contentMissing"));
                    await deleteCodexSkill(endpoint, token, skill.name, response.data.revision);
                    if (!connectionIsCurrent(connectionRevision)) return;
                    if (selectedSkill?.name === skill.name && selectedSkill.path === skill.path) clearSelection();
                    await refresh();
                    if (!connectionIsCurrent(connectionRevision)) return;
                    message.success(t("agent.skillManager.deleted"));
                } catch (error) {
                    if (!connectionIsCurrent(connectionRevision)) return;
                    message.error(error instanceof Error ? error.message : t("agent.skillManager.deleteFailed"));
                    throw error;
                } finally {
                    if (connectionIsCurrent(connectionRevision)) setBusySkill("");
                }
            },
            afterClose: () => {
                confirmRef.current = null;
            },
        });
    };
    /** 启停技能：停用当前选用技能时同步清空选用，避免聊天里引用已停用的技能。 */
    const toggleEnabled = async (skill: AgentSkillSummary, enabled: boolean) => {
        const connectionRevision = useAgentSkillStore.getState().connectionRevision;
        if (!connectionIsCurrent(connectionRevision)) return;
        setBusySkill(skill.path);
        try {
            await setCodexSkillEnabled(endpoint, token, skill, enabled);
            if (!connectionIsCurrent(connectionRevision)) return;
            if (!enabled && selectedSkill?.name === skill.name && selectedSkill.path === skill.path) clearSelection();
            await refresh();
        } catch (error) {
            if (connectionIsCurrent(connectionRevision)) message.error(error instanceof Error ? error.message : t("agent.skillManager.statusFailed"));
        } finally {
            if (connectionIsCurrent(connectionRevision)) setBusySkill("");
        }
    };
    const codexBusy = sending || waiting;
    // 「新建技能」菜单：会话/画布生成项的禁用态与说明文案随当前状态动态切换。
    const createMenu: MenuProps = {
        items: [
            {
                key: "conversation",
                icon: <MessageSquareText className="size-4" />,
                disabled: codexBusy || !hasConversation,
                label: (
                    <div className="py-0.5">
                        <div className="text-sm">{t("agent.skillManager.fromConversation")}</div>
                        <div className="mt-0.5 text-xs" style={{ color: theme.node.muted }}>{t(codexBusy ? "agent.skillManager.availableAfterRun" : hasConversation ? "agent.skillManager.conversationDescription" : activeThreadId ? "agent.skillManager.noCompletedContent" : "agent.skillManager.startConversation")}</div>
                    </div>
                ),
            },
            {
                key: "canvas",
                icon: <Workflow className="size-4" />,
                disabled: codexBusy || !hasCanvas,
                label: (
                    <div className="py-0.5">
                        <div className="text-sm">{t("agent.skillManager.fromCanvas")}</div>
                        <div className="mt-0.5 text-xs" style={{ color: theme.node.muted }}>{t(codexBusy ? "agent.skillManager.availableAfterRun" : hasCanvas ? "agent.skillManager.canvasDescription" : "agent.skillManager.canvasUnavailable")}</div>
                    </div>
                ),
            },
            { type: "divider" as const },
            {
                key: "manual",
                icon: <FilePenLine className="size-4" />,
                label: (
                    <div className="py-0.5">
                        <div className="text-sm">{t("agent.skillManager.blankCreate")}</div>
                        <div className="mt-0.5 text-xs" style={{ color: theme.node.muted }}>{t("agent.skillManager.blankDescription")}</div>
                    </div>
                ),
            },
        ],
        onClick: ({ key }) => {
            if (key === "manual") {
                setDraft(null);
                setEditor({ mode: "create" });
            }
            else void generateDraft(key as SkillDraftSource);
        },
    };

    return (
        <div className="flex min-h-0 flex-1 flex-col">
            <div className="shrink-0 border-b px-4 py-3" style={{ borderColor: theme.node.stroke }}>
                <div className="flex items-center justify-between gap-3">
                    <div>
                        <div className="text-sm font-semibold">{t("agent.skillManager.localSkills")}</div>
                        <div className="mt-0.5 text-xs" style={{ color: theme.node.muted }}>{t("agent.skillManager.localDescription")}</div>
                    </div>
                    <div className="flex items-center gap-1">
                        <Tooltip title={t("agent.skillManager.reload")}>
                            <Button type="text" shape="circle" className="!h-8 !w-8 !min-w-8" aria-label={t("agent.skillManager.reloadSkill")} disabled={!connected || loading} icon={<RefreshCw className={`size-4 ${loading ? "animate-spin" : ""}`} />} onClick={() => void refresh()} />
                        </Tooltip>
                        <Dropdown trigger={["click"]} placement="bottomRight" open={createMenuOpen} onOpenChange={setCreateMenuOpen} disabled={!connected || !clientId || Boolean(generatingSource)} menu={createMenu}>
                            <Button type="text" className="!h-8 !px-2" aria-haspopup="menu" aria-expanded={createMenuOpen} disabled={!connected || !clientId} loading={Boolean(generatingSource)} icon={<Plus className="size-4" />}>
                                {t("agent.skillManager.createSkill")} <ChevronDown className="size-3.5 opacity-60" />
                            </Button>
                        </Dropdown>
                    </div>
                </div>
                <div className="mt-3 flex gap-2">
                    <Input aria-label={t("agent.skills.search")} className="min-w-0 flex-1" allowClear disabled={!connected} value={query} onChange={(event) => setQuery(event.target.value)} prefix={<Search className="size-3.5" />} placeholder={t("agent.skills.search")} />
                    <Select<ScopeFilter>
                        size="small"
                        variant="borderless"
                        aria-label={t("agent.skillManager.filterBySource")}
                        className="w-28 shrink-0"
                        disabled={!connected}
                        value={scope}
                        onChange={setScope}
                        options={[{ value: "all", label: t("agent.skillManager.scopes.all") }, ...(["repo", "user", "system", "admin"] as AgentSkillScope[]).map((value) => ({ value, label: t(`agent.skillManager.scopes.${value}`) }))]}
                    />
                </div>
                {errors.length ? (
                    <Button danger type="text" size="small" className="!mt-1 !h-7 !px-1 text-xs" icon={<CircleAlert className="size-3.5" />} onClick={() => setErrorsOpen(true)}>{t("agent.skillManager.loadErrors", { count: errors.length })}</Button>
                ) : null}
            </div>
            <div className="thin-scrollbar min-h-0 flex-1 overflow-y-auto px-4">
                {loading && !loaded ? (
                    <div className="flex h-40 items-center justify-center gap-2 text-sm" style={{ color: theme.node.muted }}><LoaderCircle className="size-4 animate-spin" />{t("agent.skills.loading")}</div>
                ) : filteredSkills.length ? (
                    <div className="divide-y" style={{ borderColor: theme.node.stroke }}>
                        {filteredSkills.map((skill) => {
                            const selected = selectedSkill?.name === skill.name && selectedSkill.path === skill.path;
                            const busy = busySkill === skill.path;
                            return (
                                <div key={`${skill.name}:${skill.path}`} className={`py-3 transition-opacity ${skill.enabled ? "" : "opacity-55"}`} style={{ borderColor: theme.node.stroke }}>
                                    <div className="flex items-start gap-3">
                                        <Sparkles className="mt-0.5 size-4 shrink-0" style={{ color: selected ? theme.node.text : theme.node.muted }} />
                                        <div className="min-w-0 flex-1">
                                            <div className="flex min-w-0 items-center gap-2">
                                                <span className="truncate text-sm font-medium">{skill.interface?.displayName || skill.name}</span>
                                                {!skill.managed ? <Tooltip title={t("agent.skillManager.externalReadonly")}><LockKeyhole className="size-3.5 shrink-0" style={{ color: theme.node.faint }} /></Tooltip> : null}
                                            </div>
                                            <div className="mt-1 line-clamp-2 text-xs leading-5" style={{ color: theme.node.muted }}>{skill.interface?.shortDescription || skill.shortDescription || skill.description || t("agent.skillManager.noDescription")}</div>
                                            <Tooltip title={skill.path}>
                                                <div className="mt-1.5 truncate text-[11px]" style={{ color: theme.node.faint }}>{t(`agent.skillManager.scopes.${skill.scope}`)} · {skill.name}</div>
                                            </Tooltip>
                                        </div>
                                    </div>
                                    <div className="mt-2 flex items-center justify-between gap-2 pl-7">
                                        <label className="inline-flex items-center gap-2 text-xs" style={{ color: theme.node.muted }}>
                                            <Switch size="small" checked={skill.enabled} loading={busy} disabled={!connected || Boolean(busySkill) || Boolean(generatingSource)} onChange={(enabled) => void toggleEnabled(skill, enabled)} />
                                            {t(skill.enabled ? "agent.skillManager.enabled" : "agent.skillManager.disabled")}
                                        </label>
                                        <div className="flex items-center gap-0.5">
                                            <Button type="text" size="small" disabled={!connected || !skill.enabled || Boolean(busySkill)} icon={selected ? <Check className="size-3.5" /> : <Sparkles className="size-3.5" />} onClick={() => useSkill(skill)}>{t(selected ? "agent.skillManager.selected" : "agent.skillManager.use")}</Button>
                                            {skill.managed ? (
                                                <>
                                                    <Tooltip title={t("common.edit")}><Button type="text" shape="circle" size="small" aria-label={t("agent.skillManager.editNamed", { name: skill.interface?.displayName || skill.name })} disabled={!connected || Boolean(busySkill) || Boolean(generatingSource)} icon={<FilePenLine className="size-3.5" />} onClick={() => void openEdit(skill)} /></Tooltip>
                                                    <Tooltip title={t("common.delete")}><Button danger type="text" shape="circle" size="small" aria-label={t("agent.skillManager.deleteNamed", { name: skill.interface?.displayName || skill.name })} disabled={!connected || Boolean(busySkill) || Boolean(generatingSource)} icon={<Trash2 className="size-3.5" />} onClick={() => confirmDelete(skill)} /></Tooltip>
                                                </>
                                            ) : null}
                                        </div>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                ) : (
                    <div className="flex h-48 flex-col items-center justify-center text-center">
                        <Sparkles className="size-5" style={{ color: theme.node.faint }} />
                        <div className="mt-3 text-sm font-medium">{t(!connected ? "agent.skillManager.connectToView" : skills.length ? "agent.skillManager.noMatch" : "agent.skillManager.none")}</div>
                        <div className="mt-1 text-xs" style={{ color: theme.node.muted }}>{t(!connected ? "agent.skillManager.connectDescription" : skills.length ? "agent.skillManager.tryAnotherFilter" : "agent.skillManager.createOrInstall")}</div>
                    </div>
                )}
            </div>

            <Modal title={t("agent.skillManager.loadErrors", { count: errors.length })} open={errorsOpen} footer={null} width={720} onCancel={() => setErrorsOpen(false)}>
                <div className="thin-scrollbar mt-4 max-h-[60vh] overflow-y-auto rounded-md border px-3 py-2 text-xs leading-5" style={{ borderColor: theme.node.stroke }}>
                    {errors.map((error, index) => <div key={`${index}:${error}`} className="break-all py-1" style={{ color: theme.node.muted }}>{error}</div>)}
                </div>
            </Modal>

            <Modal
                title={editor?.mode === "edit" ? t("agent.skillManager.editNamed", { name: editor.detail.interface?.displayName || editor.detail.name }) : t("agent.skillManager.createSkill")}
                open={Boolean(editor)}
                okText={t(editor?.mode === "edit" ? "agent.skillManager.saveChanges" : "agent.skillManager.createSkill")}
                cancelText={t("common.cancel")}
                confirmLoading={saving}
                width={680}
                centered
                destroyOnHidden
                styles={{ body: { maxHeight: "calc(100vh - 220px)", overflowY: "auto" } }}
                onCancel={() => {
                    if (saving) return;
                    setDraft(null);
                    setEditor(null);
                    setAdvancedOpen(false);
                }}
                onOk={() => void saveSkill()}
            >
                <div className="mb-5 text-xs" style={{ color: theme.node.muted }}>{t("agent.skillManager.saveLocation")} · <span className="font-mono">.agents/skills</span></div>
                <Form key={editor?.mode === "edit" ? editor.detail.revision : `create:${editor?.values?.name || "blank"}`} form={form} initialValues={editorValues} layout="vertical" requiredMark="optional" preserve={false}>
                    <div className="mb-3 text-xs font-medium" style={{ color: theme.node.muted }}>{t("agent.skillManager.basicInfo")}</div>
                    <div className="grid grid-cols-1 gap-x-4 sm:grid-cols-2">
                        <Form.Item name="name" label={t("agent.skillManager.identifier")} extra={t("agent.skillManager.identifierExtra")} rules={[{ required: true, message: t("agent.skillManager.identifierRequired") }, { max: 64, message: t("agent.skillManager.identifierMax") }, { pattern: skillNamePattern, message: t("agent.skillManager.identifierPattern") }]}>
                            <Input maxLength={64} disabled={editor?.mode === "edit"} placeholder={t("agent.skillManager.identifierPlaceholder")} />
                        </Form.Item>
                        <Form.Item name="displayName" label={t("agent.skillManager.displayName")} rules={[{ max: 64, message: t("agent.skillManager.displayNameMax") }]}><Input maxLength={64} placeholder={t("agent.skillManager.displayNamePlaceholder")} /></Form.Item>
                    </div>
                    <Form.Item name="description" label={t("agent.skillManager.whenToUse")} extra={t("agent.skillManager.whenToUseExtra")} rules={[{ required: true, message: t("agent.skillManager.whenToUseRequired") }, { max: 1024, message: t("agent.skillManager.whenToUseMax") }, { validator: (_, value) => typeof value === "string" && /[<>]/.test(value) ? Promise.reject(new Error(t("agent.skillManager.noAngleBrackets"))) : Promise.resolve() }]}><Input.TextArea maxLength={1024} autoSize={{ minRows: 2, maxRows: 4 }} placeholder={t("agent.skillManager.whenToUsePlaceholder")} /></Form.Item>
                    <Form.Item name="instructions" label={t("agent.skillManager.instructions")} extra={t("agent.skillManager.instructionsExtra")} rules={[{ required: true, message: t("agent.skillManager.instructionsRequired") }]}><Input.TextArea className="!leading-6" autoSize={{ minRows: 6, maxRows: 10 }} placeholder={t("agent.skillManager.instructionsPlaceholder")} /></Form.Item>
                    <Collapse
                        ghost
                        size="small"
                        activeKey={advancedOpen ? ["advanced"] : []}
                        expandIconPlacement="end"
                        onChange={(keys) => setAdvancedOpen((Array.isArray(keys) ? keys : [keys]).includes("advanced"))}
                        items={[{
                            key: "advanced",
                            forceRender: true,
                            label: <span className="text-sm font-medium">{t("agent.skillManager.advanced")}</span>,
                            children: (
                                <>
                                    <Form.Item name="shortDescription" label={t("agent.skillManager.shortDescription")} extra={t("agent.skillManager.shortDescriptionExtra")} rules={[{ min: 25, message: t("agent.skillManager.shortDescriptionMin") }, { max: 64, message: t("agent.skillManager.shortDescriptionMax") }]}><Input maxLength={64} showCount placeholder={t("agent.skillManager.shortDescriptionPlaceholder")} /></Form.Item>
                                    <Form.Item name="defaultPrompt" label={t("agent.skillManager.defaultPrompt")} extra={t("agent.skillManager.defaultPromptExtra")} rules={[{ max: 1024, message: t("agent.skillManager.defaultPromptMax") }]}><Input.TextArea maxLength={1024} autoSize={{ minRows: 2, maxRows: 4 }} placeholder={t("agent.skillManager.defaultPromptPlaceholder")} /></Form.Item>
                                </>
                            ),
                        }]}
                    />
                </Form>
            </Modal>
        </div>
    );
}

/** 服务端技能详情 → 表单初值（interface 可选字段缺省为 undefined）。 */
function skillFormValues(detail: AgentSkillDetail): SkillFormValues {
    return {
        name: detail.name,
        description: detail.description,
        instructions: detail.instructions,
        displayName: detail.interface?.displayName || undefined,
        shortDescription: detail.interface?.shortDescription || undefined,
        defaultPrompt: detail.interface?.defaultPrompt || undefined,
    };
}

/** Codex 生成的技能草稿 → 新建表单初值。 */
function draftFormValues(draft: AgentSkillDraft): SkillFormValues {
    return {
        name: draft.name,
        description: draft.description,
        instructions: draft.instructions,
        displayName: draft.displayName || undefined,
        shortDescription: draft.shortDescription || undefined,
        defaultPrompt: draft.defaultPrompt || undefined,
    };
}

/** 判断当前线程是否存在已归属到 turn 的用户消息（即至少完成过一轮对话，可供草稿生成取材）。 */
function hasSettledConversation(messages: AgentChatItem[], threadId: string) {
    return Boolean(threadId && messages.some((item) => item.role === "user" && item.threadId === threadId && item.turnId));
}

/** 汇总表单里的可选展示字段；三者全空返回 undefined，服务端按无 interface 处理。 */
function compactInterface(values: SkillFormValues): AgentSkillInterface | undefined {
    const skillInterface = {
        displayName: values.displayName?.trim() || undefined,
        shortDescription: values.shortDescription?.trim() || undefined,
        defaultPrompt: values.defaultPrompt?.trim() || undefined,
    };
    return Object.values(skillInterface).some(Boolean) ? skillInterface : undefined;
}

/** defaultPrompt 是否包含对技能的引用（$name 后不能紧跟标识符字符或 :路径，防止误匹配同类名前缀）。 */
function mentionsSkill(prompt: string, name: string) {
    return new RegExp(`\\$${name}(?![A-Za-z0-9_-]|:[A-Za-z0-9_-])`).test(prompt);
}
