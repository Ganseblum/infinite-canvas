import type { Metadata } from "next";
import Link from "next/link";
import Header from "@/components/Header";

export const metadata: Metadata = {
  title: "页面不存在",
  robots: { index: false },
};

export default function NotFound() {
  return (
    <>
      <Header />
      <main
        id="main"
        className="wrap"
        style={{ minHeight: "70vh", display: "grid", placeItems: "center", textAlign: "center" }}
      >
        <div>
          <p className="tagline mono">SIGNAL LOST — 信号丢失</p>
          <h1 className="unb" style={{ fontSize: "clamp(4rem, 12vw, 9rem)", margin: "0 0 18px" }}>
            4<span className="ol" style={{ color: "transparent", WebkitTextStroke: "1.5px var(--fg)" }}>
              0
            </span>
            4
          </h1>
          <p style={{ color: "var(--soft)", marginBottom: 28 }}>这一段胶片没挂上，回放映厅看看别的。</p>
          <Link className="btn btn-solid" href="/">
            返回首页
          </Link>
        </div>
      </main>
    </>
  );
}
