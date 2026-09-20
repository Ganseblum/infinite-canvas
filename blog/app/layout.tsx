import type { Metadata } from "next";

import "./globals.css";
import SiteHeader from "@/components/site-header";
import SiteFooter from "@/components/site-footer";
import { siteUrl } from "@/lib/blog-api";

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl()),
  title: {
    default: "航的手记 — AI 工作流 / 独立开发 / 模板配色",
    template: "%s — 航的手记",
  },
  description: "主站是放映厅，这里是放映厅后台的排字房——作品怎么被做出来，就在这里怎么被写下来。",
  alternates: { canonical: "/" },
  openGraph: { type: "website", siteName: "航的手记", locale: "zh_CN" },
  robots: { index: true, follow: true },
};

/** 全站根布局：顶栏 / 页脚包裹所有路由，各页 head 由静态 metadata 或 generateMetadata 提供。 */
export default function RootLayout({ children }: { children: React.ReactNode }) {
  // 主题防闪（FOUC）：渲染前按 localStorage > 系统偏好落 data-theme，暗色为默认。
  const themeInit = `(function(){try{var t=localStorage.getItem("blog-theme");if(!t){t=window.matchMedia("(prefers-color-scheme: light)").matches?"light":"dark";}document.documentElement.setAttribute("data-theme",t);}catch(e){}})();`;
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link
          href="https://fonts.googleapis.com/css2?family=Anton&family=IBM+Plex+Mono:wght@400;500&family=Noto+Sans+SC:wght@400;500;700&family=Noto+Serif+SC:wght@400;600;700;900&display=swap"
          rel="stylesheet"
        />
        <script dangerouslySetInnerHTML={{ __html: themeInit }} />
      </head>
      <body>
        <SiteHeader />
        {children}
        <SiteFooter />
      </body>
    </html>
  );
}
