/** @type {import('next').NextConfig} */
const nextConfig = {
    // 容器部署用 standalone 产物；docker 里只带 .next/standalone + .next/static。
    // /api/v1/* 同域代理不用 rewrites：rewrites 在 standalone 构建期烘焙，
    // 运行期 env 不生效——代理在 app/api/v1/[...path]/route.ts 运行时读取 BLOG_API_INTERNAL。
    output: "standalone",
};

export default nextConfig;
