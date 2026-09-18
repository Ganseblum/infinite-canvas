import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Anton, IBM_Plex_Mono } from "next/font/google";
import "./globals.css";
import Footer from "@/components/Footer";
import { site } from "@/data/site";

/**
 * 字体在构建期下载并自托管（next/font），线上不再请求 Google Fonts；
 * 中文正文走系统字体栈（苹方/微软雅黑），零 CJK 字体下载成本。
 */
const anton = Anton({
  weight: "400",
  subsets: ["latin"],
  variable: "--font-anton",
  display: "swap",
});

const plexMono = IBM_Plex_Mono({
  weight: ["400", "500"],
  subsets: ["latin"],
  variable: "--font-mono",
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL(site.url),
  title: {
    default: "HANG — 航的个人主站 · 图像 / 视频 / 三件套模板 / 手记",
    template: "%s — HANG",
  },
  description: site.description,
  keywords: ["航", "HANG", "AI 图像", "AI 视频", "PPT 模板", "Word 模板", "Excel 模板", "博客", "youc.online"],
  alternates: { canonical: "/" },
  openGraph: {
    type: "website",
    locale: "zh_CN",
    siteName: site.name,
    title: "HANG — 航的个人主站",
    description: site.description,
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "HANG — 航的个人主站" }],
  },
  twitter: { card: "summary_large_image", images: ["/og.png"] },
  robots: { index: true, follow: true },
};

/** 结构化数据：让搜索引擎理解这是谁的个人站、站点是什么。 */
const jsonLd = {
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "Person",
      name: "航",
      alternateName: "HANG",
      url: site.url,
      email: `mailto:${site.email}`,
      address: { "@type": "PostalAddress", addressLocality: "杭州", addressCountry: "CN" },
      knowsAbout: ["AI 图像生成", "视频创作", "Office 模板设计"],
    },
    {
      "@type": "WebSite",
      name: site.name,
      url: site.url,
      inLanguage: "zh-CN",
      description: site.description,
      author: { "@type": "Person", name: "航" },
    },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN" className={`${anton.variable} ${plexMono.variable}`}>
      <body>
        <a className="skip" href="#main">
          跳到主要内容
        </a>
        <div className="grain" aria-hidden="true" />
        {children}
        <Footer />
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }}
        />
      </body>
    </html>
  );
}
