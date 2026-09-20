import { App } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { createAsset, listAssets } from "@/services/api/assets";
import type { AssetListParams } from "@/services/data/types";

// 素材列表属于服务端状态：素材页、画布侧栏与素材选择弹窗共用这一个查询入口。
/** queryKey 含完整参数，参数变化自动重新请求；@param params 为分页/关键词/类型/标签等筛选条件。 */
export function useAssetSearch(params: AssetListParams) {
    return useQuery({ queryKey: ["assets", params], queryFn: ({ signal }) => listAssets(params, signal) });
}

/** 新增素材：成功后提示并失效 assets 查询缓存，失败弹统一错误文案。 */
export function useAddAsset() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: createAsset,
        onSuccess: async () => {
            message.success(t("common.addedToAssets"));
            await queryClient.invalidateQueries({ queryKey: ["assets"] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });
}
