import { Alert, Avatar, Empty, Skeleton } from "antd";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { Link, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { mediaUrl } from "@/services/api/media";
import { getCommunityUser } from "@/services/api/community";

/** 社区用户主页入口：用户资料、作品/获赞统计与其公开作品网格。 */
export default function CommunityUserPage() {
    const { t } = useTranslation();
    const params = useParams();
    const userId = params.id ?? "";

    const profileQuery = useQuery({
        queryKey: ["community", "user", userId],
        queryFn: ({ signal }) => getCommunityUser(userId, signal),
        // 路由参数缺失时不发请求，直接落在空态。
        enabled: !!userId,
    });

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-6xl px-4 py-10 sm:px-6">
                {profileQuery.isError ? (
                    <Alert type="error" showIcon message={t("community.userLoadFailed")} description={getApiErrorMessage(profileQuery.error)} />
                ) : profileQuery.isPending ? (
                    <Skeleton active avatar paragraph={{ rows: 6 }} />
                ) : profileQuery.data ? (
                    <>
                        <div className="flex flex-wrap items-center gap-4">
                            <Avatar size={64} src={profileQuery.data.user.avatarUrl || undefined} className="bg-stone-200 text-xl dark:bg-stone-700">
                                {(profileQuery.data.user.displayName || profileQuery.data.user.username).slice(0, 1).toUpperCase()}
                            </Avatar>
                            <div>
                                <h1 className="text-2xl font-semibold">{profileQuery.data.user.displayName || profileQuery.data.user.username}</h1>
                                <p className="mt-1 text-sm text-stone-500 dark:text-stone-400">
                                    {profileQuery.data.user.username} · {t("community.joinedAt", { date: dayjs(profileQuery.data.user.createdAt).format("YYYY-MM-DD") })}
                                </p>
                            </div>
                            <div className="ml-auto flex gap-6 text-sm">
                                <div>
                                    <div className="text-xl font-semibold">{profileQuery.data.stats.works}</div>
                                    <div className="text-stone-500 dark:text-stone-400">{t("community.workCount")}</div>
                                </div>
                                <div>
                                    <div className="text-xl font-semibold">{profileQuery.data.stats.likes}</div>
                                    <div className="text-stone-500 dark:text-stone-400">{t("community.likeCount")}</div>
                                </div>
                            </div>
                        </div>
                        <div className="mt-8">
                            {profileQuery.data.items.length === 0 ? (
                                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("community.userEmpty")} />
                            ) : (
                                <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                                    {profileQuery.data.items.map((work) => (
                                        <div key={work.id} className="overflow-hidden rounded-xl border border-stone-200 dark:border-stone-800">
                                            <div className="aspect-4/3 bg-black/[0.03] dark:bg-white/[0.04]">
                                                {work.kind === "video" ? (
                                                    <video src={mediaUrl(work.storageKey)} className="size-full object-cover" muted />
                                                ) : (
                                                    <img src={mediaUrl(work.storageKey)} alt={work.title} className="size-full object-cover" loading="lazy" />
                                                )}
                                            </div>
                                            <div className="p-3">
                                                <h3 className="truncate text-sm font-medium">{work.title}</h3>
                                                <p className="mt-1 text-xs text-stone-500 dark:text-stone-400">
                                                    {t("community.likeCountValue", { count: work.likeCount })} · {dayjs(work.createdAt).format("MM-DD")}
                                                </p>
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            )}
                        </div>
                        <div className="mt-8">
                            <Link to="/community" className="text-sm text-stone-500 hover:underline dark:text-stone-400">
                                {t("community.backToList")}
                            </Link>
                        </div>
                    </>
                ) : null}
            </div>
        </main>
    );
}
