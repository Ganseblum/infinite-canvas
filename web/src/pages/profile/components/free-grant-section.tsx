import { useState } from "react";
import { App, Button, Skeleton } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Gift } from "lucide-react";

import { getApiErrorMessage } from "@/lib/api-error";
import { claimFreeGrant, type FreeGrantClaim } from "@/services/api/account";

import { useMeQuery } from "../use-me";

// 免费体验额度：注册文案承诺「验证邮箱后即可领取」。领取接口幂等（已领取返回 200 + 既有结论），
// 但未领取时调用会产生领取/风控记录，所以不自动调用，只由用户点击触发。
export function FreeGrantSection() {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const meQuery = useMeQuery();
    const me = meQuery.data;
    const [claim, setClaim] = useState<FreeGrantClaim | null>(null);

    const claimMutation = useMutation({
        mutationFn: () => claimFreeGrant(),
        onSuccess: async (result) => {
            if (result.status === "granted") {
                setClaim(result);
                message.success(`领取成功：图片生成体验 ${result.imageTrials} 次、视频生成体验 ${result.videoTrials} 次`);
            } else {
                message.error("免费体验额度暂时无法领取");
            }
            await queryClient.invalidateQueries({ queryKey: ["me"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const imageRemaining = claim ? Math.max(0, claim.imageTrials - (me?.usage?.freeImageTrialsUsed ?? 0)) : 0;
    const videoRemaining = claim ? Math.max(0, claim.videoTrials - (me?.usage?.freeVideoTrialsUsed ?? 0)) : 0;

    return (
        <section id="profile-free-grant" className="scroll-mt-4 rounded-xl border border-stone-200 p-6 dark:border-stone-800">
            <h2 className="flex items-center gap-2 text-lg font-semibold">
                <Gift className="size-4" />
                免费体验额度
            </h2>
            {meQuery.isPending ? (
                <Skeleton className="mt-4" active paragraph={{ rows: 1 }} />
            ) : claim ? (
                <>
                    <p className="mt-3 text-sm text-stone-500 dark:text-stone-400">已领取免费体验额度，剩余次数如下：</p>
                    <div className="mt-4 grid gap-3 sm:grid-cols-2">
                        <div className="rounded-lg bg-black/[0.03] px-4 py-3 dark:bg-white/[0.06]">
                            <p className="text-xs text-stone-500 dark:text-stone-400">图片生成体验</p>
                            <p className="mt-1 text-lg font-semibold">{imageRemaining} 次</p>
                        </div>
                        <div className="rounded-lg bg-black/[0.03] px-4 py-3 dark:bg-white/[0.06]">
                            <p className="text-xs text-stone-500 dark:text-stone-400">视频生成体验</p>
                            <p className="mt-1 text-lg font-semibold">{videoRemaining} 次</p>
                        </div>
                    </div>
                </>
            ) : (
                <>
                    <p className="mt-3 text-sm text-stone-500 dark:text-stone-400">验证邮箱后即可领取免费体验额度，用于免费体验图片与视频生成。</p>
                    <Button className="mt-4" type="primary" loading={claimMutation.isPending} onClick={() => claimMutation.mutate()}>
                        领取免费体验额度
                    </Button>
                </>
            )}
        </section>
    );
}
