import type { ReactNode } from "react";

// 未登录 / 非管理员这类「进不去」的场景共用一张说明卡片：只讲清状态与下一步，不放后台外壳。
export function NoticePage({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
    return (
        <div className="flex h-dvh items-center justify-center bg-background px-4 text-stone-950 dark:text-stone-100">
            <div className="w-full max-w-md rounded-xl border border-stone-200 p-6 dark:border-stone-800">
                <h1 className="text-lg font-semibold">{title}</h1>
                <p className="mt-2 text-sm text-stone-500 dark:text-stone-400">{description}</p>
                {action ? <div className="mt-4">{action}</div> : null}
            </div>
        </div>
    );
}
