import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { Popover } from "antd";
import { Sparkles } from "lucide-react";
import { useTranslation } from "react-i18next";

import { canvasThemes } from "@/lib/canvas-theme";
import { buildCanvasResourceReferences, type CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { isImeComposing, isPlainEnterKey } from "@/lib/keyboard-event";
import type { AgentSkillSummary } from "@/services/api/canvas-agent";
import { useAgentSkillStore } from "@/stores/use-agent-skill-store";
import { useAgentStore, type AgentCanvasReference, type AgentSkillReference } from "@/stores/use-agent-store";
import { AgentCanvasReferencePreview, canvasReferenceIcon, canvasReferenceKindLabel } from "./agent-canvas-reference-preview";
import { agentInlineTokenClass, agentInlineTokenMediaClass, agentReferenceMarker, agentSkillMarker, parseAgentInlineTokens } from "./agent-chat-inline-tokens";

// 输入框内的 mention 命令状态：/ 触发技能搜索、@ 触发画布素材搜索，query 为命令符后的输入。
type ComposerCommand = { type: "skill" | "resource"; query: string; length: number };
type ComposerCandidate = { type: "skill"; skill: AgentSkillSummary } | { type: "resource"; reference: CanvasResourceReference };
// 画布引用 token 的悬停预览定位（相对输入容器）。
type ReferenceHover = { reference: AgentCanvasReference; left: number; top: number; width: number; height: number };

/**
 * 基于 contentEditable 的输入框：支持 /技能 与 @画布素材 的 mention 补全、图片粘贴、IME 中文输入。
 * 值以纯文本 + 内联标记（$skill / @label）序列化，token 用不可编辑 span 呈现，光标可整体删除。
 */
export function AgentChatPromptInput({ value, disabled, placeholder, theme, onChange, onSubmit, onAddFiles }: {
    value: string;
    disabled?: boolean;
    placeholder: string;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    onChange: (value: string) => void;
    onSubmit: () => void;
    onAddFiles?: (files: FileList | File[] | null) => void | Promise<void>;
}) {
    const containerRef = useRef<HTMLDivElement>(null);
    const editorRef = useRef<HTMLDivElement>(null);
    const composingRef = useRef(false);
    const lastEmittedRef = useRef(value);
    const skills = useAgentSkillStore((state) => state.skills);
    const skillsLoading = useAgentSkillStore((state) => state.loading);
    const selectedSkill = useAgentSkillStore((state) => state.selectedSkill);
    const canvasReferences = useAgentStore((state) => state.canvasReferences);
    const [command, setCommand] = useState<ComposerCommand | null>(null);
    const [activeIndex, setActiveIndex] = useState(0);
    const [resourceCandidates, setResourceCandidates] = useState<CanvasResourceReference[]>([]);
    const [referenceHover, setReferenceHover] = useState<ReferenceHover | null>(null);

    const messageSkill = useMemo<AgentSkillReference | undefined>(() => selectedSkill ? {
        name: selectedSkill.name,
        path: selectedSkill.path,
        displayName: selectedSkill.interface?.displayName || undefined,
    } : undefined, [selectedSkill]);
    const tokens = useMemo(() => parseAgentInlineTokens(value, canvasReferences, messageSkill), [canvasReferences, messageSkill, value]);
    const selectedReferenceIds = useMemo(() => new Set(canvasReferences.map((item) => item.nodeId)), [canvasReferences]);
    const candidates = useMemo<ComposerCandidate[]>(() => {
        if (!command) return [];
        const query = command.query.trim().toLowerCase();
        if (command.type === "skill") {
            return skills
                .filter((skill) => skill.enabled && (!query || [skill.name, skill.description, skill.interface?.displayName, skill.interface?.shortDescription, skill.shortDescription].some((item) => item?.toLowerCase().includes(query))))
                .map((skill) => ({ type: "skill", skill }));
        }
        return resourceCandidates
            .filter((reference) => !selectedReferenceIds.has(reference.nodeId) && (!query || `${reference.label} ${reference.title} ${reference.kind} ${reference.text || ""}`.toLowerCase().includes(query)))
            .map((reference) => ({ type: "resource", reference }));
    }, [command, resourceCandidates, selectedReferenceIds, skills]);

    useEffect(() => {
        // React 不接管 contentEditable 的子节点：值变化时手动把 token 序列重建为 DOM。
        // 焦点在编辑器且值未变时跳过，避免光标位置被打断。
        const editor = editorRef.current;
        if (!editor || document.activeElement === editor && value === lastEmittedRef.current) return;
        editor.replaceChildren(...tokens.map((token) => {
            if (token.type === "text") return document.createTextNode(token.value);
            return token.type === "skill"
                ? createSkillToken(token.skill, theme)
                : createReferenceToken(token.reference, theme);
        }));
        lastEmittedRef.current = value;
    }, [theme, tokens, value]);

    const emit = (next: string) => {
        lastEmittedRef.current = next;
        onChange(next);
    };

    const closeCommand = () => {
        setCommand(null);
        setActiveIndex(0);
    };

    const syncCommand = () => {
        // 光标前文本命中「行首或空白后的 / 或 @」时打开候选菜单；/ 技能候选懒加载，@ 素材候选以选中节点优先。
        const text = textBeforeCaret(editorRef.current);
        const match = /(^|\s)([/@])([^\s/@]*)$/.exec(text);
        if (!match) return closeCommand();
        const type = match[2] === "/" ? "skill" : "resource";
        setCommand({ type, query: match[3] || "", length: (match[3] || "").length + 1 });
        setActiveIndex(0);
        if (type === "skill") {
            const agent = useAgentStore.getState();
            const skillState = useAgentSkillStore.getState();
            if (agent.connected && !skillState.loaded && !skillState.loading) void skillState.loadSkills(agent.url.trim().replace(/\/$/, ""), agent.token);
            return;
        }
        const snapshot = useAgentStore.getState().canvasContext?.snapshot;
        const references = buildCanvasResourceReferences(snapshot?.nodes || []);
        const selectedIds = new Set(snapshot?.selectedNodeIds || []);
        setResourceCandidates([...references.filter((item) => selectedIds.has(item.nodeId)), ...references.filter((item) => !selectedIds.has(item.nodeId))]);
    };

    const syncFromEditor = () => {
        const editor = editorRef.current;
        if (!editor) return;
        const next = serializeEditor(editor);
        emit(next);
        syncSelectedMetadata(editor);
        syncCommand();
    };

    /** 选中候选：技能有默认 prompt 且输入为空时直接套用；素材引用写入 store 并插入 token。 */
    const insertCandidate = (candidate: ComposerCandidate) => {
        const editor = editorRef.current;
        if (!editor || !command) return;
        // 技能 token 全文唯一：插入前移除旧技能 token，并删掉命令符本身。
        if (candidate.type === "skill") editor.querySelector<HTMLElement>("[data-agent-token-kind='skill']")?.remove();
        removeTextBeforeCaret(command.length);

        if (candidate.type === "skill") {
            const base = serializeEditor(editor);
            const defaultPrompt = candidate.skill.interface?.defaultPrompt?.trim();
            if (!base.trim() && defaultPrompt) {
                emit("");
                useAgentSkillStore.getState().selectSkill(candidate.skill);
                closeCommand();
                return;
            }
            insertTokenAtCaret(editor, createSkillToken({ name: candidate.skill.name, path: candidate.skill.path, displayName: candidate.skill.interface?.displayName || undefined }, theme));
            const next = serializeEditor(editor);
            emit(next);
            useAgentSkillStore.getState().selectSkill(candidate.skill);
        } else {
            const current = useAgentStore.getState().canvasReferences;
            if (!current.some((item) => item.nodeId === candidate.reference.nodeId)) useAgentStore.getState().setAgentState({ canvasReferences: [...current, candidate.reference] });
            insertTokenAtCaret(editor, createReferenceToken(candidate.reference, theme));
            emit(serializeEditor(editor));
        }
        closeCommand();
    };

    /** 候选菜单的键盘导航：上下选择、Enter/Tab 选中、Esc 关闭；IME 组合中不拦截。 */
    const handleCommandKey = (event: KeyboardEvent<HTMLDivElement>) => {
        if (!command || isImeComposing(event)) return false;
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            if (candidates.length) setActiveIndex((index) => (index + (event.key === "ArrowDown" ? 1 : candidates.length - 1)) % candidates.length);
            return true;
        }
        if (event.key === "Enter" || event.key === "Tab") {
            event.preventDefault();
            if (candidates.length) insertCandidate(candidates[Math.min(activeIndex, candidates.length - 1)]);
            return true;
        }
        if (event.key === "Escape") {
            event.preventDefault();
            closeCommand();
            return true;
        }
        return false;
    };

    const showReferencePreview = (event: MouseEvent<HTMLDivElement>) => {
        const token = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-agent-token-kind='resource']") : null;
        const container = containerRef.current;
        if (!token || !container?.contains(token)) return setReferenceHover(null);
        const reference = useAgentStore.getState().canvasReferences.find((item) => item.nodeId === token.dataset.nodeId);
        if (!reference) return setReferenceHover(null);
        const tokenRect = token.getBoundingClientRect();
        const containerRect = container.getBoundingClientRect();
        setReferenceHover({ reference, left: tokenRect.left - containerRect.left, top: tokenRect.top - containerRect.top, width: tokenRect.width, height: tokenRect.height });
    };

    return (
        <div ref={containerRef} className="relative">
            {!value.trim() ? <div className="pointer-events-none absolute left-1 top-1 text-sm leading-6" style={{ color: theme.node.placeholder }}>{placeholder}</div> : null}
            <div
                ref={editorRef}
                contentEditable={!disabled}
                suppressContentEditableWarning
                role="textbox"
                aria-multiline="true"
                aria-label={placeholder}
                className="thin-scrollbar max-h-32 min-h-20 w-full overflow-y-auto whitespace-pre-wrap break-words bg-transparent px-1 py-1 text-sm leading-6 outline-none"
                style={{ color: theme.node.text, cursor: disabled ? "default" : "text" }}
                onInput={() => {
                    if (!composingRef.current) syncFromEditor();
                }}
                onCompositionStart={() => { composingRef.current = true; }}
                onCompositionEnd={() => {
                    composingRef.current = false;
                    syncFromEditor();
                }}
                onPaste={(event) => {
                    // 粘贴行为重写：图片走附件流程，其余按纯文本插入，禁止富文本/HTML 混入。
                    const images = Array.from(event.clipboardData.files).filter((file) => file.type.startsWith("image/"));
                    if (images.length && onAddFiles) {
                        event.preventDefault();
                        void onAddFiles(images);
                        return;
                    }
                    event.preventDefault();
                    insertTextAtCaret(event.clipboardData.getData("text/plain"));
                    syncFromEditor();
                }}
                onKeyDown={(event) => {
                    // 阻止画布全局快捷键响应输入框按键；IME 组合中不触发提交/删除。
                    event.stopPropagation();
                    if (isImeComposing(event) || handleCommandKey(event)) return;
                    if ((event.key === "Backspace" || event.key === "Delete") && deleteAdjacentToken(event.key)) {
                        event.preventDefault();
                        requestAnimationFrame(syncFromEditor);
                        return;
                    }
                    if (isPlainEnterKey(event)) {
                        event.preventDefault();
                        onSubmit();
                        return;
                    }
                    requestAnimationFrame(syncCommand);
                }}
                onKeyUp={(event) => {
                    if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) syncCommand();
                }}
                onClick={syncCommand}
                onMouseOver={showReferencePreview}
                onMouseLeave={() => setReferenceHover(null)}
                onBlur={(event) => {
                    // 焦点移入候选菜单不关闭；延迟 120ms 给「点击候选项」留出触发时间。
                    if (event.relatedTarget instanceof HTMLElement && event.relatedTarget.closest("[data-agent-command-menu]")) return;
                    window.setTimeout(closeCommand, 120);
                }}
            />
            {referenceHover ? (
                <Popover
                    open
                    placement="top"
                    content={<AgentCanvasReferencePreview reference={referenceHover.reference} previewUrl={referenceHover.reference.previewUrl} previewText={referenceHover.reference.text} theme={theme} />}
                >
                    <span
                        aria-hidden
                        className="pointer-events-none absolute"
                        style={{ left: referenceHover.left, top: referenceHover.top, width: referenceHover.width, height: referenceHover.height }}
                    />
                </Popover>
            ) : null}
            {command ? <AgentCommandMenu command={command} candidates={candidates} activeIndex={Math.min(activeIndex, Math.max(candidates.length - 1, 0))} loading={command.type === "skill" && skillsLoading} theme={theme} onSelect={insertCandidate} /> : null}
        </div>
    );
}
/** mention 候选菜单：跟随输入框顶部展示，activeIndex 项自动滚动到可视区。 */
function AgentCommandMenu({ command, candidates, activeIndex, loading, theme, onSelect }: { command: ComposerCommand; candidates: ComposerCandidate[]; activeIndex: number; loading: boolean; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; onSelect: (candidate: ComposerCandidate) => void }) {
    const { t } = useTranslation();
    const activeItemRef = useRef<HTMLButtonElement | null>(null);
    useEffect(() => { activeItemRef.current?.scrollIntoView({ block: "nearest" }); }, [activeIndex]);
    const stopPropagation = (event: PointerEvent | MouseEvent) => event.stopPropagation();
    return (
        <div data-agent-command-menu className="absolute bottom-[calc(100%+8px)] left-0 z-[120] w-full min-w-64 overflow-hidden rounded-xl border shadow-xl" style={{ background: theme.toolbar.panel, borderColor: theme.toolbar.border, color: theme.node.text }} onPointerDown={stopPropagation} onMouseDown={stopPropagation}>
            <div className="border-b px-3 py-2 text-xs" style={{ borderColor: theme.toolbar.border, color: theme.node.muted }}>
                {t(command.type === "skill" ? "agent.composer.mentions.selectSkill" : "agent.composer.mentions.selectResource")}{command.query ? ` · ${command.query}` : ""}
            </div>
            <div className="thin-scrollbar max-h-[min(21rem,52vh)] overflow-y-auto p-1">
                {candidates.length ? candidates.map((candidate, index) => {
                    const skill = candidate.type === "skill" ? candidate.skill : null;
                    const reference = candidate.type === "resource" ? candidate.reference : null;
                    const title = skill ? skill.interface?.displayName || skill.name : reference?.title || "";
                    const description = skill ? skill.interface?.shortDescription || skill.shortDescription || skill.description : reference ? `${agentReferenceMarker(reference)} · ${canvasReferenceKindLabel(reference.kind)}` : "";
                    return (
                        <button key={skill ? `${skill.name}:${skill.path}` : reference?.nodeId} ref={index === activeIndex ? activeItemRef : undefined} type="button" className="flex w-full min-w-0 items-center gap-2.5 rounded-lg px-2 py-2 text-left transition hover:bg-black/5 dark:hover:bg-white/10" style={{ background: index === activeIndex ? theme.toolbar.activeBg : undefined, color: index === activeIndex ? theme.toolbar.activeText : theme.node.text }} onPointerDown={(event) => { event.preventDefault(); onSelect(candidate); }}>
                            {skill ? <span className="grid size-9 shrink-0 place-items-center"><Sparkles className="size-4" /></span> : reference ? <ReferencePreview reference={reference} /> : null}
                            <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{title}</span><span className="mt-0.5 block truncate text-xs" style={{ color: theme.node.muted }}>{description}</span></span>
                        </button>
                    );
                }) : <div className="px-3 py-6 text-center text-xs" style={{ color: theme.node.muted }}>{t(loading ? "agent.composer.mentions.loadingSkills" : command.type === "skill" ? "agent.composer.mentions.noSkills" : "agent.composer.mentions.noResources")}</div>}
            </div>
        </div>
    );
}

