import { Copy, FileText, FolderPlus } from "lucide-react";
import { Button, Modal, Space, Tag } from "antd";
import { useTranslation } from "react-i18next";

import { formatPromptDate, type Prompt } from "@/services/api/prompts";

/** 提示词详情弹窗：封面、参考图缩略、标签、描述与正文预览，底部提供复制/存入素材操作。
 * 弹窗体固定高度，中间内容区自行滚动，保证底部操作按钮始终可见。
 * @param prompt 当前展示的提示词；为 null 时弹窗关闭
 * @param onClose 关闭弹窗回调
 * @param onCopy 复制提示词正文的回调
 * @param onSaveAsset 存入素材库回调；不传则隐藏该按钮
 */
export function PromptDetailDialog({ prompt, onClose, onCopy, onSaveAsset }: { prompt: Prompt | null; onClose: () => void; onCopy: (prompt: string) => void; onSaveAsset?: (prompt: Prompt) => void }) {
    const { i18n, t } = useTranslation();

    return (
        <Modal title={prompt?.title} open={Boolean(prompt)} onCancel={onClose} footer={null} width={720} centered styles={{ body: { height: "calc(85vh - 55px)", overflow: "hidden" } }}>
            {prompt ? (
                <div className="flex h-full min-h-0 flex-col">
                    <div className="shrink-0 space-y-3 pb-4">
                        {prompt.coverUrl ? <img src={prompt.coverUrl} alt={prompt.title} className="h-48 w-full rounded-lg object-cover sm:h-56" /> : <div className="grid h-48 w-full place-items-center rounded-lg bg-stone-100 text-stone-400 dark:bg-stone-900 dark:text-stone-600 sm:h-56"><FileText className="size-9" /></div>}
                        {/* 参考图多于一张时展示缩略行：过滤掉与封面重复的一张，最多再取 6 张。 */}
                        {prompt.referenceImageUrls.length > 1 ? <div className="grid grid-cols-6 gap-2">{prompt.referenceImageUrls.filter((url) => url !== prompt.coverUrl).slice(0, 6).map((url) => <img key={url} src={url} alt="" className="aspect-square w-full rounded-md object-cover" loading="lazy" />)}</div> : null}
                    </div>
                    <div className="min-h-0 min-w-0 flex-1 overflow-y-auto border-y border-stone-200 py-4 pr-2 dark:border-stone-800">
                        <div className="flex flex-wrap gap-1.5">
                            {prompt.tags.map((tag) => (
                                <Tag key={tag} className="m-0">
                                    {tag}
                                </Tag>
                            ))}
                        </div>
                        {prompt.description ? <p className="mt-4 text-sm leading-6 text-stone-500 dark:text-stone-400">{prompt.description}</p> : null}
                        {prompt.preview ? <pre className="mt-4 whitespace-pre-wrap rounded-lg bg-stone-100 p-3 text-xs leading-5 text-stone-600 dark:bg-stone-900 dark:text-stone-300">{prompt.preview}</pre> : null}
                        <p className="mt-4 whitespace-pre-wrap text-sm leading-7 text-stone-800 dark:text-stone-300">{prompt.prompt}</p>
                        {prompt.createdAt || prompt.updatedAt ? <div className="mt-4 text-xs text-stone-500 dark:text-stone-400">{prompt.createdAt ? t("common.created", { date: formatPromptDate(prompt.createdAt, i18n.resolvedLanguage) }) : null}{prompt.createdAt && prompt.updatedAt ? " · " : null}{prompt.updatedAt ? t("common.updated", { date: formatPromptDate(prompt.updatedAt, i18n.resolvedLanguage) }) : null}</div> : null}
                    </div>
                    <div className="shrink-0 pt-4">
                        <Space wrap>
                            <Button type="primary" icon={<Copy className="size-4" />} onClick={() => onCopy(prompt.prompt)}>
                                {t("common.copyPrompt")}
                            </Button>
                            {onSaveAsset ? (
                                <Button icon={<FolderPlus className="size-4" />} onClick={() => onSaveAsset(prompt)}>
                                    {t("common.addToAssets")}
                                </Button>
                            ) : null}
                        </Space>
                    </div>
                </div>
            ) : null}
        </Modal>
    );
}
