import { Copy, FileText } from "lucide-react";
import type { ReactNode } from "react";
import { Button, Card, Tag } from "antd";
import { useTranslation } from "react-i18next";

import { formatPromptDate, type Prompt } from "@/services/api/prompts";

/**
 * 提示词卡片：封面 + 标题 + 描述/标签 + 底部操作按钮。
 * compact 模式用于选择弹窗（正方形封面、隐藏描述/标签/操作区，点击即选中）；
 * 常规模式用于提示词库页面（4:3 封面、展示元信息与复制按钮）。
 * @param item        提示词数据
 * @param onOpen      点击卡片主体（封面/内容区）时的回调
 * @param onCopy      底部主操作按钮（默认「复制」）的回调
 * @param actionLabel 自定义主操作文案，缺省用「复制」
 * @param actionIcon  主操作按钮图标
 * @param actionType  主操作按钮类型：text 或 primary（primary 时占满整行）
 * @param extraAction 追加在主操作按钮之后的额外动作区
 * @param compact     是否为弹窗内的紧凑模式
 */
export function PromptCard({
    item,
    onOpen,
    onCopy,
    actionLabel,
    actionIcon = <Copy className="size-3.5" />,
    actionType = "text",
    extraAction,
    compact = false,
}: {
    item: Prompt;
    onOpen: () => void;
    onCopy: () => void;
    actionLabel?: string;
    actionIcon?: ReactNode;
    actionType?: "text" | "primary";
    extraAction?: ReactNode;
    compact?: boolean;
}) {
    const { i18n, t } = useTranslation();
    return (
        <Card
            hoverable
            className={compact ? "group cursor-pointer overflow-hidden transition-transform duration-200 hover:-translate-y-1" : "flex h-full flex-col overflow-hidden"}
            styles={{ body: compact ? { padding: 0 } : { display: "flex", flex: 1, flexDirection: "column", padding: 0 } }}
            cover={
                <button type="button" className="block w-full cursor-pointer text-left" onClick={onOpen}>
                    {item.coverUrl ? <img src={item.coverUrl} alt={item.title} className={compact ? "aspect-square w-full object-cover transition-transform duration-300 group-hover:scale-[1.03]" : "aspect-[4/3] w-full object-cover"} loading="lazy" /> : <span className={compact ? "grid aspect-square w-full place-items-center bg-stone-100 text-stone-400 dark:bg-stone-900 dark:text-stone-600" : "grid aspect-[4/3] w-full place-items-center bg-stone-100 text-stone-400 dark:bg-stone-900 dark:text-stone-600"}><FileText className="size-8" /></span>}
                </button>
            }
        >
            <button type="button" className={compact ? "block w-full cursor-pointer text-left" : "block w-full flex-1 cursor-pointer text-left"} onClick={onOpen}>
                <div className={compact ? "px-3 py-2.5" : "p-4"}>
                    <div className="flex items-start justify-between gap-3">
                        <h2 className="line-clamp-1 text-sm font-semibold text-stone-950 dark:text-stone-100">{item.title}</h2>
                        {!compact ? <span className="shrink-0 text-xs text-stone-400 dark:text-stone-500">{formatPromptDate(item.updatedAt, i18n.resolvedLanguage)}</span> : null}
                    </div>
                    {!compact ? <><p className="mt-2 line-clamp-3 text-xs leading-5 text-stone-600 dark:text-stone-400">{item.description || item.prompt}</p><div className="mt-3 flex flex-wrap gap-1.5">{item.tags.map((tag) => <Tag key={tag} className="m-0 text-[11px]">{tag}</Tag>)}</div></> : null}
                </div>
            </button>
            {!compact ? <div className="mt-auto flex items-center gap-2 px-4 pb-4"><Button block={actionType === "primary"} type={actionType} size="small" icon={actionIcon} onClick={onCopy}>{actionLabel || t("common.copy")}</Button>{extraAction}</div> : null}
        </Card>
    );
}
