// 同域 API 代理：/api/v1/* 全量转发到内网 Go（BLOG_API_INTERNAL 运行时读取）。
// 互动请求（评论/点赞/收藏）与登录都走这里：浏览器同源，登录态 cookie 落 blog 子域。
// 不用 next.config rewrites——rewrites 在 standalone 构建期烘焙进配置快照，
// 容器运行期改 env 不生效（部署时踩过：代理一直打 127.0.0.1:8080）。
const target = () => (process.env.BLOG_API_INTERNAL ?? "http://127.0.0.1:8080").replace(/\/$/, "");

type Ctx = { params: Promise<{ path?: string[] }> };

async function proxy(request: Request, ctx: Ctx): Promise<Response> {
  const { path = [] } = await ctx.params;
  const incoming = new URL(request.url);
  const url = `${target()}/api/v1/${path.join("/")}${incoming.search}`;

  const headers = new Headers(request.headers);
  headers.delete("host");
  headers.delete("content-length");

  const body = ["GET", "HEAD"].includes(request.method) ? undefined : await request.arrayBuffer();

  const res = await fetch(url, {
    method: request.method,
    headers,
    body,
    redirect: "manual",
  });
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers: res.headers });
}

export {
  proxy as GET,
  proxy as POST,
  proxy as PUT,
  proxy as PATCH,
  proxy as DELETE,
  proxy as HEAD,
  proxy as OPTIONS,
};
