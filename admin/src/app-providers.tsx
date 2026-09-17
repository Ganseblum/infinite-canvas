import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App, ConfigProvider } from "antd";
import enUS from "antd/es/locale/en_US";
import zhCN from "antd/es/locale/zh_CN";
import dayjs from "dayjs";
import "dayjs/locale/zh-cn";
import { useTranslation } from "react-i18next";

import type { AppLocale } from "@/i18n";
import { getAntThemeConfig } from "@/lib/app-theme";
import { useAuthStore } from "@/stores/use-auth-store";
import { useThemeStore } from "@/stores/use-theme-store";

// admin 自己的一份精简 providers：不引 web 的 AppProviders——它挂着 ClientRootInit，
// 会拉进提示词来源调度与模型目录 store 一整串业务依赖，后台用不到。
const queryClient = new QueryClient({
    defaultOptions: {
        queries: {
            staleTime: 30_000,
            retry: false,
            refetchOnWindowFocus: false,
        },
    },
});

export function AppProviders({ children }: { children: ReactNode }) {
    const { t, i18n } = useTranslation();
    const theme = useThemeStore((state) => state.theme);
    const dark = theme === "dark";
    const locale: AppLocale = i18n.resolvedLanguage === "en-US" ? "en-US" : "zh-CN";
    const status = useAuthStore((state) => state.status);
    const userId = useAuthStore((state) => state.user?.id ?? null);
    const knownUserId = useRef<string | null | undefined>(undefined);

    // 后台缓存全是登录态下的数据，没有 web 那种「公共数据不受影响」的区分，所以身份一变整体清掉。
    // booting 阶段身份未定不清理；首次确定身份只记基线，避免打断刚发出的首屏请求。
    useEffect(() => {
        if (status === "booting") return;
        const known = knownUserId.current;
        knownUserId.current = userId;
        if (known === undefined || known === null || known === userId) return;
        queryClient.clear();
    }, [status, userId]);

    useEffect(() => {
        document.documentElement.classList.toggle("dark", dark);
        document.documentElement.style.colorScheme = theme;
    }, [dark, theme]);

    useEffect(() => {
        document.documentElement.lang = locale;
        document.title = t("docTitle", { ns: "admin" });
        dayjs.locale(locale === "zh-CN" ? "zh-cn" : "en");
    }, [locale, t]);

    return (
        <ConfigProvider locale={locale === "zh-CN" ? zhCN : enUS} theme={getAntThemeConfig(dark)}>
            <App>
                <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
            </App>
        </ConfigProvider>
    );
}
