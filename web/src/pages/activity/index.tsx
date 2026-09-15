import { Alert, App, Button, Card, Skeleton, Tag, Typography } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarCheck, Gift, Users } from "lucide-react";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { useCopyText } from "@/hooks/use-copy-text";
import { formatPoints } from "@/lib/credits-format";
import { checkin, getCheckinStatus, getInviteInfo } from "@/services/api/activity";

export default function ActivityPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const copyText = useCopyText();
    const queryClient = useQueryClient();

    const checkinQuery = useQuery({ queryKey: ["activity", "checkin"], queryFn: ({ signal }) => getCheckinStatus(signal) });
    const inviteQuery = useQuery({ queryKey: ["activity", "invite"], queryFn: ({ signal }) => getInviteInfo(signal) });

    const checkinMutation = useMutation({
        mutationFn: () => checkin(),
        onSuccess: async (result) => {
            message.success(t("activity.checkinSuccess", { points: formatPoints(result.rewardMicros), days: result.streakDays }));
            await Promise.all([
                queryClient.invalidateQueries({ queryKey: ["activity"] }),
                queryClient.invalidateQueries({ queryKey: ["me"] }),
                queryClient.invalidateQueries({ queryKey: ["credits"] }),
            ]);
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    return (
        <main className="h-full overflow-y-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-4xl px-4 py-10 sm:px-6">
                <h1 className="text-3xl font-semibold">{t("activity.title")}</h1>
                <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{t("activity.description")}</p>

                <div className="mt-8 grid gap-4 md:grid-cols-2">
                    <Card
                        title={
                            <span className="flex items-center gap-2">
                                <CalendarCheck className="size-4" />
                                {t("activity.checkinTitle")}
                            </span>
                        }
                    >
                        {checkinQuery.isError ? (
                            <Alert type="error" showIcon message={t("activity.loadFailed")} description={getApiErrorMessage(checkinQuery.error)} />
                        ) : checkinQuery.isPending || !checkinQuery.data ? (
                            <Skeleton active paragraph={{ rows: 3 }} />
                        ) : !checkinQuery.data.enabled ? (
                            <Typography.Text type="secondary">{t("activity.checkinDisabled")}</Typography.Text>
                        ) : (
                            <div className="flex flex-col gap-4">
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-semibold">{checkinQuery.data.streakDays}</span>
                                    <span className="text-sm text-stone-500 dark:text-stone-400">{t("activity.streakDays")}</span>
                                </div>
                                <p className="text-sm text-stone-500 dark:text-stone-400">
                                    {t("activity.checkinReward", { points: formatPoints(checkinQuery.data.rewardMicros) })}
                                </p>
                                {checkinQuery.data.checkedIn ? (
                                    <Tag color="success" className="m-0 w-fit">
                                        {t("activity.checkedInToday")}
                                    </Tag>
                                ) : (
                                    <Button type="primary" loading={checkinMutation.isPending} onClick={() => checkinMutation.mutate()}>
                                        {t("activity.checkinNow")}
                                    </Button>
                                )}
                            </div>
                        )}
                    </Card>

                    <Card
                        title={
                            <span className="flex items-center gap-2">
                                <Gift className="size-4" />
                                {t("activity.inviteTitle")}
                            </span>
                        }
                    >
                        {inviteQuery.isError ? (
                            <Alert type="error" showIcon message={t("activity.loadFailed")} description={getApiErrorMessage(inviteQuery.error)} />
                        ) : inviteQuery.isPending || !inviteQuery.data ? (
                            <Skeleton active paragraph={{ rows: 3 }} />
                        ) : !inviteQuery.data.enabled ? (
                            <Typography.Text type="secondary">{t("activity.inviteDisabled")}</Typography.Text>
                        ) : (
                            <div className="flex flex-col gap-4">
                                <div className="flex items-center gap-2">
                                    <Users className="size-4 text-stone-400" />
                                    <span className="text-sm text-stone-500 dark:text-stone-400">
                                        {t("activity.invitedCount", { count: inviteQuery.data.invitedCount })}
                                    </span>
                                </div>
                                <p className="text-sm text-stone-500 dark:text-stone-400">
                                    {t("activity.inviteReward", { points: formatPoints(inviteQuery.data.rewardPerInviteMicros) })}
                                </p>
                                <div className="rounded-lg bg-black/[0.03] px-3 py-2 dark:bg-white/[0.06]">
                                    <div className="text-xs text-stone-500 dark:text-stone-400">{t("activity.inviteCode")}</div>
                                    <div className="mt-1 flex items-center justify-between gap-3">
                                        <code className="min-w-0 truncate">{inviteQuery.data.code || t("activity.inviteCodeMissing")}</code>
                                        {inviteQuery.data.code ? (
                                            <Button
                                                size="small"
                                                type="text"
                                                onClick={() => copyText(inviteQuery.data.code, t("activity.inviteCodeCopied"))}
                                            >
                                                {t("activity.copy")}
                                            </Button>
                                        ) : null}
                                    </div>
                                </div>
                                <p className="text-xs text-stone-500 dark:text-stone-400">
                                    {t("activity.inviteTotal", { points: formatPoints(inviteQuery.data.rewardMicros) })}
                                </p>
                            </div>
                        )}
                    </Card>
                </div>
            </div>
        </main>
    );
}
