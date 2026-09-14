import { useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { App, Button, Input, Pagination, Select } from "antd";
import { Download, FileUp, Plus, Search } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { readZip } from "@/lib/zip";
import { normalizeCanvasData } from "@/lib/canvas/canvas-data";
import { getApiErrorMessage } from "@/lib/api-error";
import { createCanvas, getCanvas, listCanvases } from "@/services/api/canvas";
import { putMedia } from "@/services/api/media";
import type { CanvasSort } from "@/services/data/types";
import type { CanvasExportFile } from "@/types/canvas-export";
import { CanvasDeleteProjectsDialog } from "@/components/canvas/canvas-delete-projects-dialog";
import { CanvasProjectCard } from "@/components/canvas/canvas-project-card";
import { useCanvasUiStore } from "@/stores/canvas/use-canvas-ui-store";
import { exportCanvasProjects } from "@/lib/canvas/canvas-export";
import { hasAgentUrlBootstrap } from "@/lib/agent/agent-url-bootstrap";

const PAGE_SIZE = 12;

export default function CanvasPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const [searchParams] = useSearchParams();
    const inputRef = useRef<HTMLInputElement>(null);
    const autoOpenRef = useRef(false);
    const [keyword, setKeyword] = useState("");
    const [query, setQuery] = useState("");
    const [sort, setSort] = useState<CanvasSort>("-updatedAt");
    const [page, setPage] = useState(1);
    const selectedIds = useCanvasUiStore((state) => state.selectedProjectIds);
    const setDeleteIds = useCanvasUiStore((state) => state.setDeleteProjectIds);
    const clearSelectedIds = useCanvasUiStore((state) => state.clearSelectedProjectIds);

    const params = { page, size: PAGE_SIZE, q: query || undefined, sort };
    const canvasesQuery = useQuery({ queryKey: ["canvases", params], queryFn: ({ signal }) => listCanvases(params, signal) });
    const canvases = canvasesQuery.data?.items || [];
    const total = canvasesQuery.data?.total || 0;

    const mode = searchParams.get("mode");
    const agentMode = mode === "new" || mode === "recent" || mode === "choose";
    const agentQuery = agentMode ? `?${searchParams.toString()}` : "";
    const enterProject = (id: string) => {
        const agentHash = hasAgentUrlBootstrap(window.location.hash) ? window.location.hash : "";
        navigate(`/canvas/${id}${agentQuery}${agentHash}`, { replace: Boolean(agentHash) });
    };
    const nextTitle = () => t("canvas.defaultTitle", { count: total + 1 });

    const createMutation = useMutation({
        mutationFn: (title: string) => createCanvas({ title }),
        onSuccess: async (canvas) => {
            await queryClient.invalidateQueries({ queryKey: ["canvases"] });
            enterProject(canvas.id);
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    // 关键字交给服务端筛选，输入停下 300 毫秒再发请求。
    useEffect(() => {
        const timer = setTimeout(() => {
            setQuery(keyword.trim());
            setPage(1);
            clearSelectedIds();
        }, 300);
        return () => clearTimeout(timer);
    }, [clearSelectedIds, keyword]);

    useEffect(() => {
        if (autoOpenRef.current || (mode !== "new" && mode !== "recent") || !canvasesQuery.isSuccess) return;
        autoOpenRef.current = true;
        const recent = canvasesQuery.data.items[0];
        if (mode === "recent" && recent) {
            enterProject(recent.id);
            return;
        }
        createMutation.mutate(nextTitle());
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [canvasesQuery.isSuccess, mode]);

    const importCanvas = async (file?: File) => {
        if (!file) return;
        try {
            const zip = await readZip(file);
            const projectFile = zip.get("projects.json");
            if (!projectFile) throw new Error("missing projects.json");
            const data = JSON.parse(await projectFile.text()) as CanvasExportFile;
            for (const item of data.projects) {
                // 先传媒体再建画布：顺序反了会生成一张引用不存在图片的画布。
                await Promise.all(
                    item.files.map(async (entry) => {
                        const blob = zip.get(entry.path);
                        if (!blob) return;
                        const typedBlob = blob.type ? blob : blob.slice(0, blob.size, entry.mimeType);
                        await putMedia(entry.storageKey, typedBlob);
                    }),
                );
                await createCanvas({ title: item.project.title, data: normalizeCanvasData(item.project.data) });
            }
            await queryClient.invalidateQueries({ queryKey: ["canvases"] });
            message.success(t("canvas.imported", { count: data.projects.length }));
        } catch (error) {
            console.error(error);
            message.error(t("canvas.importFailed"));
        } finally {
            if (inputRef.current) inputRef.current.value = "";
        }
    };

    const exportSelected = async () => {
        const hide = message.loading(t("canvas.projectPage.exporting"), 0);
        try {
            // 摘要不含 data，导出前逐张拉详情。
            const details = await Promise.all(selectedIds.map((id) => getCanvas(id)));
            await exportCanvasProjects(details, `${t("canvas.title")}-${details.length}`);
            message.success(t("canvas.projectPage.exported"));
        } catch (error) {
            console.error(error);
            message.error(t("canvas.sidePanel.exportFailed"));
        } finally {
            hide();
        }
    };

    if (agentMode && (canvasesQuery.isPending || createMutation.isPending)) return <main className="flex h-full items-center justify-center bg-background text-sm text-stone-500">{t("canvas.opening")}</main>;

    return (
        <main className="h-full overflow-auto bg-background text-stone-950 dark:text-stone-100">
            <div className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-6 py-10">
                <header className="flex flex-wrap items-end justify-between gap-4 border-b border-stone-200 pb-6 dark:border-stone-800">
                    <div>
                        <p className="text-xs text-stone-500">{t("canvas.library")}</p>
                        <h1 className="mt-3 text-3xl font-semibold">{t("canvas.title")}</h1>
                    </div>
                    <div className="flex items-center gap-2">
                        {selectedIds.length ? (
                            <>
                                <Button icon={<Download className="size-4" />} onClick={() => void exportSelected()}>
                                    {t("canvas.exportSelected")}
                                </Button>
                                <Button onClick={() => setDeleteIds(selectedIds)}>{t("canvas.deleteSelected")}</Button>
                            </>
                        ) : null}
                        <Button icon={<FileUp className="size-4" />} onClick={() => inputRef.current?.click()}>
                            {t("canvas.import")}
                        </Button>
                        <Button type="primary" loading={createMutation.isPending} icon={<Plus className="size-4" />} onClick={() => createMutation.mutate(nextTitle())}>
                            {t("canvas.create")}
                        </Button>
                    </div>
                </header>

                <div className="flex flex-wrap items-center gap-3">
                    <Input
                        allowClear
                        className="max-w-72"
                        prefix={<Search className="size-4 text-stone-400" />}
                        placeholder={t("canvas.searchPlaceholder")}
                        value={keyword}
                        onChange={(event) => setKeyword(event.target.value)}
                    />
                    <Select
                        className="w-36"
                        value={sort}
                        onChange={(value: CanvasSort) => {
                            setSort(value);
                            setPage(1);
                            clearSelectedIds();
                        }}
                        options={[
                            { value: "-updatedAt", label: t("canvas.sort.updatedAt") },
                            { value: "-createdAt", label: t("canvas.sort.createdAt") },
                            { value: "title", label: t("canvas.sort.title") },
                        ]}
                    />
                </div>

                {canvasesQuery.isPending ? (
                    <section className="flex min-h-[360px] items-center justify-center border-y border-stone-200 text-sm text-stone-500 dark:border-stone-800">{t("canvas.loading")}</section>
                ) : canvasesQuery.isError ? (
                    <section className="flex min-h-[360px] flex-col items-center justify-center gap-4 border-y border-stone-200 text-center dark:border-stone-800">
                        <p className="text-sm text-stone-500">{getApiErrorMessage(canvasesQuery.error)}</p>
                        <Button onClick={() => void canvasesQuery.refetch()}>{t("common.retry")}</Button>
                    </section>
                ) : canvases.length ? (
                    <>
                        <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
                            {canvases.map((canvas) => (
                                <CanvasProjectCard key={canvas.id} project={canvas} />
                            ))}
                        </div>
                        {total > PAGE_SIZE ? (
                            <div className="flex justify-center">
                                <Pagination
                                    current={page}
                                    pageSize={PAGE_SIZE}
                                    total={total}
                                    showSizeChanger={false}
                                    onChange={(nextPage) => {
                                        setPage(nextPage);
                                        clearSelectedIds();
                                    }}
                                />
                            </div>
                        ) : null}
                    </>
                ) : (
                    <section className="flex min-h-[360px] flex-col items-center justify-center border-y border-stone-200 text-center dark:border-stone-800">
                        <h2 className="text-xl font-medium">{t("canvas.empty")}</h2>
                        <p className="mt-3 text-sm text-stone-500">{t("canvas.emptyDescription")}</p>
                        <Button type="primary" className="mt-6" icon={<Plus className="size-4" />} onClick={() => createMutation.mutate(nextTitle())}>
                            {t("canvas.create")}
                        </Button>
                    </section>
                )}
            </div>

            <input ref={inputRef} type="file" accept="application/zip,.zip" className="hidden" onChange={(event) => void importCanvas(event.target.files?.[0])} />
            <CanvasDeleteProjectsDialog />
        </main>
    );
}
