import { useEffect, type ReactNode } from "react";
import { Spin } from "antd";
import { createBrowserRouter, Navigate, Outlet, useLocation } from "react-router-dom";

import { AnalyticsTracker } from "@/components/layout/analytics-tracker";
import { RouteErrorBoundary } from "@/components/error-boundary";
import UserLayout from "@/layouts/user-layout";
import ActivityPage from "@/pages/activity";
import AssetsPage from "@/pages/assets";
import CommunityPage from "@/pages/community";
import CommunityUserPage from "@/pages/community/user";
import BillingPage from "@/pages/billing";
import CanvasPage from "@/pages/canvas";
import CanvasProjectPage from "@/pages/canvas/project";
import ConfigPage from "@/pages/config";
import FeedbackPage from "@/pages/feedback";
import HomePage from "@/pages/home";
import ImagePage from "@/pages/image";
import LoginPage from "@/pages/login";
import OAuthAuthorizePage from "@/pages/oauth-authorize";
import PrivacyPage from "@/pages/legal/privacy";
import TermsPage from "@/pages/legal/terms";
import ModelsPage from "@/pages/models";
import NotFound from "@/pages/not-found";
import PricingPage from "@/pages/pricing";
import ProfilePage from "@/pages/profile";
import PromptsPage from "@/pages/prompts";
import ResetPasswordPage from "@/pages/reset-password";
import VerifyEmailPage from "@/pages/verify-email";
import VideoPage from "@/pages/video";
import { useAuthStore } from "@/stores/use-auth-store";

function FullScreenLoading() {
    return (
        <div className="flex h-dvh items-center justify-center bg-background text-foreground">
            <Spin size="large" />
        </div>
    );
}

// 应用挂载后只执行一次 bootstrap；store 内部有模块级单飞守卫。
function RootBootstrap() {
    const bootstrap = useAuthStore((state) => state.bootstrap);

    useEffect(() => {
        void bootstrap();
    }, [bootstrap]);

    return <Outlet />;
}

function RequireAuth({ children }: { children: ReactNode }) {
    const status = useAuthStore((state) => state.status);
    const location = useLocation();

    // booting 期间既不放行也不跳转，否则已登录用户刷新页面会闪现登录页。
    if (status === "booting") return <FullScreenLoading />;
    if (status === "unauthenticated") return <Navigate to="/login" replace state={{ from: `${location.pathname}${location.search}` }} />;
    return <>{children}</>;
}

// 管理后台已拆到独立应用（admin/ 目录、独立域名），主站不再挂 /admin/* 路由：
// 主域名下的 /admin 与 /admin/xxx 都会落到下面的 NotFound，不再有任何后台入口。
export const router = createBrowserRouter([
    {
        element: <RootBootstrap />,
        // 任一路由渲染抛错时兜底展示，避免落到 React Router 默认英文错误页。
        errorElement: <RouteErrorBoundary />,
        children: [
            { path: "/login", element: <LoginPage /> },
            { path: "/verify-email", element: <VerifyEmailPage /> },
            { path: "/reset-password", element: <ResetPasswordPage /> },
            // OIDC 浏览器直跳承接页：公开路由自带登录分支，不进统一登录守卫。
            { path: "/oauth/authorize", element: <OAuthAuthorizePage /> },
            // 条款与隐私页公开可访问，不进登录守卫（差异清单 #113）。
            { path: "/terms", element: <TermsPage /> },
            { path: "/privacy", element: <PrivacyPage /> },
            {
                element: (
                    <RequireAuth>
                        <UserLayout>
                            <AnalyticsTracker />
                            <Outlet />
                        </UserLayout>
                    </RequireAuth>
                ),
                children: [
                    { path: "/", element: <HomePage /> },
                    { path: "/image", element: <ImagePage /> },
                    { path: "/video", element: <VideoPage /> },
                    { path: "/models", element: <ModelsPage /> },
                    { path: "/assets", element: <AssetsPage /> },
                    { path: "/community", element: <CommunityPage /> },
                    { path: "/community/users/:id", element: <CommunityUserPage /> },
                    { path: "/activity", element: <ActivityPage /> },
                    { path: "/prompts", element: <PromptsPage /> },
                    { path: "/canvas", element: <CanvasPage /> },
                    { path: "/canvas/:id", element: <CanvasProjectPage /> },
                    { path: "/config", element: <ConfigPage /> },
                    { path: "/feedback", element: <FeedbackPage /> },
                    { path: "/pricing", element: <PricingPage /> },
                    { path: "/profile", element: <ProfilePage /> },
                    { path: "/billing", element: <BillingPage /> },
                ],
            },
            { path: "*", element: <NotFound /> },
        ],
    },
]);
