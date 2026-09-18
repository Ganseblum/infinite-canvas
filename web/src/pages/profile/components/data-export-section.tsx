import { useState } from "react";
import { App, Button } from "antd";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { exportMyData } from "@/services/api/account";

// 个人数据导出入口（差异清单 #123）：下载 GET /me/export 返回的 JSON 副本。
export function DataExportSection() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const [exporting, setExporting] = useState(false);

    const handleExport = async () => {
        setExporting(true);
        try {
            await exportMyData();
            message.success(t("profile.dataExport.success"));
        } catch (error) {
            message.error(getApiErrorMessage(error));
        } finally {
            setExporting(false);
        }
    };

    return (
        <section id="profile-export" className="scroll-mt-4 rounded-xl border border-stone-200 p-6 dark:border-stone-800">
            <h2 className="text-lg font-semibold">{t("profile.dataExport.title")}</h2>
            <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
                <p className="max-w-xl text-xs leading-5 text-stone-500 dark:text-stone-400">{t("profile.dataExport.description")}</p>
                <Button loading={exporting} onClick={() => void handleExport()}>
                    {t("profile.dataExport.action")}
                </Button>
            </div>
        </section>
    );
}
