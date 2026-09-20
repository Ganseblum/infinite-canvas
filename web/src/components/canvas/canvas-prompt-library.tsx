import { useState } from "react";
import { Button, Tooltip } from "antd";
import { BookOpen } from "lucide-react";
import { useTranslation } from "react-i18next";

import { PromptSelectDialog } from "@/components/prompts/prompt-select-dialog";
import { canvasThemes } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";

/** 提示词库入口按钮：点击打开提示词选择弹窗，选中后把提示词文本回传 onSelect。 */
export function CanvasPromptLibrary({ onSelect }: { onSelect: (prompt: string) => void }) {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);
    const theme = canvasThemes[useThemeStore((state) => state.theme)];

    return (
        <>
            <Tooltip title={t("navigation.prompts")}>
                <Button
                    type="text"
                    className="!h-8 !w-8 !min-w-8 shrink-0 !rounded-full !bg-transparent !p-0"
                    style={{ color: theme.node.text }}
                    icon={<BookOpen className="size-3.5" />}
                    onClick={() => setOpen(true)}
                    aria-label={t("navigation.prompts")}
                />
            </Tooltip>
            <PromptSelectDialog open={open} onOpenChange={setOpen} onSelect={onSelect} />
        </>
    );
}