function ReferencePreview({ reference }: { reference: CanvasResourceReference }) {
    if (reference.kind === "image" && reference.previewUrl) return <img src={reference.previewUrl} alt="" className="size-9 rounded-md object-cover" />;
    const Icon = canvasReferenceIcon(reference.kind);
    return <span className="grid size-9 shrink-0 place-items-center"><Icon className="size-4" /></span>;
}

// —— 以下为 contentEditable 底层操作：token DOM 构造、光标处插入/删除与序列化 ——

/** 构造技能 mention token：$name 作为数据标记，显示名以 / 开头。 */
function createSkillToken(skill: AgentSkillReference, theme: (typeof canvasThemes)[keyof typeof canvasThemes]) {
    const token = createToken("skill", agentSkillMarker(skill), theme);
    token.dataset.skillName = skill.name;
    token.title = skill.path;
    token.textContent = `/${skill.displayName || skill.name}`;
    return token;
}

/** 构造画布素材 mention token：nodeId 存入 dataset，图片素材内嵌缩略图。 */
function createReferenceToken(reference: AgentCanvasReference, theme: (typeof canvasThemes)[keyof typeof canvasThemes]) {
    const token = createToken("resource", agentReferenceMarker(reference), theme);
    token.dataset.nodeId = reference.nodeId;
    token.title = reference.title;
    if (reference.kind === "image" && reference.previewUrl) {
        const image = document.createElement("img");
        image.src = reference.previewUrl;
        image.alt = "";
        image.className = agentInlineTokenMediaClass;
        token.append(image);
    }
    token.append(document.createTextNode(agentReferenceMarker(reference)));
    return token;
}

