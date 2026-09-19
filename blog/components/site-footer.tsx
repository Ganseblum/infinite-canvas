export default function SiteFooter() {
  return (
    <footer>
      <div className="wrap foot-grid">
        <div className="wordmark" aria-hidden="true">
          HANG.
        </div>
        <nav className="foot-links mono" aria-label="链接">
          <a href="https://youc.online" target="_blank" rel="noreferrer">
            主站 ↗ youc.online
          </a>
          <a href="https://github.com/Ganseblum" target="_blank" rel="noreferrer">
            GITHUB ↗
          </a>
          <a href="mailto:hi@youc.online">hi@youc.online ↗</a>
          <a href="/feed.xml">RSS /feed.xml</a>
        </nav>
      </div>
      <div className="wrap base mono">
        <span>© {new Date().getFullYear()} 航 HANG — blog.youc.online</span>
        <span>SET IN ANTON / NOTO SERIF — 深夜排字房手工排印</span>
      </div>
      <div className="end mono">你已到底，底下没有了 ✺ </div>
    </footer>
  );
}
