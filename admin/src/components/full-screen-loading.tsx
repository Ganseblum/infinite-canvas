import { Spin } from "antd";

// 身份未定（bootstrap 静默刷新中）时的全屏占位：守卫、登录页、说明页共用一份。
export function FullScreenLoading() {
    return (
        <div className="flex h-dvh items-center justify-center bg-background text-foreground">
            <Spin size="large" />
        </div>
    );
}
