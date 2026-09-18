import { useEffect, useId, useState } from "react";
import { Cpu, RotateCcw } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select";
import { estimateModelPoints, useModelOptions } from "@/hooks/use-model-catalog";
import { useModelCatalogStore } from "@/stores/use-model-catalog-store";
import { cn } from "@/lib/utils";
import type { CatalogModel, ModelCapability } from "@/services/api/catalog";

type ModelPickerProps = {
    value?: string;
    onChange: (model: string) => void;
    capability?: ModelCapability;
    className?: string;
    fullWidth?: boolean;
    placeholder?: string;
};

export function ModelPicker({ value, onChange, capability, className, fullWidth = false, placeholder }: ModelPickerProps) {
    const { t } = useTranslation();
    const pickerId = useId();
    const [open, setOpen] = useState(false);
    const options = useModelOptions(capability);
    const status = useModelCatalogStore((state) => state.status);
    const reloadModels = useModelCatalogStore((state) => state.load);
    const current = value || "";
    const currentModel = options.find((model) => model.id === current);
    const pickerPlaceholder = placeholder || t("settingsPanels.model.select");

    useEffect(() => {
        const closeOtherPicker = (event: Event) => {
            if ((event as CustomEvent<string>).detail !== pickerId) setOpen(false);
        };
        window.addEventListener("model-picker-open", closeOtherPicker);
        return () => window.removeEventListener("model-picker-open", closeOtherPicker);
    }, [pickerId]);

    return (
        <Select
            open={open}
            value={current}
            onOpenChange={(nextOpen) => {
                if (nextOpen) window.dispatchEvent(new CustomEvent("model-picker-open", { detail: pickerId }));
                setOpen(nextOpen);
            }}
            onValueChange={onChange}
        >
            <SelectTrigger
                className={cn(
                    "canvas-composer-model-picker h-8 w-fit max-w-full gap-2 rounded-full border border-input bg-transparent px-3 text-sm font-normal shadow-sm transition-colors",
                    fullWidth ? "w-full min-w-0 justify-start" : "min-w-[9rem] justify-start",
                    "data-[state=open]:border-ring data-[state=open]:ring-2 data-[state=open]:ring-ring/20",
                    className,
                )}
                onMouseDown={(event) => event.stopPropagation()}
                onPointerDown={(event) => event.stopPropagation()}
                title={currentModel ? modelLabel(currentModel) : pickerPlaceholder}
            >
                <ModelIcon model={currentModel} />
                <span className="canvas-model-picker-text min-w-0 flex-1 truncate text-left">{currentModel ? modelLabel(currentModel) : pickerPlaceholder}</span>
                <ModelPoints model={currentModel} />
            </SelectTrigger>
            <SelectContent
                data-canvas-no-zoom
                className="z-[1200] w-80 max-w-[calc(100vw-24px)] rounded-xl border border-border/70 bg-popover p-1 shadow-xl"
                position="popper"
                align="start"
                side="bottom"
                sideOffset={6}
                onPointerDown={(event) => event.stopPropagation()}
                onMouseDown={(event) => event.stopPropagation()}
            >
                {options.length ? (
                    options.map((model) => (
                        <SelectItem key={model.id} value={model.id} textValue={modelLabel(model)}>
                            <span className="flex min-w-0 flex-1 items-center gap-2">
                                <ModelIcon model={model} />
                                <span className="min-w-0 flex-1 truncate">{model.displayName || model.id}</span>
                                <ModelPoints model={model} />
                            </span>
                        </SelectItem>
                    ))
                ) : status === "error" ? (
                    <div className="flex flex-col items-center gap-2 px-3 py-3 text-center">
                        <span className="text-sm text-muted-foreground">{t("settingsPanels.model.loadFailed")}</span>
                        <button
                            type="button"
                            className="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-accent"
                            onMouseDown={(event) => event.stopPropagation()}
                            onClick={(event) => {
                                event.stopPropagation();
                                void reloadModels();
                            }}
                        >
                            <RotateCcw className="size-3.5" />
                            {t("common.retry")}
                        </button>
                    </div>
                ) : (
                    <SelectItem value="__empty__" disabled>
                        {t("settingsPanels.model.empty")}
                    </SelectItem>
                )}
            </SelectContent>
        </Select>
    );
}

function modelLabel(model: CatalogModel) {
    return model.displayName || model.id;
}

function ModelPoints({ model }: { model?: CatalogModel }) {
    const { t } = useTranslation();
    const points = model ? estimateModelPoints(model) : undefined;
    if (typeof points !== "number" || !Number.isFinite(points)) return null;
    return <span className="shrink-0 text-xs text-muted-foreground">{t("settingsPanels.model.points", { points: Math.round(points).toLocaleString() })}</span>;
}

function ModelIcon({ model }: { model?: CatalogModel }) {
    if (!model) return <Cpu className="size-4 shrink-0 opacity-70" />;
    const icon = resolveModelIcon(`${model.id} ${model.provider}`);
    return icon ? <img src={icon} alt="" className="size-4 shrink-0 dark:invert" /> : <Cpu className="size-4 shrink-0 opacity-70" />;
}

function resolveModelIcon(model: string) {
    const name = model.toLowerCase();
    if (name.includes("claude") || name.includes("anthropic")) return "/icons/claude.svg";
    if (name.includes("gemini") || name.includes("google")) return "/icons/gemini.svg";
    if (name.includes("gpt") || name.includes("openai")) return "/icons/openai.svg";
    if (name.includes("grok")) return "/icons/grok.svg";
    if (name.includes("deepseek")) return "/icons/deepseek.svg";
    if (name.includes("glm")) return "/icons/glm.svg";
    return "";
}
