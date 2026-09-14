import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { ProConfigProvider } from "@ant-design/pro-components";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App, ConfigProvider } from "antd";
import enUS from "antd/es/locale/en_US";
import zhCN from "antd/es/locale/zh_CN";
import dayjs from "dayjs";
import "dayjs/locale/zh-cn";
import { useTranslation } from "react-i18next";

import { ClientRootInit } from "@/components/layout/client-root-init";
import type { AppLocale } from "@/i18n";
import { getAntThemeConfig } from "@/lib/app-theme";
import { useAuthStore } from "@/stores/use-auth-store";
import { useThemeStore } from "@/stores/use-theme-store";

const queryClient = new QueryClient({
    defaultOptions: {
        queries: {
            staleTime: 30_000,
            retry: false,
            refetchOnWindowFocus: false,
        },
    },
});

// 与账号绑定的查询键前缀。这些查询的 key 已带上 userId，身份变化时按前缀整体移除，
// 避免 A 登出后 B 登录仍看到 A 的邮箱、点数、订单、画布与素材。只清这些键，公共数据（档位、模型、提示词等）缓存不受影响。
const USER_SCOPED_QUERY_KEY_PREFIXES = [["me"], ["credits"], ["orders"], ["order"], ["canvases"], ["canvas"], ["assets"], ["generations"]];

export function AppProviders({ children }: { children: ReactNode }) {
    const { i18n, t } = useTranslation();
    const theme = useThemeStore((state) => state.theme);
    const dark = theme === "dark";
    const locale = i18n.resolvedLanguage as AppLocale;
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const status = useAuthStore((state) => state.status);
    const knownUserId = useRef<string | null | undefined>(undefined);

    // booting 阶段身份未定，不建立基线也不清理；未登录态没有用户态缓存，进入新身份时也只记基线，
    // 避免中断刚发出的首屏请求。只有从一个真实账号切到另一个身份（登出、直接换号）才按前缀清理上一账号缓存。
    useEffect(() => {
        if (status === "booting") return;
        const known = knownUserId.current;
        if (known === userId || known === undefined || known === null) {
            knownUserId.current = userId;
            return;
        }
        knownUserId.current = userId;
        for (const queryKey of USER_SCOPED_QUERY_KEY_PREFIXES) {
            void queryClient.cancelQueries({ queryKey });
            queryClient.removeQueries({ queryKey });
        }
    }, [status, userId]);

    useEffect(() => {
        document.documentElement.classList.toggle("dark", dark);
        document.documentElement.style.colorScheme = theme;
    }, [dark, theme]);

    useEffect(() => {
        document.documentElement.lang = locale;
        document.title = t("meta.title");
        document.querySelector('meta[name="description"]')?.setAttribute("content", t("meta.description"));
        dayjs.locale(locale === "zh-CN" ? "zh-cn" : "en");
    }, [locale, t]);

    return (
        <ConfigProvider locale={locale === "zh-CN" ? zhCN : enUS} theme={getAntThemeConfig(dark)}>
            <ProConfigProvider dark={dark}>
                <App>
                    <QueryClientProvider client={queryClient}>
                        <ClientRootInit>{children}</ClientRootInit>
                    </QueryClientProvider>
                </App>
            </ProConfigProvider>
        </ConfigProvider>
    );
}
