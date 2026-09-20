import { useState } from "react";
import { App, Button, Modal } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { deleteCanvas } from "@/services/api/canvas";
import { useCanvasUiStore } from "@/stores/canvas/use-canvas-ui-store";

/**
 * 批量删除画布项目确认弹窗：由 UI store 的 deleteProjectIds 驱动显隐。
 * 删除用 Promise.allSettled 并发执行，任一失败提示错误但其余仍生效，
 * 完成后从选中列表移除并刷新项目列表缓存。
 */
export function CanvasDeleteProjectsDialog() {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [deleting, setDeleting] = useState(false);
    const ids = useCanvasUiStore((state) => state.deleteProjectIds);
    const setDeleteIds = useCanvasUiStore((state) => state.setDeleteProjectIds);
    const removeSelectedIds = useCanvasUiStore((state) => state.removeSelectedProjectIds);

    const confirm = async () => {
        setDeleting(true);
        try {
            const results = await Promise.allSettled(ids.map((id) => deleteCanvas(id)));
            const failed = results.find((result) => result.status === "rejected");
            if (failed?.status === "rejected") message.error(getApiErrorMessage(failed.reason));
            removeSelectedIds(ids);
            setDeleteIds([]);
            await queryClient.invalidateQueries({ queryKey: ["canvases"] });
        } finally {
            setDeleting(false);
        }
    };

    return (
        <Modal
            title={t("canvas.project.deleteTitle")}
            open={ids.length > 0}
            centered
            onCancel={() => setDeleteIds([])}
            footer={
                <>
                    <Button disabled={deleting} onClick={() => setDeleteIds([])}>
                        {t("common.cancel")}
                    </Button>
                    <Button danger type="primary" loading={deleting} onClick={() => void confirm()}>
                        {t("common.delete")}
                    </Button>
                </>
            }
        >
            <p className="text-sm text-stone-500">{t("canvas.project.deleteDescription", { count: ids.length })}</p>
        </Modal>
    );
}
