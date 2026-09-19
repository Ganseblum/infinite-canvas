import { Copy, Download, PencilLine, Search, Share2, Trash2, Upload } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { App, Button, Card, Drawer, Empty, Form, Image, Input, Modal, Pagination, Select, Space, Tag, Typography } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { saveAs } from "file-saver";
import { useTranslation } from "react-i18next";

import { useAssetSearch } from "@/hooks/use-asset-library";
import { useCopyText } from "@/hooks/use-copy-text";
import { useMediaDownload } from "@/hooks/use-media-download";
import { assetCoverUrl, assetHeight, assetMimeType, assetNote, assetSource, assetText, assetUrl, assetWidth } from "@/lib/asset";
import { getApiErrorMessage } from "@/lib/api-error";
import { peekRemixSource, setRemixSource as persistRemixSource, type RemixSource } from "@/lib/remix-source";
import { deleteImagePreview } from "@/services/image-preview";
import { formatBytes, readFileAsDataUrl } from "@/lib/image-utils";
import { createAsset, deleteAsset, patchAsset } from "@/services/api/assets";
import { publishCommunityWork, getPublicSettings } from "@/services/api/community";
import type { AssetItem, AssetKind } from "@/services/data/types";
import { uploadImage } from "@/services/media-ingest";
import { cn } from "@/lib/utils";
import { exportAssets, readAssetPackage } from "./asset-transfer";

type AssetFormValues = {
    kind: AssetKind;
    title: string;
    coverUrl: string;
    tags: string[];
    source?: string;
    note?: string;
    content?: string;
};

type ImageDraft = { url: string; storageKey?: string; width: number; height: number; bytes: number; mimeType: string } | null;

const kindOptions = ["all", "text", "image", "video"] as const;
const PAGE_SIZES = [10, 20, 50, 100];

