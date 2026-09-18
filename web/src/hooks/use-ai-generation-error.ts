import { createElement, useCallback, useEffect, useRef, useState } from "react";
import { App } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { ApiError, getApiErrorMessage } from "@/lib/api-error";

// 生成入口统一错误处理：402 引导充值，429 按 Retry-After 进入禁用倒计时，
// 5xx 说明点数已退回，其余走 apiErrors 文案；每次失败后刷新余额。
// onQuoteChanged 由调用方注入：报价在两轮之间再次变化时，由页面决定是否按新价重试。
export function useAiGenerationError() {
    const { message, modal } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const [retryAfterSeconds, setRetryAfterSeconds] = useState(0);
    const quoteNoticeShown = useRef(false);

    useEffect(() => {
        if (retryAfterSeconds <= 0) return;
        const timer = window.setTimeout(() => setRetryAfterSeconds((value) => Math.max(0, value - 1)), 1000);
        return () => window.clearTimeout(timer);
    }, [retryAfterSeconds]);

    const handleAiError = useCallback(
        (error: unknown) => {
            void queryClient.invalidateQueries({ queryKey: ["me"] });
            if (error instanceof ApiError && error.code === "INSUFFICIENT_CREDITS") {
                const shortfall = Math.max(0, Math.round(error.shortfallMicros || 0)).toLocaleString();
                modal.warning({
                    title: t("apiErrors.insufficientCredits"),
                    content: t("billing.shortfall", { points: shortfall }),
                    okText: t("profile.account.topUp"),
                    okCancel: false,
                    onOk: () => navigate("/billing"),
                });
                return;
            }
            if (error instanceof ApiError && (error.code === "CONCURRENCY_LIMITED" || error.code === "RATE_LIMITED")) {
                // 按钮进入按 Retry-After 秒数的禁用倒计时，避免用户连点。
                setRetryAfterSeconds(Math.max(1, Math.ceil(error.retryAfter || 5)));
                message.warning(t("workbench.generationBusy", { seconds: Math.max(1, Math.ceil(error.retryAfter || 5)) }));
                return;
            }
            if (error instanceof ApiError && error.code === "QUOTE_STALE") {
                // 生成服务已经静默重报过一次；仍失败说明价格或活动在两次之间又变了。
                // 这里只提示价格已更新，由用户再次点击生成（会重新报价），绝不自动按新价提交。
                if (quoteNoticeShown.current) return;
                quoteNoticeShown.current = true;
                modal.info({
                    title: t("workbench.quoteChangedTitle"),
                    content: t("workbench.quoteChangedHint"),
                    okText: t("workbench.quoteConfirm"),
                    onOk: () => {
                        quoteNoticeShown.current = false;
                    },
                });
                return;
            }
            if (error instanceof ApiError && error.code === "CONTENT_REJECTED") {
                // 输入或产物被拒：保留用户输入，提示修改后重试；产物拒绝不退点。
                modal.warning({
                    title: t("workbench.contentRejectedTitle"),
                    content: t("workbench.contentRejectedHint"),
                    okText: t("common.confirm"),
                    okCancel: false,
                });
                return;
            }
            if (error instanceof ApiError && error.code === "MODERATION_UNAVAILABLE") {
                message.error(t("workbench.moderationUnavailable"));
                return;
            }
            if (error instanceof ApiError && (error.code === "UPSTREAM_ERROR" || error.code === "UPSTREAM_TIMEOUT")) {
                message.error(t("workbench.refunded"));
                return;
            }
            if (error instanceof ApiError && error.code === "EMAIL_NOT_VERIFIED") {
                // 不只弹提示：附「去验证」链接跳到验证页（页内可重发验证邮件），与用户菜单的重发入口一致。
                // 本文件是 .ts 写不了 JSX，用 createElement 挂链接。
                message.error({
                    key: "email-not-verified",
                    duration: 8,
                    content: createElement(
                        "span",
                        null,
                        getApiErrorMessage(error),
                        " ",
                        createElement(Link, { className: "underline underline-offset-2", to: "/verify-email" }, "去验证"),
                    ),
                });
                return;
            }
            if (error instanceof ApiError) {
                message.error(getApiErrorMessage(error));
                return;
            }
            message.error(error instanceof Error && error.message ? error.message : t("apiErrors.operationFailed"));
        },
        [message, modal, navigate, queryClient, t],
    );

    return { handleAiError, retryAfterSeconds };
}
