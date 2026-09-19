/** @type {import('next').NextConfig} */
const goApi = process.env.BLOG_API_INTERNAL ?? "http://127.0.0.1:8080";

const nextConfig = {
    // 容器部署用 standalone 产物；docker 里只带 .next/standalone + .next/static。
    output: "standalone",
    async rewrites() {
        // 互动请求（评论/点赞/收藏）走同域代理到内网 Go：浏览器 cookie 落在 blog 子域，
        // 不跨站、不动主应用 cookie 作用域（见 docs/content/docs/overview/blog-architecture.mdx §3.3）。
        return [
            { source: "/api/v1/blog/:path*", destination: `${goApi}/api/v1/blog/:path*` },
            // 登录：博客自己的登录页走同域代理调 Go 认证接口，accessToken 落 localStorage，
            // 互动请求带 Bearer（middleware.Auth 口径不变）。
            { source: "/api/v1/auth/:path*", destination: `${goApi}/api/v1/auth/:path*` },
        ];
    },
};

export default nextConfig;
