import { useState } from "react";
import { Alert, App, Button, Card, Input, Skeleton, Tag, Typography } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarCheck, Gift, Users } from "lucide-react";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage, ApiError } from "@/lib/api-error";
import { useCopyText } from "@/hooks/use-copy-text";
import { formatPoints } from "@/lib/credits-format";
import { bindInviteCode, checkin, getCheckinStatus, getInviteInfo } from "@/services/api/activity";

// 绑定邀请码的服务端校验原因在 error.fields 里（如「邀请码不存在」），
// 409 冲突的业务原因直接放在 message；两种都要优先于通用 i18n 文案展示。
function bindErrorMessage(error: unknown) {
    if (error instanceof ApiError) {
        const fieldReason = error.fields ? Object.values(error.fields).find(Boolean) : undefined;
        if (fieldReason) return fieldReason;
        if (error.status === 409 && error.message) return error.message;
    }
    return getApiErrorMessage(error);
}

export default function ActivityPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const copyText = useCopyText();
    const queryClient = useQueryClient();

    const checkinQuery = useQuery({ queryKey: ["activity", "checkin"], queryFn: ({ signal }) => getCheckinStatus(signal) });
    const inviteQuery = useQuery({ queryKey: ["activity", "invite"], queryFn: ({ signal }) => getInviteInfo(signal) });
    const [inviteCode, setInviteCode] = useState("");

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

    const bindInviteMutation = useMutation({
        mutationFn: () => bindInviteCode(inviteCode.trim()),
        onSuccess: async (result) => {
            message.success(
                result.inviteeRewardMicros > 0 ? `绑定成功，你获得 ${formatPoints(result.inviteeRewardMicros)} 点数` : "邀请码绑定成功",
            );
            setInviteCode("");
            await Promise.all([
                queryClient.invalidateQueries({ queryKey: ["activity"] }),
                queryClient.invalidateQueries({ queryKey: ["me"] }),
                queryClient.invalidateQueries({ queryKey: ["credits"] }),
            ]);
        },
        onError: (error) => message.error(bindErrorMessage(error)),
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
                                <div className="rounded-lg border border-dashed border-stone-200 px-3 py-2.5 dark:border-stone-700">
                                    <div className="text-xs text-stone-500 dark:text-stone-400">收到好友邀请？输入对方的邀请码完成绑定</div>
                                    <div className="mt-2 flex items-center gap-2">
                                        <Input
                                            size="small"
                                            value={inviteCode}
                                            maxLength={32}
                                            allowClear
                                            placeholder="填写好友的邀请码"
                                            onChange={(event) => setInviteCode(event.target.value)}
                                            onPressEnter={() => inviteCode.trim() && bindInviteMutation.mutate()}
                                        />
                                        <Button
                                            size="small"
                                            loading={bindInviteMutation.isPending}
                                            disabled={!inviteCode.trim()}
                                            onClick={() => bindInviteMutation.mutate()}
                                        >
                                            绑定邀请码
                                        </Button>
                                    </div>
                                    <p className="mt-1.5 text-xs text-stone-400 dark:text-stone-500">需已完成邮箱验证且注册满 1 小时，每个账号只能绑定一次。</p>
                                </div>
                            </div>
                        )}
                    </Card>
                </div>
            </div>
        </main>
    );
}
