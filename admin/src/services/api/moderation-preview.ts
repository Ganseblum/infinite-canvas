import { ApiError } from "@/lib/api-error";
import { API_BASE_URL, refreshSession } from "@/services/api/client";
import { useAuthStore } from "@/stores/use-auth-store";

// 审核隔离原件预览接口要求 Authorization 头：<img src> 发不出这个头，ic_media cookie 又只覆盖 /api/media，
// 所以直接把 previewPath 当 src 用，在同源形态下就已经是 401 破图，拆到跨域后还会再叠一层 404。
// 这里带令牌取回二进制再转 blob URL；调用方负责在切换预览对象或关闭面板时 revokeObjectURL。
export async function fetchModerationPreviewUrl(previewPath: string, signal?: AbortSignal) {
    const load = () => {
        const accessToken = useAuthStore.getState().accessToken;
        return fetch(`${API_BASE_URL}${previewPath}`, {
            headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : undefined,
            credentials: "include",
            signal,
        });
    };

    let response = await load();
    // 与 client.ts 一致：access token 过期时单飞刷新后重放一次，其余失败直接抛出。
    if (response.status === 401 && !signal?.aborted && (await refreshSession())) response = await load();
    if (!response.ok) throw new ApiError({ code: `HTTP_${response.status}`, status: response.status });
    return URL.createObjectURL(await response.blob());
}
