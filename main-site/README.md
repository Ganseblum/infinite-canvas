# main-site — 个人主站（youc.online）

航的个人聚合门户：展示图像 / 视频作品、Office 三件套模板、博客手记，把访客分发到各渠道。
视觉基于 `design/html/home-draft-cinema.html`（暗色放映厅）。

## 技术栈与部署形态

- **Next.js（App Router）+ 全站静态导出**（`next.config.mjs` 里 `output: "export"`）。
- 构建产物在 `out/`，是纯静态目录，**nginx 直接托管，服务器不需要 Node 运行时**。
- 与 `web/`（画布应用）、`admin/`（管理后台）互不依赖，只共享设计语言。
- SEO：每页独立 title / description / canonical / OG 标签；`sitemap.xml` 与 `robots.txt`
  构建期生成；根布局注入 JSON-LD（Person + WebSite）；全站语义化标签（header/nav/main/figure）。
- 字体：Anton 与 IBM Plex Mono 由 `next/font` 在**构建期**下载并自托管，线上不请求
  Google Fonts；中文正文使用系统字体栈（苹方 / 微软雅黑），无 CJK 字体下载成本。

## 命令

```bash
npm install        # 安装依赖（构建期需要网络，用于拉字体）
npm run dev        # 本地开发 http://localhost:3000
npm run build      # 静态导出到 out/
npm run preview    # 本地预览 out/（或 python3 -m http.server -d out 4173）
```

## 内容更新（上新流程）

全部内容集中在 **`src/data/site.ts`**：站点信息、作品、影片、模板、手记、社媒渠道。
改完 `npm run build` 重新发布 `out/` 即可。后续接图库 / 模板站数据库或 CMS 时，
只需替换这个数据文件的数据源，组件不动。

## 占位资源清单（上线前必须替换）

| 位置 | 现状 | 替换为 |
| --- | --- | --- |
| 首页 Hero 视频 | MDN 公共示例素材（CC0）`flower.mp4` | 自有 showreel，建议放 `public/video/showreel.mp4` 并改 `src/app/page.tsx` 的 `src` |
| 作品 / 影片封面 | picsum.photos 随机图 | 自有作品图，建议放 `public/works/` 并在 `src/data/site.ts` 里改字段 |
| OG 分享图 | 本地生成的占位图 `public/og.png` | 正式品牌图（1200×630） |
| 社媒 / 下载链接 | `#` | 真实地址（`src/data/site.ts`） |
| 模板下载 / 详情 | 站内占位卡 | 接入模板站数据源或直链 |

## 合规注意

- AI 生成内容按《人工智能生成合成内容标识办法》在卡片上带 `AI` 角标
  （数据字段 `aigc: true`），发布前逐项核对。
- 域名 `youc.online` 由 `src/data/site.ts` 的 `site.url` 控制，部署正式环境时可用
  `NEXT_PUBLIC_SITE_URL` 环境变量覆盖（影响 sitemap / canonical / OG 的绝对地址）。

## 部署（nginx 参考）

```nginx
server {
    listen 443 ssl;
    server_name youc.online;
    root /opt/infinite-canvas/main-site/out;   # 按实际服务器 checkout 路径调整
    index index.html;

    location / {
        try_files $uri $uri/ =404;
    }
}
```

静态导出的 `out/` 已带尾斜杠目录形式（`/works/` → `out/works/index.html`），
`try_files` 无需 `$uri/` 以外的回退分支。可选部署方式：在服务器构建（需 Node）或
本地 / CI 构建后同步 `out/`，二选一，按现有发布流程择一固定。
