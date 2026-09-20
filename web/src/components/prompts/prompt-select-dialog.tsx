import { Search } from "lucide-react";
import { type UIEvent, useEffect, useState } from "react";
import { App, Empty, Input, Modal, Spin, Tag } from "antd";
import { useTranslation } from "react-i18next";

import { ALL_PROMPTS_OPTION } from "@/services/api/prompts";
import { cn } from "@/lib/utils";
import { PromptCard } from "./prompt-card";
import { usePromptList } from "./use-prompt-list";

/**
 * 提示词选择弹窗：从提示词库中筛选并选中一条，把其内容通过 onSelect 回传给使用方。
 * 左侧为分类/标签筛选，右侧为搜索框 + 无限滚动卡片列表；点击卡片即选中并关闭弹窗。
 * @param onSelect     选中提示词后的回调（收到提示词正文）
 * @param onOpenChange 受控开关回调
 */
export function PromptSelectDialog({ open, onOpenChange, onSelect }: { open: boolean; onOpenChange: (open: boolean) => void; onSelect: (prompt: string) => void }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const [keyword, setKeyword] = useState("");
    const [selectedTags, setSelectedTags] = useState<string[]>([]);
    const [selectedCategory, setSelectedCategory] = useState(ALL_PROMPTS_OPTION);
    // 弹窗未打开时不发请求，避免首屏多拉一次列表。
    const { query, items, tags: promptTags, categories: promptCategories } = usePromptList({ keyword, tags: selectedTags, category: selectedCategory, enabled: open });
    const toggleTag = (tag: string) => {
        // 「全部」不参与多选：点它即清空已选标签。
        if (tag === ALL_PROMPTS_OPTION) return setSelectedTags([]);
        setSelectedTags((items) => (items.includes(tag) ? items.filter((item) => item !== tag) : [...items, tag]));
    };
    const selectPrompt = (prompt: string) => {
        onSelect(prompt);
        onOpenChange(false);
    };

    useEffect(() => {
        if (query.isError) message.error(query.error instanceof Error ? query.error.message : t("prompts.loadFailed"));
    }, [message, query.error, query.isError, t]);

    // 距底部不足 160px 时预取下一页，比滚到绝对底部更早开始加载。
    const handleListScroll = (event: UIEvent<HTMLDivElement>) => {
        const target = event.currentTarget;
        if (query.hasNextPage && !query.isFetchingNextPage && target.scrollTop + target.clientHeight >= target.scrollHeight - 160) void query.fetchNextPage();
    };

    return (
        <Modal title={t("prompts.library")} open={open} onCancel={() => onOpenChange(false)} footer={null} width={880} centered>
            {/* 弹窗可能在画布页打开：拦截滚轮事件冒泡并标记 data-canvas-no-zoom，避免滚动列表时触发画布缩放。 */}
            <div className="grid h-[62dvh] min-h-0 gap-5 sm:grid-cols-[200px_minmax(0,1fr)]" data-canvas-no-zoom onWheelCapture={(event) => event.stopPropagation()}>
                <aside className="thin-scrollbar min-h-0 overflow-y-auto border-r border-stone-200 pr-4 dark:border-stone-800">
                    <div className="mb-2 text-xs font-semibold uppercase tracking-widest text-stone-400 dark:text-stone-500">{t("prompts.category")}</div>
                    <div className="flex flex-wrap gap-1.5">
                        {promptCategories.map((category) => (
                            <Tag.CheckableTag key={category} checked={selectedCategory === category} className={cn("prompt-filter-tag", selectedCategory === category && "is-active")} onChange={() => setSelectedCategory(category)}>
                                {category === ALL_PROMPTS_OPTION ? t("common.all") : category}
                            </Tag.CheckableTag>
                        ))}
                    </div>
                    <div className="mb-2 mt-5 text-xs font-semibold uppercase tracking-widest text-stone-400 dark:text-stone-500">{t("prompts.tags")}</div>
                    <div className="flex flex-wrap gap-1.5">
                        {promptTags.map((tag) => {
                            const active = tag === ALL_PROMPTS_OPTION ? selectedTags.length === 0 : selectedTags.includes(tag);
                            return (
                                <Tag.CheckableTag key={tag} checked={active} className={cn("prompt-filter-tag", active && "is-active")} onChange={() => toggleTag(tag)}>
                                    {tag === ALL_PROMPTS_OPTION ? t("common.all") : tag}
                                </Tag.CheckableTag>
                            );
                        })}
                    </div>
                </aside>
                <section className="flex min-h-0 min-w-0 flex-col">
                    <Input size="large" prefix={<Search className="size-4 text-stone-400" />} value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder={t("prompts.searchTitle")} />
                    <div className="thin-scrollbar mt-4 min-h-0 flex-1 overflow-y-auto pr-2" data-canvas-no-zoom onScroll={handleListScroll} onWheelCapture={(event) => event.stopPropagation()}>
                        {query.isLoading ? (
                            <div className="flex h-40 items-center justify-center">
                                <Spin />
                            </div>
                        ) : null}
                        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
                            {items.map((item) => (
                                <PromptCard key={item.id} item={item} onOpen={() => selectPrompt(item.prompt)} onCopy={() => selectPrompt(item.prompt)} compact />
                            ))}
                        </div>
                        {!query.isLoading && items.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("prompts.empty")} className="py-8" /> : null}
                        {query.isFetchingNextPage ? (
                            <div className="py-4 text-center">
                                <Spin size="small" />
                            </div>
                        ) : null}
                    </div>
                </section>
            </div>
        </Modal>
    );
}
