import type { MetadataRoute } from "next";

import { siteUrl } from "@/lib/blog-api";

/** robots.txt：全站允许抓取，仅屏蔽 /api/ 路径，并指向 sitemap。 */
export default function robots(): MetadataRoute.Robots {
  return {
    rules: { userAgent: "*", allow: "/", disallow: ["/api/"] },
    sitemap: `${siteUrl()}/sitemap.xml`,
  };
}
