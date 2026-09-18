import type { MetadataRoute } from "next";
import { site } from "@/data/site";

// 静态导出（output: "export"）要求元数据路由显式声明为构建期静态生成
export const dynamic = "force-static";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: { userAgent: "*", allow: "/" },
    sitemap: `${site.url}/sitemap.xml`,
  };
}
