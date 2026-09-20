// Agent 连接参数通过 URL hash 引导（#agentUrl=...&agentToken=...）：避免地址进 query 被
// 服务端访问日志记录。读取后须从 hash 中移除这两个参数，防止刷新后重复消费。
/** 判断 hash 里是否携带 agentUrl / agentToken 引导参数。 */
export function hasAgentUrlBootstrap(hash: string) {
    const params = new URLSearchParams(hash.replace(/^#/, ""));
    return params.has("agentUrl") || params.has("agentToken");
}

/** 解析并返回引导参数；remainingHash 为剔除这两个参数后的剩余 hash。 */
export function readAgentUrlBootstrap(hash: string) {
    const params = new URLSearchParams(hash.replace(/^#/, ""));
    if (!params.has("agentUrl") && !params.has("agentToken")) return null;
    const url = params.get("agentUrl")?.trim() || "";
    const token = params.get("agentToken")?.trim() || "";
    params.delete("agentUrl");
    params.delete("agentToken");
    const remaining = params.toString();
    return { url, token, remainingHash: remaining ? `#${remaining}` : "" };
}
