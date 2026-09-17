# 管理后台（admin）

从 `web/` 拆出的独立管理后台应用（独立域名、独立镜像）。切换已完成：`web/` 不再提供 `/admin/*` 路由与后台页面代码，主站只保留用户菜单里的后台入口（运行期配置 `ADMIN_BASE_URL`，留空则隐藏）。

## 本地运行

```bash
bun install
bun run dev        # http://localhost:5174
bun run typecheck
bun run build      # 先跑 prebuild 的 import 边界断言
```

## 本地开发必须保留 `/api` 代理

`vite.config.ts` 里的 `/api` → `http://127.0.0.1:8080` 代理不是可选项：admin 跑在 `localhost:5174`、API 在 `127.0.0.1:8080`，两者属于**跨站**，而登录用的 `Secure` cookie 在 http 下写不进浏览器。去掉代理会看到「登录不工作」，然后很容易顺着去动 `SameSite`——那是误修路径。本地一律走同源代理；跨源部署由服务端 `CORS_ALLOWED_ORIGINS` 与 HTTPS 承担（见 `deploy/README.md`）。

## 复用边界（重要）

`@/*` 别名指向 `../web/src`，**别名不是边界**：映射建立之后，admin 里任何文件都能 import web 的任何模块，TypeScript 与 Vite 都不会拦。边界靠下面两件事守住，且共用同一份清单 `scripts/web-whitelist.mjs`：

1. `scripts/check-import-boundary.mjs` 挂在 `prebuild` 上：扫描 `admin/src/**` 的 import，出现白名单外的 `@/` 路径、或越出 `admin/src` 的相对引用，构建直接失败。
2. `vite.config.ts` 按同一份清单给 Tailwind 注入 `@source`：admin 与 web 是兄弟目录，Tailwind 的扫描范围不覆盖 `web/src`，不显式列出就是**静默失败**（构建成功、样式丢失）。给白名单加一个路径，import 边界与扫描范围同时生效，不用改 CSS。

往白名单加路径前先确认它确实是「外壳」：只加没有业务状态的纯工具/类型，别把页面、store、业务 hook 拖进来。admin 自己的代码一律用 `@admin/*`，与 web 的 `@/*` 分开。

复用的外壳里 `@/i18n` 是模块级单例，admin **不再 init 自己的实例**（否则 `t()` 与 `getApiErrorMessage` 会绑到不同实例，语言切换只作用于其中一个），只把自己的命名空间并进去，见 `src/i18n/index.ts`。

## 依赖版本

跨边界共用的依赖按 `web/` 已安装的版本固定（`antd` / `i18next` / `react-i18next` / `zustand` / `react` / `react-dom`）：`web/src/lib/app-theme.ts` 这类复用文件的类型来自 `web/node_modules` 里的那份 antd，两份 antd 的类型不兼容，类型检查会直接报错。`web/` 升级这些依赖后 admin 要同步，否则 `bun run typecheck` 会在 `app-providers.tsx` 报 `ThemeConfig` 不兼容。

## 路由与身份校验

| 路径 | 说明 |
| --- | --- |
| `/admin` … `/admin/orders` | 8 个管理页，路径与拆分前一致，切换批次不必再改链接 |
| `/admin/no-permission` | 已进后台但当前角色缺该页权限时的说明页，侧边栏保留，可直接走去有权限的页面 |
| `/login` | admin 域自己的登录页，复用主站登录接口，不提供注册入口 |
| `/forbidden` | 已登录但没有后台角色时的说明页，附退出登录（由用户自己点，不自动登出） |
| 其它任意路径 | 重定向到 `/admin`，再由守卫判断身份 |

身份判定只有 `src/hooks/use-console-access.ts` 一个入口（`useConsoleAccess()`），守卫、登录页、说明页、菜单都从这里取结论：

- `status === "booting"` 或 `/admin/me` 在途时只显示全屏 loading：既不放行也不跳转，否则刷新页面会先闪一下登录页；
- 未登录 / 会话失效（401）→ `/login`；已登录但没有后台角色或接口返回 403 → `/forbidden`；有后台角色 → 放行并带上 `role` 与 `permissions`。

### 权限（RBAC）

- 结论来自 `GET /api/admin/me`（不需要具体权限点，有后台角色即可调用），不再看 `user.role` 这个保守投影列。
- **菜单与权限点的映射只有一份**：`src/lib/admin-nav.ts`（`ADMIN_NAV_ITEMS` + `permissionsForPath`）。侧边栏过滤、页面级守卫 `RequirePermission`、`/admin` 首页重定向全部取自它，改动只改这一处。
- 菜单项没有对应权限就不渲染；直输无权限路径由 `RequirePermission` 送到 `/admin/no-permission`，不重试、不空白。`/admin/system` 的四个 tab（设置 / 角色 / 社区 / 审计）同样按权限过滤。
- 查询失败统一走 `components/query-error.tsx`：403 显示「没有访问权限」且不给重试按钮，其他错误保留失败文案 + 重试。
- 前端只负责隐藏入口，**服务端 `RequirePermission` 中间件是唯一权威**：前端判断写错最多是多显示/少显示菜单。角色与权限调整后重新聚焦窗口会重新拉 `/admin/me`。
- 角色与成员管理在 `/admin/system` 的「角色与成员」tab：角色列表（`isSystem`、成员数）、建/改（按模块勾选 `GET /api/admin/permissions` 的返回）/删；系统角色不可改权限、不可删除（UI 禁用，服务端也拒）。权限目录在界面上只读。用户页（`/admin/users`）的「设置角色」需要 `roles.manage`。

### 会话与主站共用

refresh cookie 是 host-only 挂在 API 域（`sim-art.youc.online`）的 `/api/auth` 下，admin 与主站前端打的是同一个 API 域，所以两处是同一个登录态：**在后台登录，主站也处于登录态；在后台退出，主站也会一起退出。** 这条要出现在登录页与侧边栏登出按钮旁（`sharedSession.loginNote` / `sharedSession.logoutNote`），单产品下刻意不做每产品独立会话。

## 尚未做（后续批次）

- 侧边栏顶部的产品切换器、主题与语言切换入口：拆出来后这两个开关暂时只能在主站里改（两边共用 `infinite-canvas:theme_store` 与 `infinite-canvas:locale`）。
- `/admin/system` 社区 tab 里指向主站用户主页的链接用 `ADMIN_BASE_URL` 拼绝对地址，该值为空时降级为纯文本；注意 `ADMIN_BASE_URL` 的语义是**后台自己的域名**（见 `deploy/README.md`），这一处要跳到主站路由仍然需要主站基址，上线前需确认取值。
- 写操作的逐按钮权限隐藏只覆盖了本批新增/改动的位置（角色管理、用户设置角色、站点设置只读、社区写操作）；其余页面的写按钮仍只靠服务端 403 兜底。
- 页面只做到构建与类型检查通过，尚未在浏览器里实测。
