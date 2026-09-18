import Link from "next/link";
import { site } from "@/data/site";

const NAV = [
  { href: "/works/", label: "影像 STILL" },
  { href: "/films/", label: "视频 MOTION" },
  { href: "/templates/", label: "模板 FILES" },
  { href: "/blog/", label: "手记 LOG" },
];

/** 全站页头。active 由各页面传入，静态导出下不需要运行时判断路径。 */
export default function Header({ active = "" }: { active?: string }) {
  return (
    <header className="bar">
      <div className="wrap bar-inner">
        <Link className="logo unb" href="/" aria-label={`${site.name} 首页`}>
          HANG<span>.</span>
        </Link>
        <nav aria-label="主导航">
          {NAV.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className={`mono${active === item.href ? " on" : ""}`}
              aria-current={active === item.href ? "page" : undefined}
            >
              {item.label}
            </Link>
          ))}
        </nav>
        <span className="geo mono">{site.location}</span>
      </div>
    </header>
  );
}
