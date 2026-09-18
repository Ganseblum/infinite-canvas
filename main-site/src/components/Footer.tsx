import Link from "next/link";
import { site } from "@/data/site";

export default function Footer() {
  return (
    <footer className="foot">
      <div className="wrap foot-grid">
        <div className="wordmark unb" aria-hidden="true">
          HANG.
        </div>
        <nav className="foot-links mono" aria-label="外部链接">
          {site.channels.map((c) => (
            <a key={c.label} href={c.href}>
              {c.label} ↗
            </a>
          ))}
          <a href={`mailto:${site.email}`}>{site.email} ↗</a>
        </nav>
      </div>
      <div className="wrap foot-base mono">
        <span>© 2026 {site.name} — youc.online</span>
        <span>SET IN ANTON / 系统黑体 — DESIGNED ON CANVAS</span>
      </div>
    </footer>
  );
}