/** token 公共属性：不可编辑（整体删除）、携带序列化标记与主题配色。 */
function createToken(kind: "skill" | "resource", marker: string, theme: (typeof canvasThemes)[keyof typeof canvasThemes]) {
    const token = document.createElement("span");
    token.contentEditable = "false";
    token.dataset.agentToken = marker;
    token.dataset.agentTokenKind = kind;
    token.className = agentInlineTokenClass;
    Object.assign(token.style, { background: theme.toolbar.panel, borderColor: theme.node.stroke, color: theme.node.text } as CSSProperties);
    return token;
}

/** 在光标处插入 token（token 后补一个空格并把光标移到空格后）；编辑器未聚焦时追加到末尾。 */
function insertTokenAtCaret(editor: HTMLElement, token: HTMLElement) {
    const selection = window.getSelection();
    const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
    const space = document.createTextNode(" ");
    if (!range || !editor.contains(range.startContainer)) {
        editor.append(token, space);
        placeCaretAfter(space);
        return;
    }
    range.deleteContents();
    range.insertNode(space);
    range.insertNode(token);
    placeCaretAfter(space);
}

function insertTextAtCaret(text: string) {
    const selection = window.getSelection();
    if (!selection?.rangeCount) return;
    const range = selection.getRangeAt(0);
    range.deleteContents();
    const node = document.createTextNode(text);
    range.insertNode(node);
    placeCaretAfter(node);
}

