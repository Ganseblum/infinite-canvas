import { Button, Tooltip } from "antd";
import { ChevronDown } from "lucide-react";

import { canvasThemes } from "@/lib/canvas-theme";

/**
 * 「回到底部」悬浮按钮：绝对定位在滚动容器底部中间，供聊天时间线与日志视图复用。
 * 只负责展示与点击回调，跟随/置底的判断逻辑由调用方维护。
 */
export function AgentScrollToBottom({
    theme,
    title,
    ariaLabel = title,
    className = "",
    onClick,
}: {
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    title: string;
    ariaLabel?: string;
    className?: string;
    onClick: () => void;
}) {
    return (
        <Tooltip title={title} placement="top">
            <Button
                type="text"
                shape="circle"
                aria-label={ariaLabel}
                className={`!absolute bottom-6 left-1/2 z-10 !h-8 !w-8 !min-w-8 -translate-x-1/2 backdrop-blur transition hover:-translate-y-0.5 ${className}`}
                style={{ background: theme.toolbar.panel, border: `1px solid ${theme.node.stroke}`, color: theme.node.text }}
                icon={<ChevronDown className="size-4" />}
                onClick={onClick}
            />
        </Tooltip>
    );
}
