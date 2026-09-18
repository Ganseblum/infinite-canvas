/** @type {import('next').NextConfig} */
import path from "node:path";

const nextConfig = {
  // 避免上层目录的其他 lockfile 干扰 workspace 根推断
  outputFileTracingRoot: path.resolve(),
  // 全站静态导出：构建产物在 out/，nginx 直接托管，服务器无需 Node 运行时
  output: "export",
  // 导出目录带尾斜杠（works/ 目录形式），对 nginx 与搜索引擎更友好
  trailingSlash: true,
  images: {
    // 静态导出不支持默认图片优化服务；图片在构建期不可变，直接原图输出
    unoptimized: true,
  },
};

export default nextConfig;