function placeCaretAfter(node: Node) {
    const selection = window.getSelection();
    const range = document.createRange();
    range.setStartAfter(node);
    range.collapse(true);
    selection?.removeAllRanges();
    selection?.addRange(range);
}

function removeTextBeforeCaret(length: number) {
    const selection = window.getSelection();
    if (!selection?.rangeCount) return;
    const range = selection.getRangeAt(0);
    if (range.startContainer.nodeType !== Node.TEXT_NODE || range.startOffset < length) return;
    range.setStart(range.startContainer, range.startOffset - length);
    range.deleteContents();
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
}

/** Backspace/Delete 时删除紧邻光标的 token（跨过空白文本节点查找）；命中返回 true。 */
function deleteAdjacentToken(key: string) {
    const selection = window.getSelection();
    if (!selection?.rangeCount || !selection.isCollapsed) return false;
    const range = selection.getRangeAt(0);
    const previous = key === "Backspace";
    const target = adjacentToken(range, previous);
    if (!target) return false;
    const caret = document.createTextNode("");
    target.replaceWith(caret);
    const nextRange = document.createRange();
    nextRange.setStart(caret, 0);
    nextRange.collapse(true);
    selection.removeAllRanges();
    selection.addRange(nextRange);
    return true;
}

function adjacentToken(range: Range, previous: boolean) {
    const container = range.startContainer;
    const offset = range.startOffset;
    if (container.nodeType === Node.TEXT_NODE) {
        const text = container.textContent || "";
        if (previous ? offset > 0 : offset < text.length) return null;
        return tokenSibling(container, previous);
    }
    const children = Array.from(container.childNodes);
    const node = children[previous ? offset - 1 : offset];
    return node instanceof HTMLElement && node.dataset.agentToken ? node : tokenSibling(node || container, previous);
}

