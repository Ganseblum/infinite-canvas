import Link from "next/link";

export default function NotFound() {
  return (
    <main id="view-404">
      <div className="n404 wrap">
        <div className="big">
          4<em>0</em>4
        </div>
        <p>这页活字还没排出来——可能被编辑划掉了，也可能是你打错了地址。</p>
        <div className="cta-row">
          <Link className="btn solid" href="/">
            回首页
          </Link>
          <Link className="btn ghost" href="/topics">
            逛逛栏目
          </Link>
        </div>
      </div>
    </main>
  );
}
