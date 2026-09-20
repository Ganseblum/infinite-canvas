import type { AgentCanvasReference, AgentSkillReference } from "@/stores/use-agent-store";

// mention token 的内联展示样式：带边框的小胶囊，媒体缩略图与图标各用一套尺寸。
export const agentInlineTokenClass = "mx-0.5 inline-block h-7 whitespace-nowrap rounded-md border px-1.5 align-baseline text-sm leading-[26px]";
export const agentInlineTokenMediaClass = "mr-1 inline-block size-5 rounded object-cover align-middle";
export const agentInlineTokenIconClass = "mr-1 inline-block size-4 align-middle opacity-65";

/** 输入框/消息正文解析出的内联片段：纯文本、技能引用或画布素材引用三选一。 */
export type AgentInlineToken =
    | { type: "text"; value: string }
    | { type: "skill"; skill: AgentSkillReference }
    | { type: "reference"; reference: AgentCanvasReference };

/** 技能在纯文本中的标记格式：`$技能名`，序列化与解析都以此为准。 */
export function agentSkillMarker(skill: Pick<AgentSkillReference, "name">) {
    return `$${skill.name}`;
}

/** 画布素材在纯文本中的标记格式：`@素材标签`。 */
export function agentReferenceMarker(reference: Pick<AgentCanvasReference, "label">) {
    return `@${reference.label}`;
}

/**
 * 把带 mention 标记的纯文本切成 token 序列，供输入框/消息按片段渲染。
 * @param text 含 `$技能名` / `@素材标签` 标记的原文
 * @param references 参与匹配的画布素材引用
 * @param skill 当前选中的技能（至多一个），不传则不匹配技能标记
 * @returns 按原文顺序排列的 token 数组；无任何标记时返回整段文本
 */
export function parseAgentInlineTokens(text: string, references: AgentCanvasReference[], skill?: AgentSkillReference): AgentInlineToken[] {
    // 标记按长度降序排列：避免「@a」是「@ab」前缀时短标记先匹配导致长标记永远匹配不上。
    const markers = [
        ...(skill ? [{ marker: agentSkillMarker(skill), token: { type: "skill", skill } as AgentInlineToken }] : []),
        ...references.map((reference) => ({ marker: agentReferenceMarker(reference), token: { type: "reference", reference } as AgentInlineToken })),
    ].sort((a, b) => b.marker.length - a.marker.length);
    if (!markers.length) return [{ type: "text", value: text }];

    const tokens: AgentInlineToken[] = [];
    let cursor = 0;
    while (cursor < text.length) {
        let nextIndex = -1;
        let nextMarker: (typeof markers)[number] | undefined;
        markers.forEach((item) => {
            const index = text.indexOf(item.marker, cursor);
            if (index >= 0 && (nextIndex < 0 || index < nextIndex)) {
                nextIndex = index;
                nextMarker = item;
            }
        });
        if (!nextMarker || nextIndex < 0) {
            tokens.push({ type: "text", value: text.slice(cursor) });
            break;
        }
        if (nextIndex > cursor) tokens.push({ type: "text", value: text.slice(cursor, nextIndex) });
        tokens.push(nextMarker.token);
        cursor = nextIndex + nextMarker.marker.length;
    }
    // 循环至少会产出一个 token；此兜底仅防御空输入等极端情况，保证调用方拿到的数组非空。
    return tokens.length ? tokens : [{ type: "text", value: "" }];
}