function tokenSibling(node: Node, previous: boolean) {
    let current: Node | null = previous ? node.previousSibling : node.nextSibling;
    while (current?.nodeType === Node.TEXT_NODE && !(current.textContent || "").trim()) current = previous ? current.previousSibling : current.nextSibling;
    return current instanceof HTMLElement && current.dataset.agentToken ? current : null;
}

function syncSelectedMetadata(editor: HTMLElement) {
    const state = useAgentStore.getState();
    const nodeIds = new Set(Array.from(editor.querySelectorAll<HTMLElement>("[data-agent-token-kind='resource']")).map((item) => item.dataset.nodeId));
    const references = state.canvasReferences.filter((item) => nodeIds.has(item.nodeId));
    if (references.length !== state.canvasReferences.length) state.setAgentState({ canvasReferences: references });
    const selectedSkill = useAgentSkillStore.getState().selectedSkill;
    const skillName = editor.querySelector<HTMLElement>("[data-agent-token-kind='skill']")?.dataset.skillName;
    if (selectedSkill && skillName !== selectedSkill.name) useAgentSkillStore.getState().clearSelection();
}

function textBeforeCaret(editor: HTMLElement | null) {
    const selection = window.getSelection();
    if (!editor || !selection?.rangeCount) return "";
    const range = selection.getRangeAt(0).cloneRange();
    if (!editor.contains(range.startContainer)) return "";
    range.setStart(editor, 0);
    return range.toString();
}

/** 编辑器内容 → 带标记的纯文本：token 还原为标记，BR/块级节点换行，最后去掉粘贴残留的零宽 BOM。 */
function serializeEditor(editor: HTMLElement) {
    return serializeNodes(editor.childNodes).replace(/﻿/g, "");
}

function serializeNodes(nodes: NodeListOf<ChildNode>) {
    let result = "";
    nodes.forEach((node) => {
        if (node.nodeType === Node.TEXT_NODE) {
            result += node.textContent || "";
            return;
        }
        if (!(node instanceof HTMLElement)) return;
        const marker = node.dataset.agentToken;
        if (marker) result += marker;
        else if (node.tagName === "BR") result += "\n";
        else {
            if (["DIV", "P"].includes(node.tagName) && result && !result.endsWith("\n")) result += "\n";
            result += serializeNodes(node.childNodes);
        }
    });
    return result;
}
