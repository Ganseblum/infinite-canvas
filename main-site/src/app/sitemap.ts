import type { MetadataRoute } from "next";
import { site } from "@/data/site";

// 静态导出（output: "export"）要求元数据路由显式声明为构建期静态生成
export const dynamic = "force-static";

export default function sitemap(): MetadataRoute.Sitemap {
  const routes = ["", "/works/", "/films/", "/templates/", "/blog/"];
  return routes.map((route) => ({
    url: `${site.url}${route}`,
    lastModified: new Date(),
    changeFrequency: "weekly",
    priority: route === "" ? 1 : 0.7,
  }));
}
