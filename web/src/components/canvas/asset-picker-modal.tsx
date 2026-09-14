import { useEffect, useState } from "react";
import { Empty, Input, Modal, Pagination, Tag } from "antd";
import { Search } from "lucide-react";
import { useTranslation } from "react-i18next";

import { assetHeight, assetText, assetUrl, assetWidth } from "@/lib/asset";
import { cn } from "@/lib/utils";
import { useAssetSearch } from "@/hooks/use-asset-library";
import type { AssetItem, AssetKind } from "@/services/data/types";

export type InsertAssetPayload = { kind: "text"; content: string; title: string } | { kind: "image"; dataUrl: string; title: string; storageKey?: string } | { kind: "video"; url: string; title: string; storageKey?: string; width?: number; height?: number };

type Props = {
    open: boolean;
    defaultTab?: string;
    onInsert: (payload: InsertAssetPayload) => void;
    onClose: () => void;
};

export function AssetPickerModal({ open, onInsert, onClose }: Props) {
    const { t } = useTranslation();
    return (
        <Modal title={t("canvas.assetPicker.title")} open={open} onCancel={onClose} footer={null} width={860} destroyOnHidden styles={{ body: { padding: "0 24px 24px", minHeight: 480 } }}>
            <MyAssetsTab onInsert={onInsert} />
        </Modal>
    );
}

const PAGE_SIZE = 8;

const kindOptions: Array<"all" | AssetKind> = ["all", "text", "image", "video"];

function PickerCard({ title, kind, cover, onClick }: { title: string; kind: string; cover: string; onClick: () => void }) {
    const { t } = useTranslation();
    return (
        <button
            type="button"
            className="group relative cursor-pointer overflow-hidden rounded-lg border border-stone-200 bg-white text-left transition hover:border-stone-400 hover:shadow-md dark:border-stone-700 dark:bg-stone-900 dark:hover:border-stone-500"
            onClick={onClick}
        >
            {cover ? (
                <img src={cover} alt={title} className="aspect-[4/3] w-full object-cover" />
            ) : (
                <div className="flex aspect-[4/3] items-center justify-center bg-stone-100 p-3 text-center text-xs leading-5 text-stone-500 dark:bg-stone-800 dark:text-stone-400">{title}</div>
            )}
            <div className="p-2.5">
                <div className="flex items-center justify-between gap-2">
                    <span className="line-clamp-1 text-xs font-medium text-stone-800 dark:text-stone-200">{title}</span>
                    <Tag className="m-0 shrink-0 text-[10px]">{t(`assets.kinds.${kind}`)}</Tag>
                </div>
            </div>
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-stone-950/0 text-sm font-medium text-white opacity-0 transition group-hover:bg-stone-950/55 group-hover:opacity-100">{t("canvas.assetPicker.insert")}</div>
        </button>
    );
}

function MyAssetsTab({ onInsert }: { onInsert: (payload: InsertAssetPayload) => void }) {
    const { t } = useTranslation();
    const [keyword, setKeyword] = useState("");
    const [query, setQuery] = useState("");
    const [kind, setKind] = useState<"all" | AssetKind>("all");
    const [page, setPage] = useState(1);
    const assetsQuery = useAssetSearch({ q: query || undefined, kind: kind === "all" ? undefined : kind, page, size: PAGE_SIZE });
    const items = assetsQuery.data?.items || [];
    const total = assetsQuery.data?.total || 0;

    // 关键字交给服务端筛选，输入停下 300 毫秒再发请求。
    useEffect(() => {
        const timer = setTimeout(() => {
            setQuery(keyword.trim());
            setPage(1);
        }, 300);
        return () => clearTimeout(timer);
    }, [keyword]);

    const handleInsert = (asset: AssetItem) => {
        if (asset.kind === "text") {
            onInsert({ kind: "text", content: assetText(asset), title: asset.title });
            return;
        }
        if (asset.kind === "video") {
            onInsert({ kind: "video", url: assetUrl(asset), storageKey: asset.storageKey, title: asset.title, width: assetWidth(asset), height: assetHeight(asset) });
            return;
        }
        onInsert({ kind: "image", dataUrl: assetUrl(asset), storageKey: asset.storageKey, title: asset.title });
    };

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3">
                <Input
                    className="w-56"
                    size="small"
                    prefix={<Search className="size-3.5 text-stone-400" />}
                    placeholder={t("canvas.assetPicker.search")}
                    value={keyword}
                    allowClear
                    onChange={(e) => setKeyword(e.target.value)}
                />
                <div className="flex gap-1.5">
                    {kindOptions.map((option) => (
                        <Tag.CheckableTag
                            key={option}
                            checked={kind === option}
                            className={cn("prompt-filter-tag", kind === option && "is-active")}
                            onChange={() => {
                                setPage(1);
                                setKind(option);
                            }}
                        >
                            {option === "all" ? t("common.all") : t(`assets.kinds.${option}`)}
                        </Tag.CheckableTag>
                    ))}
                </div>
            </div>

            {items.length ? (
                <div className="grid grid-cols-4 gap-3">
                    {items.map((asset) => (
                        <PickerCard key={asset.id} title={asset.title} kind={asset.kind} cover={asset.kind === "text" ? "" : assetUrl(asset)} onClick={() => handleInsert(asset)} />
                    ))}
                </div>
            ) : (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={assetsQuery.isPending ? t("common.loading") : t("canvas.assetPicker.empty")} className="py-12" />
            )}

            {total > PAGE_SIZE && (
                <div className="flex justify-center">
                    <Pagination size="small" current={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} showSizeChanger={false} />
                </div>
            )}
        </div>
    );
}
