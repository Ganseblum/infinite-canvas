export const metadata = {
  title: "关于",
  description: "主站是放映厅，这里是放映厅后台的排字房。",
  alternates: { canonical: "/about" },
};

/** 关于页（/about）：纯静态内容，无数据请求；SEO 元信息由静态 metadata 提供。 */
export default function AboutPage() {
  return (
    <main id="view-about">
      <div className="pagehero wrap">
        <p className="kick mono">ABOUT — 关于本站</p>
        <h2 className="anton">
          HANG&apos;S <span style={{ color: "var(--acc)" }}>NOTEBOOK.</span>
        </h2>
        <p className="zh">主站是放映厅，这里是放映厅后台的排字房——作品怎么被做出来，就在这里怎么被写下来。</p>
      </div>

      <section className="wrap">
        <div className="about-grid">
          <div className="about-txt">
            <p>
              我是<b>航</b>，做图像，也做动态；排得了一页 PPT，也写得了长文。白天在
              <b>无限画布</b>里生产作品，晚上把过程里的坑、轮子和取舍记在这里——权当给下一次的自己留排字稿。
            </p>
            <p>
              这里写三类东西：<b>怎么做的</b>（AI 工作流、独立开发）、<b>怎么排的</b>（模板、配色与排版）、
              <b>怎么想的</b>（创作方法与碎念）。月更，一期一到四篇，宁可慢，不烂尾。
            </p>
            <p>
              文章全部以 Markdown 起稿，在后台排好版、点了「发布」才会见读者。AI 参与生成的封面与配图一律带角标，
              正文一个字一个字自己写的。
            </p>
            <div className="chan-row mono">
              <a href="https://youc.online" target="_blank" rel="noreferrer">
                主站 ↗ youc.online
              </a>
              <a href="https://github.com/Ganseblum" target="_blank" rel="noreferrer">
                GITHUB ↗
              </a>
              <a href="mailto:hi@youc.online">hi@youc.online ↗</a>
              <a href="/feed.xml">RSS /feed.xml</a>
            </div>
          </div>
          <aside className="colophon">
            <h3>排字房铭牌 — COLOPHON</h3>
            <dl>
              <dt>稿纸</dt>
              <dd>Markdown（GFM 表格 / 任务列表 / 脚注 / Callout）</dd>
              <dt>排印</dt>
              <dd>Next 服务端渲染 + Go goldmark 管线，无追踪脚本</dd>
              <dt>字体</dt>
              <dd>Anton / IBM Plex Mono / Noto Serif SC</dd>
              <dt>代码</dt>
              <dd>服务端高亮（Chroma · monokai），带文件名标签</dd>
              <dt>订阅</dt>
              <dd>RSS 全文订阅 /feed.xml</dd>
              <dt>管理</dt>
              <dd>后台排字：新增 / 编辑 / 发布 / 下架</dd>
              <dt>托管</dt>
              <dd>blog.youc.online — 自有服务器</dd>
            </dl>
          </aside>
        </div>
      </section>
    </main>
  );
}
