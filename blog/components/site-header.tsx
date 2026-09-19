"use client";

import { useEffect, useState } from "react";

// 顶栏：导航 + RSS + 明暗主题切换。主题在 layout 的内联脚本里已防闪落值，
// 这里只负责切换与持久化（localStorage > 系统偏好）。
export default function SiteHeader() {
  const [theme, setTheme] = useState<"dark" | "light">("dark");

  useEffect(() => {
    const current = document.documentElement.getAttribute("data-theme");
    setTheme(current === "light" ? "light" : "dark");
  }, []);

  const toggle = () => {
    const next = theme === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try {
      localStorage.setItem("blog-theme", next);
    } catch {}
    setTheme(next);
  };

  return (
    <header>
      <div className="wrap bar">
        <a className="logo" href="/">
          HANG<i>.</i>
        </a>
        <nav>
          <a href="/">手记 LOG</a>
          <a href="/topics">栏目 TOPICS</a>
          <a href="/bookmarks">收藏 MARKS</a>
          <a href="/about">关于 ABOUT</a>
        </nav>
        <div className="bar-right">
          <a className="rss mono" href="/feed.xml">
            RSS
          </a>
          <button className="theme-toggle mono" onClick={toggle} aria-label="切换明暗主题">
            {theme === "dark" ? "☀︎" : "☾"}
          </button>
        </div>
      </div>
    </header>
  );
}
