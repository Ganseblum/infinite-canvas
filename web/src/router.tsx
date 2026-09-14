import { useEffect, type ReactNode } from "react";
import { Spin } from "antd";
import { createBrowserRouter, Navigate, Outlet, useLocation } from "react-router-dom";

import { AnalyticsTracker } from "@/components/layout/analytics-tracker";
import UserLayout from "@/layouts/user-layout";
import AssetsPage from "@/pages/assets";
import BillingPage from "@/pages/billing";
import CanvasPage from "@/pages/canvas";
import CanvasProjectPage from "@/pages/canvas/project";
import ConfigPage from "@/pages/config";
import HomePage from "@/pages/home";
import ImagePage from "@/pages/image";
import LoginPage from "@/pages/login";
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

export const router = createBrowserRouter([
    {
        element: <RootBootstrap />,
        children: [
            { path: "/login", element: <LoginPage /> },
            { path: "/verify-email", element: <VerifyEmailPage /> },
            { path: "/reset-password", element: <ResetPasswordPage /> },
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
                    { path: "/prompts", element: <PromptsPage /> },
                    { path: "/canvas", element: <CanvasPage /> },
                    { path: "/canvas/:id", element: <CanvasProjectPage /> },
                    { path: "/config", element: <ConfigPage /> },
                    { path: "/pricing", element: <PricingPage /> },
                    { path: "/profile", element: <ProfilePage /> },
                    { path: "/billing", element: <BillingPage /> },
                ],
            },
            { path: "*", element: <NotFound /> },
        ],
    },
]);
