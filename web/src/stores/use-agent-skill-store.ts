import { create } from "zustand";
import i18n from "@/i18n";

import { fetchCodexSkills, type AgentSkillDraft, type AgentSkillSummary } from "@/services/api/canvas-agent";
import { useAgentStore } from "@/stores/use-agent-store";

let loadSequence = 0;

type AgentSkillStore = {
    skills: AgentSkillSummary[];
    selectedSkill: AgentSkillSummary | null;
    // 选中态版本号：跨异步操作判断「这次清理是否还作数」的乐观锁。
    selectionRevision: number;
    connectionRevision: number;
    // 由技能自动填入的提示词；用于区分「用户自己写的」与「技能带进来的」输入。
    autoPrompt: string;
    draft: AgentSkillDraft | null;
    generatingSource: "conversation" | "canvas" | null;
    loading: boolean;
    loaded: boolean;
    errors: string[];
    loadSkills: (endpoint: string, token: string, forceReload?: boolean) => Promise<void>;
    selectSkill: (skill: AgentSkillSummary | null, prompt?: string) => void;
    clearSelection: (expectedRevision?: number) => void;
    setDraft: (draft: AgentSkillDraft | null) => void;
    setGeneratingSource: (source: "conversation" | "canvas" | null) => void;
    reset: () => void;
};

/**
 * Agent 技能面板 store：管技能列表、选中态、草稿与生成来源，
 * Agent 面板的技能页读写；选中态与 Agent 输入框提示词双向联动。
 * 不持久化；loadSequence 用于丢弃过期请求的响应。
 */
export const useAgentSkillStore = create<AgentSkillStore>((set, get) => ({
    skills: [],
    selectedSkill: null,
    selectionRevision: 0,
    connectionRevision: 0,
    autoPrompt: "",
    draft: null,
    generatingSource: null,
    loading: false,
    loaded: false,
    errors: [],
    loadSkills: async (endpoint, token, forceReload = false) => {
        // 递增序号标记本次请求；响应回来时序号已变说明有更新的请求，直接丢弃。
        const sequence = ++loadSequence;
        set({ loading: true });
        try {
            const response = await fetchCodexSkills(endpoint, token, forceReload);
            if (sequence !== loadSequence) return;
            const skills = response.data || [];
            const current = get();
            const selected = current.selectedSkill;
            const selectedSkill = selected ? skills.find((item) => item.name === selected.name && item.path === selected.path && item.enabled) || null : null;
            const selectionChanged = Boolean(selected && !selectedSkill);
            if (selectionChanged) clearSelectedSkillPrompt(current);
            const autoPrompt = selectedSkill
                ? replaceAutoPrompt(current.autoPrompt, selectedSkill.interface?.defaultPrompt?.trim() || `$${selectedSkill.name}`, false)
                : "";
            set({ skills, selectedSkill, autoPrompt, selectionRevision: current.selectionRevision + (selectionChanged ? 1 : 0), loading: false, loaded: true, errors: (response.errors || []).map(skillErrorText) });
        } catch (error) {
            if (sequence !== loadSequence) return;
            set({ loading: false, loaded: true, errors: [error instanceof Error ? error.message : i18n.t("agent.state.skillReadFailed")] });
        }
    },
    selectSkill: (selectedSkill, prompt) => {
        const current = get();
        const agent = useAgentStore.getState();
        const marker = selectedSkill ? `$${selectedSkill.name}` : "";
        const previousMarker = current.selectedSkill ? `$${current.selectedSkill.name}` : "";
        let nextPrompt = prompt;
        let autoPrompt = "";
        if (nextPrompt === undefined && selectedSkill) {
            const defaultPrompt = selectedSkill.interface?.defaultPrompt?.trim() || marker;
            if (!agent.prompt.trim() || Boolean(current.autoPrompt && agent.prompt === current.autoPrompt)) {
                nextPrompt = defaultPrompt;
                autoPrompt = defaultPrompt;
            } else if (previousMarker && agent.prompt.includes(previousMarker)) {
                nextPrompt = agent.prompt.replace(previousMarker, marker);
            } else {
                nextPrompt = agent.prompt.includes(marker) ? agent.prompt : `${agent.prompt}${/\s$/.test(agent.prompt) ? "" : " "}${marker}`;
            }
        }
        if (nextPrompt !== undefined && agent.prompt !== nextPrompt) agent.setAgentState({ prompt: nextPrompt });
        set({ selectedSkill, autoPrompt, selectionRevision: current.selectionRevision + 1 });
    },
    clearSelection: (expectedRevision) => {
        const current = get();
        if (expectedRevision !== undefined && expectedRevision !== current.selectionRevision) return;
        clearSelectedSkillPrompt(current);
        set({ selectedSkill: null, autoPrompt: "", selectionRevision: current.selectionRevision + 1 });
    },
    setDraft: (draft) => set({ draft }),
    setGeneratingSource: (generatingSource) => set({ generatingSource }),
    reset: () => {
        loadSequence += 1;
        const current = get();
        clearSelectedSkillPrompt(current);
        set({ skills: [], selectedSkill: null, selectionRevision: current.selectionRevision + 1, connectionRevision: current.connectionRevision + 1, autoPrompt: "", draft: null, generatingSource: null, loading: false, loaded: false, errors: [] });
    },
}));

// 只替换「确实由技能填入」的提示词：用户已改过输入时（prompt ≠ autoPrompt）不覆盖，
// fillEmpty 允许在输入框为空时填入默认提示。
function replaceAutoPrompt(previous: string, next: string, fillEmpty: boolean) {
    const agent = useAgentStore.getState();
    const ownsPrompt = Boolean(previous && agent.prompt === previous);
    if (!ownsPrompt && !(fillEmpty && !agent.prompt.trim())) return "";
    if (agent.prompt !== next) agent.setAgentState({ prompt: next });
    return next;
}

function clearSelectedSkillPrompt(selection: Pick<AgentSkillStore, "selectedSkill" | "autoPrompt">) {
    const agent = useAgentStore.getState();
    if (selection.autoPrompt && agent.prompt === selection.autoPrompt) {
        agent.setAgentState({ prompt: "" });
        return;
    }
    if (!selection.selectedSkill) return;
    const marker = `$${selection.selectedSkill.name}`;
    if (agent.prompt.includes(marker)) agent.setAgentState({ prompt: agent.prompt.replace(marker, "") });
}

function skillErrorText(value: unknown) {
    if (typeof value === "string") return value;
    if (!value || typeof value !== "object") return i18n.t("agent.state.skillParseFailed");
    const item = value as { path?: unknown; error?: unknown; message?: unknown };
    const message = typeof item.error === "string" ? item.error : typeof item.message === "string" ? item.message : i18n.t("agent.state.skillParseFailed");
    return typeof item.path === "string" ? `${item.path}：${message}` : message;
}
