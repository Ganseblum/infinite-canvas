/** 一条版本记录：version/date 取自 `## v1.2.3 - 日期` 标题行，items 为 `[新增]/[调整]…` 条目。 */
export type ReleaseInfo = {
    version: string;
    date: string;
    items: { type: string; content: string }[];
};

/** 解析 CHANGELOG.md：按 `## ` 切分版本块，只保留含条目的版本，供版本弹窗展示。 */
export function parseChangelog(content: string): ReleaseInfo[] {
    return content
        .split(/^## /m)
        .slice(1)
        .map((block) => {
            const [title = "", ...lines] = block.trim().split("\n");
            const [, version = title.trim(), date = ""] = title.match(/^(.+?)(?:\s+-\s+(.+))?$/) || [];
            return {
                version: version.trim(),
                date: date.trim(),
                items: lines
                    .map((line) => line.trim().match(/^\+\s+\[(.+?)\]\s+(.+)$/))
                    .filter((match): match is RegExpMatchArray => Boolean(match))
                    .map((match) => ({ type: match[1], content: match[2] })),
            };
        })
        .filter((release) => release.items.length);
}
