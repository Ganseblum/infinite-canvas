import { saveAs } from "file-saver";
import { useCallback, useRef, useState } from "react";
import { App } from "antd";

import { getApiErrorMessage } from "@/lib/api-error";
import { fetchMediaDownload, requestDownload } from "@/services/api/media";

// 素材页与图像/视频工作台共用的两步下载：先申请取件链接再取件保存。
// 以 key 维度做请求中防重（连点不会并发重复申请），失败统一走 getApiErrorMessage 三级文案。
export function useMediaDownload() {
    const { message } = App.useApp();
    const pendingRef = useRef<Set<string>>(new Set());
    const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set());

    const isDownloading = useCallback((key?: string) => (key ? pending.has(key) : false), [pending]);

    const download = useCallback(
        async (input: { key: string; storageKey?: string; filename: string }) => {
            if (!input.storageKey || pendingRef.current.has(input.key)) return;
            pendingRef.current.add(input.key);
            setPending(new Set(pendingRef.current));
            try {
                // 先向服务端申请取件链接（按档位返回水印版或干净件），再凭链接取件。
                const { url } = await requestDownload(input.storageKey);
                const blob = await fetchMediaDownload(url);
                saveAs(blob, input.filename);
            } catch (error) {
                message.error(getApiErrorMessage(error));
            } finally {
                pendingRef.current.delete(input.key);
                setPending(new Set(pendingRef.current));
            }
        },
        [message],
    );

    return { download, isDownloading };
}