export default function AssetsPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const copyText = useCopyText();
    const { download: downloadMedia, isDownloading } = useMediaDownload();
    const queryClient = useQueryClient();
    const [form] = Form.useForm<AssetFormValues>();
    const [publishForm] = Form.useForm<{ title: string; description: string; tags: string }>();
    const [publishAsset, setPublishAsset] = useState<AssetItem | null>(null);
    const [remixSource, setRemixSourceState] = useState<RemixSource | null>(null);
    const coverInputRef = useRef<HTMLInputElement>(null);
    const imageInputRef = useRef<HTMLInputElement>(null);
    const assetInputRef = useRef<HTMLInputElement>(null);
    const [keyword, setKeyword] = useState("");
    const [query, setQuery] = useState("");
    const [kindFilter, setKindFilter] = useState<AssetKind | "all">("all");
    const [tagFilter, setTagFilter] = useState<string[]>([]);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(10);
    const [editingAsset, setEditingAsset] = useState<AssetItem | null>(null);
    const [isAssetOpen, setIsAssetOpen] = useState(false);
    const [previewAsset, setPreviewAsset] = useState<AssetItem | null>(null);
    const [deletingAsset, setDeletingAsset] = useState<AssetItem | null>(null);
    const [formKind, setFormKind] = useState<AssetKind>("text");
    const [imageDraft, setImageDraft] = useState<ImageDraft>(null);
    const coverUrl = Form.useWatch("coverUrl", form) || "";
    const title = Form.useWatch("title", form) || "";
    const tags = Form.useWatch("tags", form) || [];
    const content = Form.useWatch("content", form) || "";

    // 关键字停下 300 毫秒再发请求，筛选条件全部交给服务端。
    useEffect(() => {
        const timer = setTimeout(() => {
            setQuery(keyword.trim());
            setPage(1);
        }, 300);
        return () => clearTimeout(timer);
    }, [keyword]);

    const assetsQuery = useAssetSearch({ q: query || undefined, kind: kindFilter === "all" ? undefined : kindFilter, tag: tagFilter.length ? tagFilter : undefined, page, size: pageSize });
    const items = assetsQuery.data?.items || [];
    const total = assetsQuery.data?.total || 0;
    const tagOptions = assetsQuery.data?.tags || [];

    const refreshAssets = () => queryClient.invalidateQueries({ queryKey: ["assets"] });

    const openCreate = () => {
        setEditingAsset(null);
        setImageDraft(null);
        setFormKind("text");
        form.setFieldsValue({ kind: "text", title: "", coverUrl: "", tags: [], source: t("assets.manual"), note: "", content: "" });
        setIsAssetOpen(true);
    };

    const openEdit = (asset: AssetItem) => {
        setEditingAsset(asset);
        setFormKind(asset.kind);
        setImageDraft(asset.kind === "text" ? null : { url: assetUrl(asset), storageKey: asset.storageKey, width: assetWidth(asset), height: assetHeight(asset), bytes: asset.bytes, mimeType: assetMimeType(asset) });
        form.setFieldsValue({
            kind: asset.kind,
            title: asset.title,
            coverUrl: asset.kind === "text" ? assetCoverUrl(asset) : "",
            tags: asset.tags || [],
            source: assetSource(asset),
            note: assetNote(asset),
            content: asset.kind === "text" ? assetText(asset) : "",
        });
        setIsAssetOpen(true);
    };

    const saveAsset = async () => {
        const values = await form.validateFields();
        const data: Record<string, unknown> = { source: values.source?.trim(), note: values.note?.trim() };
        if (values.kind === "text") {
            data.content = (values.content || "").trim();
            const cover = values.coverUrl?.trim();
            if (cover) data.coverUrl = cover;
        } else {
            if (!imageDraft) {
                message.error(t("assets.selectImage"));
                return;
            }
            data.width = imageDraft.width;
            data.height = imageDraft.height;
            data.mimeType = imageDraft.mimeType;
            if (!imageDraft.storageKey) data.url = imageDraft.url;
        }
        const payload = { kind: values.kind, title: values.title.trim(), tags: values.tags || [], storageKey: values.kind === "text" ? undefined : imageDraft?.storageKey, bytes: values.kind === "text" ? 0 : imageDraft?.bytes || 0, data };
        try {
            if (editingAsset) await patchAsset(editingAsset.id, payload);
            else await createAsset(payload);
            await refreshAssets();
            message.success(editingAsset ? t("assets.updated") : t("assets.saved"));
            setIsAssetOpen(false);
        } catch (error) {
            message.error(getApiErrorMessage(error));
        }
    };

    const readCoverFile = async (file?: File) => {
        if (!file) return;
        form.setFieldValue("coverUrl", await readFileAsDataUrl(file));
    };

    const readImageFile = async (file?: File) => {
        if (!file || !file.type.startsWith("image/")) return;
        try {
            const image = await uploadImage(file);
            setImageDraft({ url: image.url, storageKey: image.storageKey, width: image.width, height: image.height, bytes: image.bytes, mimeType: image.mimeType });
            if (!form.getFieldValue("title")) form.setFieldValue("title", file.name);
        } catch (error) {
            message.error(getApiErrorMessage(error));
        }
    };

    const copyAssetText = (asset: AssetItem) => {
        if (asset.kind !== "text") return;
        copyText(assetText(asset), t("assets.textCopied"));
    };

    const downloadAsset = async (asset: AssetItem) => {
        if (asset.kind === "text") return;
        const ext = assetMimeType(asset).split("/")[1]?.split("+")[0] || (asset.kind === "video" ? "mp4" : "png");
        const filename = `${asset.title || "asset"}.${ext}`;
        if (asset.storageKey) {
            await downloadMedia({ key: asset.id, storageKey: asset.storageKey, filename });
            return;
        }
        try {
            saveAs(await (await fetch(assetUrl(asset))).blob(), filename);
        } catch (error) {
            message.error(getApiErrorMessage(error));
        }
    };

    const exportAllAssets = async () => {
        if (!items.length) {
            message.warning(t("assets.noneToExport"));
            return;
        }
        await exportAssets(items, t("assets.packageName"));
    };

    const importAssetZip = async (file?: File) => {
        if (!file) return;
        try {
            const importedAssets = await readAssetPackage(file);
            for (const asset of importedAssets) await createAsset(asset);
            await refreshAssets();
            message.success(t("assets.imported", { count: importedAssets.length }));
        } catch (error) {
            console.error(error);
            message.error(t("assets.importFailed"));
        } finally {
            if (assetInputRef.current) assetInputRef.current.value = "";
        }
    };

    const publicSettingsQuery = useQuery({
        queryKey: ["settings", "public"],
        queryFn: ({ signal }) => getPublicSettings(signal),
        staleTime: 5 * 60 * 1000,
    });
    const publishMutation = useMutation({
        mutationFn: (values: { title: string; description: string; tags: string }) =>
            publishCommunityWork({
                assetId: publishAsset!.id,
                title: values.title,
                description: values.description,
                tags: values.tags,
                sourceWorkId: remixSource?.workId,
            }),
        onSuccess: async () => {
            message.success(t("assets.publishSuccess"));
            setPublishAsset(null);
            setRemixSourceState(null);
            persistRemixSource(null);
            publishForm.resetFields();
            await queryClient.invalidateQueries({ queryKey: ["community"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const deleteMutation = useMutation({
        mutationFn: (id: string) => deleteAsset(id),
        onSuccess: async () => {
            // 素材删除后同步清掉本地缩略图缓存。
            void deleteImagePreview(deletingAsset?.storageKey);
            await refreshAssets();
            message.success(t("assets.deleted"));
            setDeletingAsset(null);
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    return (
        <div className="flex h-full flex-col overflow-hidden bg-background text-stone-900 dark:text-stone-100">
            <main className="min-h-0 flex-1 overflow-y-auto bg-[radial-gradient(#e5e7eb_1px,transparent_1px)] px-6 py-8 [background-size:16px_16px] dark:bg-[radial-gradient(rgba(245,245,244,.14)_1px,transparent_1px)]">
                <div className="pb-8">
                    <div className="mx-auto max-w-5xl text-center">
                        <h1 className="text-4xl font-semibold tracking-tight text-stone-950 dark:text-stone-100">{t("assets.title")}</h1>
                        <p className="mt-3 text-sm text-stone-500 dark:text-stone-400">{t("assets.description")}</p>
                    </div>

                    <div className="mx-auto mt-8 w-full max-w-2xl">
                        <Input.Search className="w-full" size="large" allowClear prefix={<Search className="size-4 text-stone-400" />} value={keyword} placeholder={t("assets.search")} onChange={(event) => setKeyword(event.target.value)} />
                    </div>

                    <div className="mx-auto mt-6 grid max-w-6xl gap-3 text-left">
                        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                            <div className="grid gap-2 sm:grid-cols-[56px_minmax(0,1fr)] sm:items-center">
                                <div className="text-xs font-medium text-stone-500 dark:text-stone-400">{t("assets.type")}</div>
                                <div className="flex flex-wrap gap-2">
                                    {kindOptions.map((option) => (
                                        <Tag.CheckableTag
                                            key={option}
                                            checked={kindFilter === option}
                                            className={cn("prompt-filter-tag", kindFilter === option && "is-active")}
                                            onChange={() => {
                                                setPage(1);
                                                setKindFilter(option);
                                            }}
                                        >
                                            {option === "all" ? t("common.all") : t(`assets.kinds.${option}`)}
                                        </Tag.CheckableTag>
                                    ))}
                                </div>
                            </div>
                            <div className="flex flex-wrap gap-4">
                                <button
                                    type="button"
                                    className="cursor-pointer text-sm font-medium text-stone-700 underline-offset-4 hover:underline focus-visible:outline-none focus-visible:underline dark:text-stone-300"
                                    onClick={() => void exportAllAssets()}
                                >
                                    {t("assets.export")}
                                </button>
                                <button
                                    type="button"
                                    className="cursor-pointer text-sm font-medium text-stone-700 underline-offset-4 hover:underline focus-visible:outline-none focus-visible:underline dark:text-stone-300"
                                    onClick={() => assetInputRef.current?.click()}
                                >
                                    {t("assets.import")}
                                </button>
                                <button type="button" className="cursor-pointer text-sm font-medium text-stone-700 underline-offset-4 hover:underline focus-visible:outline-none focus-visible:underline dark:text-stone-300" onClick={openCreate}>
                                    {t("assets.add")}
                                </button>
                            </div>
                        </div>
                        {tagOptions.length ? (
                            <div className="flex flex-wrap items-center gap-2">
                                <div className="text-xs font-medium text-stone-500 dark:text-stone-400">{t("assets.fields.tags")}</div>
                                <Select
                                    className="min-w-56"
                                    size="small"
                                    mode="multiple"
                                    allowClear
                                    placeholder={t("assets.tagFilter")}
                                    value={tagFilter}
                                    onChange={(value: string[]) => {
                                        setPage(1);
                                        setTagFilter(value);
                                    }}
                                    options={tagOptions.map((tag) => ({ label: tag, value: tag }))}
                                />
                            </div>
                        ) : null}
                    </div>
                </div>

                <div className="mx-auto flex max-w-7xl flex-col gap-5">
                    <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                        {items.map((asset) => (
                            <AssetCard
                                key={asset.id}
                                asset={asset}
                                downloading={isDownloading(asset.storageKey)}
                                onOpen={() => setPreviewAsset(asset)}
                                onEdit={() => openEdit(asset)}
                                onCopy={copyAssetText}
                                onDownload={downloadAsset}
                                onDelete={() => setDeletingAsset(asset)}
                                onPublish={() => {
                                    setPublishAsset(asset);
                                    publishForm.setFieldsValue({ title: asset.title, description: "", tags: (asset.tags || []).join(",") });
                                    // 发布弹窗打开时读取复刻来源（工作台 ?remix= 写入），展示并随发布透传。
                                    setRemixSourceState(peekRemixSource());
                                }}
                            />
                        ))}
                    </div>

                    {!items.length ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={assetsQuery.isPending ? t("common.loading") : t("assets.empty")} className="py-20" /> : null}

                    {total > 0 ? (
                        <div className="flex justify-center">
                            <Pagination
                                current={page}
                                pageSize={pageSize}
                                total={total}
                                showSizeChanger
                                pageSizeOptions={PAGE_SIZES}
                                onChange={(nextPage, nextPageSize) => {
                                    setPage(nextPage);
                                    setPageSize(nextPageSize);
                                }}
                            />
                        </div>
                    ) : null}
                </div>
            </main>

            {publicSettingsQuery.data && !publicSettingsQuery.data.communityEnabled ? null : (
                <Modal
                    title={t("assets.publishTitle")}
                    open={!!publishAsset}
                    okText={t("assets.publishSubmit")}
                    cancelText={t("common.cancel")}
                    confirmLoading={publishMutation.isPending}
                    onCancel={() => setPublishAsset(null)}
                    onOk={async () => {
                        const values = await publishForm.validateFields();
                        await publishMutation.mutateAsync(values);
                    }}
                >
                    <p className="mb-4 text-sm text-stone-500 dark:text-stone-400">{t("assets.publishHint")}</p>
                    {remixSource ? (
                        <div className="mb-4 flex items-center gap-2">
                            <Tag color="geekblue" className="m-0">
                                {t("assets.remixFrom", { title: remixSource.title || remixSource.workId })}
                            </Tag>
                            <Button size="small" type="text" onClick={() => setRemixSourceState(null)}>
                                {t("common.cancel")}
                            </Button>
                        </div>
                    ) : null}
                    <Form form={publishForm} layout="vertical">
                        <Form.Item name="title" label={t("assets.publishFields.title")} rules={[{ required: true, message: t("assets.publishFields.titleRequired") }]}>
                            <Input maxLength={200} />
                        </Form.Item>
                        <Form.Item name="description" label={t("assets.publishFields.description")}>
                            <Input.TextArea rows={3} maxLength={1000} />
                        </Form.Item>
                        <Form.Item name="tags" label={t("assets.publishFields.tags")} extra={t("assets.publishFields.tagsHint")}>
                            <Input maxLength={200} />
                        </Form.Item>
                    </Form>
                </Modal>
            )}

            <Modal title={editingAsset ? t("assets.edit") : t("assets.add")} open={isAssetOpen} width={980} onCancel={() => setIsAssetOpen(false)} onOk={() => void saveAsset()} okText={t("common.save")} cancelText={t("common.cancel")} destroyOnHidden>
                <div className="grid gap-6 pt-1 lg:grid-cols-[minmax(0,1fr)_320px]">
                    <Form form={form} layout="vertical" requiredMark={false} initialValues={{ kind: "text", tags: [] }}>
                        <Form.Item name="kind" label={t("assets.type")}>
                            <Select
                                options={[
                                    { label: t("assets.kinds.text"), value: "text" },
                                    { label: t("assets.kinds.image"), value: "image" },
                                ]}
                                onChange={(value) => setFormKind(value)}
                            />
                        </Form.Item>
                        <Form.Item name="title" label={t("assets.fields.title")} rules={[{ required: true, message: t("assets.fields.titleRequired") }]}>
                            <Input size="large" placeholder={t("assets.fields.titlePlaceholder")} />
                        </Form.Item>
                        {formKind === "text" ? (
                            <Form.Item name="coverUrl" label={t("assets.fields.coverUrl")}>
                                <Space.Compact className="w-full">
                                    <Input placeholder={t("assets.fields.coverPlaceholder")} />
                                    <Button icon={<Upload className="size-3.5" />} onClick={() => coverInputRef.current?.click()}>
                                        {t("common.upload")}
                                    </Button>
                                </Space.Compact>
                            </Form.Item>
                        ) : null}
                        <Form.Item name="tags" label={t("assets.fields.tags")}>
                            <Select mode="tags" tokenSeparators={[",", "，"]} placeholder={t("assets.fields.tagsPlaceholder")} />
                        </Form.Item>
                        <div className="grid gap-4 sm:grid-cols-2">
                            <Form.Item name="source" label={t("assets.fields.source")}>
                                <Input placeholder={t("assets.fields.sourcePlaceholder")} />
                            </Form.Item>
                            <Form.Item name="note" label={t("assets.fields.note")}>
                                <Input placeholder={t("assets.fields.optional")} />
                            </Form.Item>
                        </div>
                        {formKind === "text" ? (
                            <Form.Item name="content" label={t("assets.fields.textContent")} rules={[{ required: true, message: t("assets.fields.textRequired") }]}>
                                <Input.TextArea rows={8} placeholder={t("assets.fields.textPlaceholder")} />
                            </Form.Item>
                        ) : (
                            <Form.Item label={t("assets.fields.imageContent")} required>
                                <div className="rounded-lg border border-dashed border-stone-300 p-4 dark:border-stone-700">
                                    <Button icon={<Upload className="size-4" />} onClick={() => imageInputRef.current?.click()}>
                                        {t("assets.selectImageFile")}
                                    </Button>
                                    {imageDraft ? (
                                        <Typography.Text type="secondary" className="ml-3 text-xs">
                                            {imageDraft.width}x{imageDraft.height} · {formatBytes(imageDraft.bytes)}
                                        </Typography.Text>
                                    ) : (
                                        <Typography.Text type="secondary" className="ml-3 text-xs">
                                            {t("assets.noImageSelected")}
                                        </Typography.Text>
                                    )}
                                </div>
                            </Form.Item>
                        )}
                    </Form>
                    <div className="rounded-xl border border-stone-200 bg-stone-50 p-4 dark:border-stone-800 dark:bg-stone-950">
                        <Typography.Text strong>{t("assets.preview")}</Typography.Text>
                        <div className="mt-3 overflow-hidden rounded-lg border border-stone-200 bg-background dark:border-stone-800">
                            {coverUrl || imageDraft?.url ? (
                                <img src={coverUrl || imageDraft?.url} alt="" className="aspect-[4/3] w-full object-cover" />
                            ) : (
                                <div className="flex aspect-[4/3] items-center justify-center bg-stone-100 p-5 text-center text-sm text-stone-500 dark:bg-stone-900">{content || t("assets.noCover")}</div>
                            )}
                            <div className="p-4">
                                <Typography.Text strong ellipsis className="block">
                                    {title || t("assets.untitled")}
                                </Typography.Text>
                                <div className="mt-2 flex flex-wrap gap-1.5">
                                    {tags.length ? (
                                        tags.map((tag) => (
                                            <Tag key={tag} className="m-0">
                                                {tag}
                                            </Tag>
                                        ))
                                    ) : (
                                        <Tag className="m-0">{t("assets.untagged")}</Tag>
                                    )}
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
                <input
                    ref={coverInputRef}
                    type="file"
                    accept="image/*"
                    className="hidden"
                    onChange={(event) => {
                        void readCoverFile(event.target.files?.[0]);
                        event.target.value = "";
                    }}
                />
                <input
                    ref={imageInputRef}
                    type="file"
                    accept="image/*"
                    className="hidden"
                    onChange={(event) => {
                        void readImageFile(event.target.files?.[0]);
                        event.target.value = "";
                    }}
                />
            </Modal>

            <AssetDrawer asset={previewAsset} downloading={isDownloading(previewAsset?.storageKey)} onClose={() => setPreviewAsset(null)} onCopy={copyAssetText} onDownload={downloadAsset} />

            <input ref={assetInputRef} type="file" accept="application/zip,.zip" className="hidden" onChange={(event) => void importAssetZip(event.target.files?.[0])} />

            <Modal
                title={t("assets.deleteTitle")}
                open={Boolean(deletingAsset)}
                onCancel={() => setDeletingAsset(null)}
                onOk={() => deletingAsset && deleteMutation.mutate(deletingAsset.id)}
                confirmLoading={deleteMutation.isPending}
                okText={t("common.delete")}
                okButtonProps={{ danger: true }}
                cancelText={t("common.cancel")}
            >
                {t("assets.deleteConfirm", { name: deletingAsset?.title })}
            </Modal>
        </div>
    );
}

function AssetCard({
    asset,
    downloading,
    onOpen,
    onEdit,
    onCopy,
    onDownload,
    onDelete,
    onPublish,
}: {
    asset: AssetItem;
    downloading: boolean;
    onOpen: () => void;
    onEdit: () => void;
    onCopy: (asset: AssetItem) => void;
    onDownload: (asset: AssetItem) => void;
    onDelete: () => void;
    onPublish: () => void;
}) {
    const { t } = useTranslation();
    const cover = assetCoverUrl(asset);
    return (
        <Card
            hoverable
            className="overflow-hidden"
            styles={{ body: { padding: 0 } }}
            cover={
                <button type="button" className="block w-full text-left" onClick={onOpen}>
                    {cover ? (
                        <img src={cover} alt={asset.title} className="aspect-[4/3] w-full object-cover" />
                    ) : (
                        <div className="flex aspect-[4/3] items-center justify-center bg-stone-100 p-5 text-center text-sm leading-6 text-stone-600 dark:bg-stone-900 dark:text-stone-300">
                            {asset.kind === "text" ? assetText(asset) : t("assets.noCover")}
                        </div>
                    )}
                </button>
            }
        >
            <button type="button" className="block w-full text-left" onClick={onOpen}>
                <div className="p-4">
                    <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                            <h2 className="line-clamp-1 text-sm font-semibold text-stone-950 dark:text-stone-100">{asset.title}</h2>
                            <Typography.Text type="secondary" className="mt-1 block text-xs">
                                {assetSource(asset) || t("assets.unknownSource")}
                            </Typography.Text>
                        </div>
                        <Tag className="m-0 shrink-0 text-[11px]">{t(`assets.kinds.${asset.kind}`)}</Tag>
                    </div>
                    <Typography.Paragraph type="secondary" ellipsis={{ rows: 3 }} className="!mb-0 !mt-2 !text-xs !leading-5">
                        {assetSummary(asset)}
                    </Typography.Paragraph>
                    <div className="mt-3 flex flex-wrap gap-1.5">
                        {(asset.tags || []).slice(0, 3).map((tag) => (
                            <Tag key={tag} className="m-0 text-[11px]">
                                {tag}
                            </Tag>
                        ))}
                        {!asset.tags?.length ? <Tag className="m-0 text-[11px]">{t("assets.noTags")}</Tag> : null}
                    </div>
                </div>
            </button>
            <div className="flex items-center gap-2 px-4 pb-4">
                <Button size="small" onClick={onOpen}>
                    {t("common.view")}
                </Button>
                {asset.kind !== "video" ? (
                    <Button size="small" icon={<PencilLine className="size-3.5" />} onClick={onEdit}>
                        {t("common.edit")}
                    </Button>
                ) : null}
                {asset.kind === "text" ? (
                    <Button size="small" icon={<Copy className="size-3.5" />} onClick={() => onCopy(asset)}>
                        {t("common.copy")}
                    </Button>
                ) : null}
                {asset.kind === "image" || asset.kind === "video" ? (
                    <Button size="small" icon={<Download className="size-3.5" />} loading={downloading} onClick={() => onDownload(asset)}>
                        {t("common.download")}
                    </Button>
                ) : null}
                {asset.kind === "image" || asset.kind === "video" ? (
                    <Button size="small" icon={<Share2 className="size-3.5" />} onClick={onPublish}>
                        {t("assets.publish")}
                    </Button>
                ) : null}
                <Button size="small" danger icon={<Trash2 className="size-3.5" />} onClick={onDelete}>
                    {t("common.delete")}
                </Button>
            </div>
        </Card>
    );
}

function AssetDrawer({ asset, downloading, onClose, onCopy, onDownload }: { asset: AssetItem | null; downloading: boolean; onClose: () => void; onCopy: (asset: AssetItem) => void; onDownload: (asset: AssetItem) => void }) {
    const { t } = useTranslation();
    const cover = asset ? assetCoverUrl(asset) : "";
    const note = asset ? assetNote(asset) : "";
    return (
        <Drawer title={t("assets.details")} open={Boolean(asset)} size="large" onClose={onClose}>
            {asset ? (
                <div className="space-y-5">
                    {cover ? (
                        <Image src={cover} alt={asset.title} className="rounded-lg" />
                    ) : (
                        <div className="rounded-lg border border-stone-200 bg-stone-50 p-5 text-sm leading-6 text-stone-600 dark:border-stone-800 dark:bg-stone-900 dark:text-stone-300">{asset.kind === "text" ? assetText(asset) : t("assets.noCover")}</div>
                    )}
                    <div>
                        <Typography.Title level={4} className="!mb-2">
                            {asset.title}
                        </Typography.Title>
                        <Space size={[4, 4]} wrap>
                            <Tag>{t(`assets.kinds.${asset.kind}`)}</Tag>
                            {(asset.tags || []).map((tag) => (
                                <Tag key={tag}>{tag}</Tag>
                            ))}
                        </Space>
                    </div>
                    <div className="rounded-lg border border-stone-200 p-4 dark:border-stone-800">
                        <Typography.Text type="secondary" className="block text-xs">
                            {t("assets.fields.textContent")}
                        </Typography.Text>
                        {asset.kind === "text" ? (
                            <Typography.Paragraph className="mt-2 whitespace-pre-wrap">{assetText(asset)}</Typography.Paragraph>
                        ) : asset.kind === "video" ? (
                            <video src={assetUrl(asset)} controls className="mt-2 aspect-video w-full rounded-lg bg-black" />
                        ) : (
                            <Typography.Text className="mt-2 block">
                                {assetWidth(asset)}x{assetHeight(asset)} · {formatBytes(asset.bytes)} · {assetMimeType(asset)}
                            </Typography.Text>
                        )}
                    </div>
                    {note ? (
                        <div>
                            <Typography.Text type="secondary">{t("assets.fields.note")}</Typography.Text>
                            <Typography.Paragraph className="mt-1">{note}</Typography.Paragraph>
                        </div>
                    ) : null}
                    <Space>
                        {asset.kind === "text" ? (
                            <Button type="primary" icon={<Copy className="size-4" />} onClick={() => onCopy(asset)}>
                                {t("assets.copyText")}
                            </Button>
                        ) : null}
                        {asset.kind === "image" || asset.kind === "video" ? (
                            <Button type="primary" icon={<Download className="size-4" />} loading={downloading} onClick={() => onDownload(asset)}>
                                {asset.kind === "video" ? t("assets.downloadVideo") : t("assets.downloadImage")}
                            </Button>
                        ) : null}
                    </Space>
                </div>
            ) : null}
        </Drawer>
    );
}

function assetSummary(asset: AssetItem) {
    if (asset.kind === "text") return assetText(asset);
    return `${assetWidth(asset)}x${assetHeight(asset)} · ${formatBytes(asset.bytes)} · ${assetMimeType(asset)}`;
}
