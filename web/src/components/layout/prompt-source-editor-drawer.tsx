import { App, Button, Drawer, Input, Space, Switch } from "antd";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import type { PromptSource } from "@/services/api/prompt-source-presets";

/**
 * 提示词源编辑抽屉：新增或编辑一个远程 JSON 提示词源（内置源不可编辑）。
 * 保存前校验名称必填、URL 与主页均为 http(s) 地址；draft 为 null 时不渲染。
 * @param source 被编辑的源；null 表示新增模式（drawer 仍以 open 控制显隐）
 * @param onSave 校验通过后回传整理过的源数据
 */
export function PromptSourceEditorDrawer({ open, source, onSave, onClose }: { open: boolean; source: PromptSource | null; onSave: (source: PromptSource) => void; onClose: () => void }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const [draft, setDraft] = useState<PromptSource | null>(source);

    useEffect(() => {
        if (open && source) setDraft(source);
    }, [open, source]);

    if (!draft) return null;

    const patch = (value: Partial<PromptSource>) => setDraft((current) => (current ? { ...current, ...value } : current));

    const save = () => {
        const name = draft.name.trim();
        const url = draft.url.trim();
        if (!name) return message.warning(t("config.promptSources.editor.nameRequired"));
        if (!isHttpUrl(url)) return message.warning(t("config.promptSources.editor.invalidUrl"));
        if (draft.homepage.trim() && !isHttpUrl(draft.homepage.trim())) return message.warning(t("config.promptSources.editor.invalidHomepage"));
        onSave({ ...draft, name, url, homepage: draft.homepage.trim(), builtIn: false });
        onClose();
    };

    return (
        <Drawer
            open={open}
            width={560}
            title={t(source?.name ? "config.promptSources.editor.editTitle" : "config.promptSources.editor.addTitle")}
            onClose={onClose}
            styles={{ body: { paddingTop: 16 } }}
            extra={
                <Space>
                    <Button onClick={onClose}>{t("common.cancel")}</Button>
                    <Button type="primary" onClick={save}>
                        {t("common.save")}
                    </Button>
                </Space>
            }
        >
            <div className="space-y-5">
                <label className="block">
                    <span className="mb-1.5 block text-sm font-medium">{t("config.promptSources.editor.name")}</span>
                    <Input value={draft.name} onChange={(event) => patch({ name: event.target.value })} placeholder={t("config.promptSources.editor.namePlaceholder")} />
                </label>
                <label className="block">
                    <span className="mb-1.5 block text-sm font-medium">JSON URL</span>
                    <Input value={draft.url} onChange={(event) => patch({ url: event.target.value })} placeholder="https://example.com/prompts.json" />
                </label>
                <label className="block">
                    <span className="mb-1.5 block text-sm font-medium">{t("config.promptSources.editor.homepage")}</span>
                    <Input value={draft.homepage} onChange={(event) => patch({ homepage: event.target.value })} placeholder="https://example.com" />
                </label>
                <div className="flex items-center justify-between border-y border-stone-200 py-3 dark:border-stone-800">
                    <span className="text-sm font-medium">{t("config.promptSources.editor.enabled")}</span>
                    <Switch checked={draft.enabled} onChange={(enabled) => patch({ enabled })} />
                </div>
                <div>
                    <div className="mb-2 text-sm font-medium">{t("config.promptSources.editor.jsonFormat")}</div>
                    <pre className="overflow-x-auto rounded-md bg-stone-100 p-3 text-xs leading-5 text-stone-600 dark:bg-stone-900 dark:text-stone-300">{`[
  {
    "id": "product-photo-1",
    "title": "Product photo",
    "prompt": "Generate a professional product photo on a white background",
    "description": "",
    "coverUrl": "",
    "referenceImageUrls": [],
    "tags": ["product", "photography"]
  }
]`}</pre>
                </div>
            </div>
        </Drawer>
    );
}

/** 校验为合法的 http/https URL（new URL 也接受 ftp: 等协议，这里显式限定）。 */
function isHttpUrl(value: string) {
    try {
        return ["http:", "https:"].includes(new URL(value).protocol);
    } catch {
        return false;
    }
}
