import { useEffect, useMemo, useState } from "react";
import { Alert, App, Avatar, Button, Empty, Input, Modal, Popconfirm, Segmented, Skeleton, Tag } from "antd";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { Heart, MessageSquareWarning, Repeat2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router-dom";

import { useCopyText } from "@/hooks/use-copy-text";
import { getApiErrorMessage } from "@/lib/api-error";
import { mediaUrl } from "@/services/api/media";
import { useAuthStore } from "@/stores/use-auth-store";
import {
    deleteCommunityWork,
    likeCommunityWork,
    listCommunityWorks,
    listMyCommunityWorks,
    reportCommunityWork,
    unlikeCommunityWork,
    type CommunityWork,
} from "@/services/api/community";

export default function CommunityPage() {
    const { message, modal } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const copyText = useCopyText();
    const queryClient = useQueryClient();
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const [sort, setSort] = useState<"latest" | "hot">("latest");
    const [keyword, setKeyword] = useState("");
    const [query, setQuery] = useState("");
    const [activeTag, setActiveTag] = useState<string | undefined>();
    const [preview, setPreview] = useState<CommunityWork | null>(null);
    const [showMine, setShowMine] = useState(false);

    useEffect(() => {
        const timer = setTimeout(() => setQuery(keyword.trim()), 300);
        return () => clearTimeout(timer);
    }, [keyword]);

    const worksQuery = useInfiniteQuery({
        queryKey: ["community", "works", sort, query, activeTag],
        queryFn: ({ pageParam, signal }) => listCommunityWorks({ cursor: pageParam, size: 24, sort, q: query || undefined, tag: activeTag }, signal),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
        enabled: sort === "latest" ? true : false,
    });
    const hotQuery = useQuery({
        queryKey: ["community", "hot", query, activeTag],
        queryFn: ({ signal }) => listCommunityWorks({ size: 24, sort: "hot", q: query || undefined, tag: activeTag }, signal),
        enabled: sort === "hot",
    });

    const mineQuery = useQuery({
        queryKey: ["community", "mine"],
        queryFn: ({ signal }) => listMyCommunityWorks(signal),
        enabled: showMine && !!userId,
    });
    const mineWorks = mineQuery.data?.items ?? [];

    const works = useMemo(() => {
        if (sort === "hot") return hotQuery.data?.items ?? [];
        return worksQuery.data?.pages.flatMap((page) => page.items) ?? [];
    }, [hotQuery.data?.items, sort, worksQuery.data?.pages]);

    const loading = sort === "hot" ? hotQuery.isPending : worksQuery.isPending;
    const failed = sort === "hot" ? hotQuery.error : worksQuery.error;

    const tags = useMemo(() => {
        const counts = new Map<string, number>();
        for (const work of works) {
            for (const tag of work.tags ?? []) {
                if (!tag) continue;
                counts.set(tag, (counts.get(tag) ?? 0) + 1);
            }
        }
        return Array.from(counts.entries())
            .sort((a, b) => b[1] - a[1])
            .slice(0, 12)
            .map(([tag]) => tag);
    }, [works]);

    const invalidate = () => queryClient.invalidateQueries({ queryKey: ["community"] });

    const unpublishMutation = useMutation({
        mutationFn: (id: string) => deleteCommunityWork(id),
        onSuccess: async () => {
            message.success("作品已下架");
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    // 复刻的最小闭环：复制原作素材链接并跳到对应工作台当参考图；
    // sourceWorkId 随发布落库还需要素材发布弹窗透传（见差异清单说明）。
    const remix = (work: CommunityWork) => {
        const media = mediaUrl(work.storageKey);
        if (media) copyText(new URL(media, window.location.origin).toString(), work.kind === "video" ? "已复制原作视频链接，可在视频工作台作为参考视频使用" : "已复制原作图片链接，可在图片工作台作为参考图使用");
        navigate(work.kind === "video" ? "/video" : "/image");
    };

    const toggleLike = async (work: CommunityWork) => {
        try {
            const result = work.liked ? await unlikeCommunityWork(work.id) : await likeCommunityWork(work.id);
            await invalidate();
            return result;
        } catch (error) {
            message.error(getApiErrorMessage(error));
        }
    };

    const report = (work: CommunityWork) => {
        let reason = "";
        modal.confirm({
            title: t("community.reportTitle"),
            content: (
                <Input.TextArea
                    className="mt-3"
                    rows={3}
                    maxLength={200}
                    placeholder={t("community.reportPlaceholder")}
                    onChange={(event) => {
                        reason = event.target.value;
                    }}
                />
            ),
            okText: t("community.reportSubmit"),
            cancelText: t("common.cancel"),
            onOk: async () => {
                if (!reason.trim()) {
                    message.warning(t("community.reportReasonRequired"));
                    return Promise.reject();
                }
                try {
                    await reportCommunityWork(work.id, reason.trim());
                    message.success(t("community.reported"));
                    await invalidate();
                } catch (error) {
                    message.error(getApiErrorMessage(error));
                }
            },
        });
    };

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-7xl px-4 py-10 sm:px-6">
                <div className="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
                    <div>
                        <h1 className="text-3xl font-semibold">{t("community.title")}</h1>
                        <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("community.description")}</p>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                        <Input.Search allowClear className="w-56" placeholder={t("community.searchPlaceholder")} onChange={(event) => setKeyword(event.target.value)} />
                        <Segmented
                            value={sort}
                            onChange={(value) => setSort(value as "latest" | "hot")}
                            options={[
                                { value: "latest", label: t("community.sortLatest") },
                                { value: "hot", label: t("community.sortHot") },
                            ]}
                        />
                        {userId ? (
                            <Button type={showMine ? "primary" : "default"} onClick={() => setShowMine((value) => !value)}>
                                我的作品
                            </Button>
                        ) : null}
                    </div>
                </div>

                {!showMine && tags.length > 0 ? (
                    <div className="mt-4 flex flex-wrap gap-2">
                        <Tag.CheckableTag checked={!activeTag} onChange={() => setActiveTag(undefined)}>
                            {t("community.allTags")}
                        </Tag.CheckableTag>
                        {tags.map((tag) => (
                            <Tag.CheckableTag key={tag} checked={activeTag === tag} onChange={(checked) => setActiveTag(checked ? tag : undefined)}>
                                {tag}
                            </Tag.CheckableTag>
                        ))}
                    </div>
                ) : null}

                {showMine ? (
                    mineQuery.isError ? (
                        <Alert
                            className="mt-6"
                            type="error"
                            showIcon
                            message={t("community.loadFailed")}
                            description={getApiErrorMessage(mineQuery.error)}
                            action={
                                <Button size="small" onClick={() => void mineQuery.refetch()}>
                                    {t("community.retry")}
                                </Button>
                            }
                        />
                    ) : mineQuery.isPending ? (
                        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                            {Array.from({ length: 4 }).map((_, index) => (
                                <Skeleton.Image key={index} active className="!h-56 !w-full rounded-xl" />
                            ))}
                        </div>
                    ) : mineWorks.length === 0 ? (
                        <Empty className="mt-16" image={Empty.PRESENTED_IMAGE_SIMPLE} description="你还没有发布过作品，可在「我的素材」页把图片或视频发布到社区。" />
                    ) : (
                        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                            {mineWorks.map((work) => (
                                <article key={work.id} className="group flex flex-col overflow-hidden rounded-xl border border-stone-200 transition hover:border-stone-300 dark:border-stone-800 dark:hover:border-stone-700">
                                    <button type="button" className="relative block aspect-4/3 w-full overflow-hidden bg-black/[0.03] dark:bg-white/[0.04]" onClick={() => setPreview(work)}>
                                        {work.kind === "video" ? (
                                            <video src={mediaUrl(work.storageKey)} className="size-full object-cover" muted />
                                        ) : (
                                            <img src={mediaUrl(work.storageKey)} alt={work.title} className="size-full object-cover" loading="lazy" />
                                        )}
                                    </button>
                                    <div className="flex flex-1 flex-col gap-3 p-4">
                                        <h3 className="truncate font-medium">{work.title}</h3>
                                        <div className="mt-auto flex items-center justify-between gap-2">
                                            <span className="flex items-center gap-1 text-xs text-stone-500 dark:text-stone-400">
                                                <Heart className="size-3.5" />
                                                {work.likeCount}
                                            </span>
                                            <Popconfirm
                                                title="确认下架该作品？"
                                                description="下架后作品将从社区公开列表中移除。"
                                                okText="下架"
                                                cancelText={t("common.cancel")}
                                                okButtonProps={{ danger: true }}
                                                onConfirm={() => unpublishMutation.mutate(work.id)}
                                            >
                                                <Button size="small" type="text" loading={unpublishMutation.isPending && unpublishMutation.variables === work.id}>
                                                    下架
                                                </Button>
                                            </Popconfirm>
                                        </div>
                                    </div>
                                </article>
                            ))}
                        </div>
                    )
                ) : failed ? (
                    <Alert
                        className="mt-6"
                        type="error"
                        showIcon
                        message={t("community.loadFailed")}
                        description={getApiErrorMessage(failed)}
                        action={
                            <Button size="small" onClick={() => (sort === "hot" ? void hotQuery.refetch() : void worksQuery.refetch())}>
                                {t("community.retry")}
                            </Button>
                        }
                    />
                ) : loading ? (
                    <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                        {Array.from({ length: 8 }).map((_, index) => (
                            <Skeleton.Image key={index} active className="!h-56 !w-full rounded-xl" />
                        ))}
                    </div>
                ) : works.length === 0 ? (
                    <Empty className="mt-16" image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("community.empty")} />
                ) : (
                    <>
                        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                            {works.map((work) => (
                                <article
                                    key={work.id}
                                    className="group flex flex-col overflow-hidden rounded-xl border border-stone-200 transition hover:border-stone-300 dark:border-stone-800 dark:hover:border-stone-700"
                                >
                                    <button type="button" className="relative block aspect-4/3 w-full overflow-hidden bg-black/[0.03] dark:bg-white/[0.04]" onClick={() => setPreview(work)}>
                                        {work.kind === "video" ? (
                                            <video src={mediaUrl(work.storageKey)} className="size-full object-cover" muted />
                                        ) : (
                                            <img src={mediaUrl(work.storageKey)} alt={work.title} className="size-full object-cover" loading="lazy" />
                                        )}
                                    </button>
                                    <div className="flex flex-1 flex-col gap-3 p-4">
                                        <div className="min-w-0">
                                            <h3 className="truncate font-medium">{work.title}</h3>
                                            {work.description ? <p className="mt-1 line-clamp-2 text-xs text-stone-500 dark:text-stone-400">{work.description}</p> : null}
                                        </div>
                                        <div className="flex flex-wrap gap-1">
                                            {(work.tags ?? []).filter(Boolean).slice(0, 3).map((tag) => (
                                                <Tag key={tag} className="m-0">
                                                    {tag}
                                                </Tag>
                                            ))}
                                        </div>
                                        <div className="mt-auto flex items-center justify-between gap-2">
                                            <Link to={`/community/users/${work.author.id}`} className="flex min-w-0 items-center gap-2">
                                                <Avatar size={24} src={work.author.avatarUrl || undefined} className="bg-stone-200 text-xs dark:bg-stone-700">
                                                    {(work.author.displayName || work.author.username).slice(0, 1).toUpperCase()}
                                                </Avatar>
                                                <span className="truncate text-xs text-stone-500 dark:text-stone-400">{work.author.displayName || work.author.username}</span>
                                            </Link>
                                            <div className="flex shrink-0 items-center gap-1 text-xs text-stone-500 dark:text-stone-400">
                                                <Button
                                                    size="small"
                                                    type="text"
                                                    className="!h-6 !px-1"
                                                    icon={<Heart className={work.liked ? "size-3.5 fill-rose-500 text-rose-500" : "size-3.5"} />}
                                                    onClick={() => void toggleLike(work)}
                                                >
                                                    {work.likeCount}
                                                </Button>
                                                <span className="flex items-center gap-1">
                                                    <Repeat2 className="size-3.5" />
                                                    {work.remixCount}
                                                </span>
                                            </div>
                                        </div>
                                    </div>
                                </article>
                            ))}
                        </div>
                        {sort === "latest" && worksQuery.hasNextPage ? (
                            <div className="mt-6 text-center">
                                <Button loading={worksQuery.isFetchingNextPage} onClick={() => void worksQuery.fetchNextPage()}>
                                    {t("community.loadMore")}
                                </Button>
                            </div>
                        ) : null}
                    </>
                )}
            </div>

            <Modal
                open={!!preview}
                footer={null}
                width={720}
                onCancel={() => setPreview(null)}
                title={preview?.title}
            >
                {preview ? (
                    <div className="flex flex-col gap-4">
                        {preview.kind === "video" ? (
                            <video src={mediaUrl(preview.storageKey)} controls className="max-h-[60vh] w-full rounded-lg bg-black" />
                        ) : (
                            <img src={mediaUrl(preview.storageKey)} alt={preview.title} className="max-h-[60vh] w-full rounded-lg object-contain" />
                        )}
                        {preview.description ? <p className="text-sm text-stone-600 dark:text-stone-300">{preview.description}</p> : null}
                        <div className="flex flex-wrap items-center justify-between gap-3">
                            <div className="flex flex-wrap items-center gap-2">
                                <Avatar size={24} src={preview.author.avatarUrl || undefined}>
                                    {(preview.author.displayName || preview.author.username).slice(0, 1).toUpperCase()}
                                </Avatar>
                                <span className="text-sm">{preview.author.displayName || preview.author.username}</span>
                                <span className="text-xs text-stone-500 dark:text-stone-400">{dayjs(preview.createdAt).format("YYYY-MM-DD HH:mm")}</span>
                            </div>
                            <div className="flex items-center gap-2">
                                <Button
                                    icon={<Heart className={preview.liked ? "size-4 fill-rose-500 text-rose-500" : "size-4"} />}
                                    onClick={async () => {
                                        const result = await toggleLike(preview);
                                        // 详情是打开时的快照，点赞后把结果同步回弹窗数据源，避免再点发反方向请求。
                                        if (result) setPreview({ ...preview, liked: result.liked, likeCount: result.likeCount });
                                    }}
                                >
                                    {preview.likeCount}
                                </Button>
                                {userId ? (
                                    <Button icon={<Repeat2 className="size-4" />} onClick={() => remix(preview)}>
                                        以此作品为素材创作
                                    </Button>
                                ) : null}
                                {userId !== preview.author.id ? (
                                    <Button icon={<MessageSquareWarning className="size-4" />} onClick={() => report(preview)}>
                                        {t("community.report")}
                                    </Button>
                                ) : null}
                            </div>
                        </div>
                    </div>
                ) : null}
            </Modal>
        </main>
    );
}
