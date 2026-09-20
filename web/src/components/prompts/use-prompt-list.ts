import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { ALL_PROMPTS_OPTION, fetchPrompts } from "@/services/api/prompts";

// 提示词库分页大小：与无限滚动列表的单页条数保持一致。
export const PROMPT_PAGE_SIZE = 20;

/**
 * 提示词列表的共享数据 hook：负责关键词防抖、分页拉取与筛选选项归集。
 * 首页返回的 tags / categories 作为全部筛选项来源（翻页不追加筛选项）。
 * @param keyword 搜索关键词，内部做 300ms 防抖后再发起请求
 * @param tags    标签筛选，多选且为交集语义（传给接口的 tag 参数）
 * @param category 分类筛选，单选
 * @param enabled  false 时不发请求（如弹窗未打开），打开后自动拉取
 * @returns query 为原始 infinite query；items 为跨页合并的列表
 */
export function usePromptList({ keyword, tags, category, enabled = true }: { keyword: string; tags: string[]; category: string; enabled?: boolean }) {
    const [debouncedKeyword, setDebouncedKeyword] = useState(keyword);
    useEffect(() => {
        // 输入停顿 300ms 才更新搜索词，避免每敲一个字符就发一次请求。
        const timer = setTimeout(() => setDebouncedKeyword(keyword), 300);
        return () => clearTimeout(timer);
    }, [keyword]);
    const query = useInfiniteQuery({
        queryKey: ["prompts", debouncedKeyword, tags, category],
        queryFn: ({ pageParam }) => fetchPrompts({ keyword: debouncedKeyword, tag: tags, category, page: pageParam, pageSize: PROMPT_PAGE_SIZE }),
        initialPageParam: 1,
        getNextPageParam: (lastPage, pages) => (pages.reduce((total, page) => total + page.items.length, 0) < lastPage.total ? pages.length + 1 : undefined),
        enabled,
    });
    const firstPage = query.data?.pages[0];
    return {
        query,
        items: useMemo(() => query.data?.pages.flatMap((page) => page.items) || [], [query.data?.pages]),
        tags: useMemo(() => [ALL_PROMPTS_OPTION, ...(firstPage?.tags || [])], [firstPage?.tags]),
        categories: useMemo(() => [ALL_PROMPTS_OPTION, ...(firstPage?.categories || [])], [firstPage?.categories]),
        total: firstPage?.total || 0,
    };
}
