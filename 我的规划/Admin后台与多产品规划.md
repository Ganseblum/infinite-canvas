---
title: Admin 后台与多产品规划
description: 把管理后台拆到独立域名（admin.youc.online）、区分 sim 与 prod 环境，并为后续多产品共用一套后台的前端架构设计
---

# Admin 后台与多产品规划

本文档规划管理后台的域名、环境隔离与多产品形态。当前阶段只落地「本产品 + 独立域名」；多产品共用后台是后续演进方向，现在只做不返工的准备工作。

## 现状

- 管理后台不是独立应用：页面是 `web` 应用里的 `/admin/*` 路由，接口是同一个 api 服务的 `/api/admin/*`。
- 权限已由服务端强制：`Auth` + `AdminOnly` 中间件，非管理员访问 `/api/admin/*` 一律 403；前端 `RequireAdmin` 只负责跳转。
- 登录态：access token 存内存走 `Authorization` 头，refresh token 是 host-only cookie（`Path=/api/auth`、`SameSite=Lax`）。
- 管理页面本身约 2.6k 行，依赖 `web` 的请求层、主题、i18n 与状态库。

## 结论一：admin 必须区分 sim 与 prod

**结论：区分，用两个域名，不做应用内环境切换。**

理由：

- 两套环境的数据库完全隔离，admin 页面只能指向其中一个 API；不存在「一套 admin 同时管两个环境」的安全形态。
- 后台操作包含封禁、扣赠点数、改订单等不可逆动作，环境切换按钮等于给误操作留了入口。
- 两套环境的 admin 账号本来就独立（`ADMIN_EMAIL` 各自初始化），会话天然无法共用。

域名约定与主站保持一致的前缀风格：

| 环境 | 域名 | 后端 API |
| --- | --- | --- |
| 正式 | `admin.youc.online` | `art.youc.online/api` |
| 测试 | `sim-admin.youc.online` | `sim-art.youc.online/api` |

实施方式：

- 同一份前端构建产物，运行时由容器入口脚本注入 `API_BASE_URL` 与 `SITE_ENV`（现有机制已支持），不引入第二套构建。
- 顶栏保留现有环境标识；正式环境建议改用更醒目的样式（如红色角标），避免在测试站当成正式站操作。
- 若未来确实需要环境切换，只允许在测试环境提供「切到正式」入口，并且必须二次确认；正式环境不提供任何切换。

## 结论二：多产品共用一个 admin，分两步

目标是 `admin.youc.online` 同时承载 infinite-canvas、blog、ppt work 等多个产品。

**第一步（现在，不返工的准备）**

- `admin.youc.online` 先按域名分流到本产品，主域名禁掉 `/admin` 路径。
- 后台 API 增加 `GET /api/admin/meta`：返回产品标识、展示名、版本、可用模块清单。
- 模块清单以**现有 `/api/admin/*` 接口分组**为准，不要凭设计稿臆列，否则渲染出空菜单或死链。当前已交付的 11 个模块：

  | 模块 | 后端接口 | 前端位置 |
  | --- | --- | --- |
  | `dashboard` | `/api/admin/stats`、`/stats/revenue` | `/admin` |
  | `users` | `/api/admin/users/*` | `/admin/users` |
  | `models` | `/api/admin/models/*`、`/model-promotions/*` | `/admin/models` |
  | `channels` | `/api/admin/channels/*`、`/api/admin/requests/refunds/retry`（该重试接口目前无前端入口） | `/admin/channels` |
  | `moderation` | `/api/admin/moderation/records/*` | `/admin/moderation` |
  | `packages` | `/api/admin/credit-packages/*` | `/admin/credit-packages` |
  | `orders` | `/api/admin/orders` | `/admin/orders` |
  | `settings` | `/api/admin/settings` | `/admin/system` 的「站点设置」tab |
  | `admins` | `/api/admin/admins/*` | `/admin/system` 的「管理员」tab |
  | `community` | `/api/admin/community/works`、`/reports` | `/admin/system` 的「社区」tab |
  | `audit` | `/api/admin/audit-logs` | `/admin/system` 的「审计日志」tab |

  后续新增：`tickets`（客服工单，见本文档「后端角色与工单」一节）。
- 两点注意：`activity`（签到与邀请）**没有独立后台面**，其开关与奖励值是 `/api/admin/settings` 的字段，归入 `settings`，不要单列成模块；`community`、`admins`、`audit` 三个模块当前不是顶级导航项，而是 `/admin/system` 页面内的 tab，抽独立应用时要一并决定它们的导航形态。
- 后台主导航现在是顶栏横向 Tabs（`web/src/layouts/admin-layout.tsx`），已占 8 个位置；第二步加产品切换器时要把横向空间一并算进去，必要时改为侧边栏。
- 新增 CORS 白名单中间件：服务端目前**没有任何 CORS 处理**，这是本次要新写的中间件，不是改个配置。白名单走环境变量，默认只允许对应环境的 admin 域名；因为要带 `credentials`，响应头必须是精确的 `Access-Control-Allow-Origin`，不能用通配 `*`，并要处理 `OPTIONS` 预检。
- 后台代码不假设同源：所有请求统一走已有 API 客户端，禁止硬编码相对路径。
- 主域名响应给 `/admin` 路径返回 404 或跳转 admin 域名；`admin.youc.online` 加 `X-Robots-Tag: noindex`。

**第二步（触发条件：第二个产品立项）**

- 抽出独立 admin 应用（新 `admin/` 目录），从 `web` 迁移后台页面、请求层与主题的最小集。
- 引入产品注册表（静态配置或配置接口）：每项包含 `id`、`name`、`logo`、`apiBase`（prod / sim 各一）、`modules`。前端顶栏做产品切换，按 `modules` 渲染菜单。
- 认证采用「每产品独立会话」：进入某产品时用该产品的 admin 账号登录，token 与 refresh cookie 按产品隔离；暂不做平台级 SSO。
- 跨产品的汇总看板（收入、用户数）不在第二步范围，需要时再对各产品 API 做扇出聚合。

**第三步（仅当多产品都需要统一管理员身份时）**

- 新增中心认证服务（平台账号），各产品信任平台签发的管理令牌；这属于独立立项，不在本规划内。

**前置条件**：多产品共用能否成立，取决于产品们是否复用同一套后端账号服务模板（同一份 `/api/admin/*` 接口形状）。新产品的后端若不采用该模板，需要先对齐接口，否则 admin 应用无法直接接入。

## 技术栈与工程结构（P1 实施约定）

**结论：沿用 web 现有技术栈，不引入新框架。** 管理页面约 3.1k 行已按此实现，换栈等于重写，而后台的价值在功能不在框架。

| 层 | 选择 | 说明 |
| --- | --- | --- |
| 构建 | Vite 7 + bun | 与 web 一致，镜像构建流程可沿用 |
| 框架 | React 19 + TypeScript | 页面已实现 |
| 组件库 | Ant Design 6 | 现有页面用到 24 种 antd 组件（Table / Form / Modal / Drawer / Tabs 等） |
| 布局与样式 | Tailwind 4 | 沿用现状分工：antd 管组件，Tailwind 管排布与主题 token |
| 路由 | react-router 7（library 模式） | 纯客户端 SPA，不用 framework / loader 模式 |
| 数据层 | @tanstack/react-query 5 | 现有页面在用，缓存与失效机制现成 |
| 客户端状态 | zustand，只保留会话，不新增 | admin 没有跨页共享的客户端状态 |
| i18n | react-i18next | `admin` 命名空间（中英各 294 行）已就绪 |
| 日期 | dayjs | 现成 |

不采用其它方案的理由：

- **Refine / react-admin / Ant Design Pro 脚手架**：这类方案是「按其数据层模型写页面、由框架生成 CRUD」。现有 8 个页面已按 antd + react-query 手写完成，迁移等于重写 3.1k 行；且其 dataProvider 抽象与「同一契约 + 按 `apiBase` 切换产品」的方向不合。注：`@ant-design/pro-components` 已在 web 依赖中，但全仓仅用于 `ProConfigProvider` 做主题透传，ProTable / ProForm 未使用。
- **Next.js / SSR**：admin 为纯客户端 SPA，数据全部来自带 Bearer 令牌的接口，无 SSR 与 SEO 需求；引入后镜像需额外承载 Node 运行时（现为 nginx 静态托管），只增加部署复杂度与资源占用。

工程结构（P1 阶段）：

- 新建 `admin/` 独立 Vite 应用，自带 `package.json`、vite/tsconfig、`index.html`、`Dockerfile`；8 个页面与 `services/api/admin.ts` 从 `web` 迁入。
- 会话、请求层、主题、i18n 等外壳**先用路径别名复用 `web/src`**：不复制逻辑、不搭 monorepo，与「不提前抽组件库」的取舍一致；第二个产品立项时再抽 `packages/shell`。
- 两个实施要点：`admin` 的 tsconfig 与 vite 别名的类型解析要跨目录可用；Tailwind 4 的 `@source` 必须同时覆盖 `admin/src` 与 `web/src`，否则复用外壳上的类名不会被生成。
- 布局由顶栏 Tabs 改为侧边栏（antd Layout + Menu）：现有 8 个 tab 已铺满顶栏，产品切换器与按产品渲染模块都需要空间。
- 打包与部署：独立镜像（bun 构建 → nginx 静态），运行时经 `docker-entrypoint.sh` 注入 `API_BASE_URL` / `SITE_ENV`，同一镜像可指向不同产品与环境；compose 新增 `admin` 服务，端口 3101（测试）/ 3201（正式），版本可独立于 web 发布。

## 后端角色与工单（后续，不在本期实施）

- 角色从 `user` / `admin` 扩为 `user` / `support` / `admin`；中间件拆为 `AdminOnly`（仅 admin）与 `SupportOrAdmin`（工单接口）。
- 管理员任命：`PATCH /api/admin/users/:id/role`，仅 admin 可调用、不能修改自己，写入审计日志；后台用户页提供「设为管理员 / 客服」。
- 客服只能访问工单模块，看不到用户、模型、渠道、订单等其他后台页面。
- 反馈/工单：用户端「反馈」入口与「我的工单」；客服端工单台（筛选、指派、回复、关闭）；表 `tickets` + `ticket_messages`，附件复用媒体存储与审核链路。是否把社区举报统一进工单，实施前再定。

## 分期与验收

| 阶段 | 范围 | 验收 |
| --- | --- | --- |
| P0 | DNS 分流、主域名禁 `/admin`、`/api/admin/meta`、CORS 白名单、noindex、环境标识强化 | 两个 admin 域名分别指向各自 API；非管理员 403；主域名 `/admin` 不可访问；跨域请求成功 |
| P1 | 抽独立 admin 应用 + 产品注册表 + 产品切换 + 每产品独立登录 | 两个产品可切换；会话互不串联；模块按产品隐藏 |
| P2 | 角色拆分、管理员任命、客服工单台、用户反馈页 | 客服仅见工单；任命写入审计；用户可提交与回复工单 |
| P3 | 平台级 SSO | 单独立项 |

## 落地状态（2026-09-18 更新）

- **P0 全部完成**：DNS 分流、主域 `/admin` 真 404（nginx 已在宿主机应用）、`GET /api/admin/meta`、CORS 白名单、noindex、正式环境红色角标均已上线并验收。
- **P1 的「不返工准备」已完成**：admin 独立应用已拆出（原计划 P1 才抽，提前落地）；`GET /admin/meta` 返回产品标识（id/name/version）与按调用者权限过滤的模块清单，与 `/admin/me` 同为免权限点接口；前端 `admin/src/lib/products.ts` 静态注册表（当前产品 connected，blog / 办公套件 planned），侧边栏产品切换器 + `/admin/product/:key` 未接入占位页。
- **P1 剩余**（触发条件：第二个产品立项）：per-product `apiBase` 切换与每产品独立登录态。planned 产品接入的硬前置不变：该产品后端必须实现同一套 `/api/admin/*` 契约（相对各自 apiBase 的路径与响应形状一致），并加入对应环境的 CORS 白名单；纯静态或数据只在浏览器里的产品只能在切换器里做外链。
- **P2/P3 未动**，触发条件不变。

## 本产品后台的能力维度管理（图片 / 视频 / 音频 / 文本）

背景：模型目录以前是一张平表加一套通用约束表单，图片、视频、音频模型的管理方式没有区分。2026-09-18 起按能力分区：

- **列表层**：模型页顶部按能力筛选（全部/图片/视频/音频/文本，带数量），能力列保留，价格、免费试用列不变。
- **编辑层**：新建/编辑抽屉按能力渲染不同的约束分组，数据结构与提交格式不变（`constraints` 仍是字符串数组映射，价格矩阵逻辑不动）：
  - 图片：尺寸（size）、质量（quality）、背景（background）、单次张数（n.max）、特性（参考图/蒙版）
  - 视频：分辨率（resolution）、比例（ratio）、时长档（duration，4-30 秒）、特性（水印/生成音频/参考视频/参考音频）
  - 音频：格式（format）、音色（voice）、语速（speed）
  - 文本：单次条数（n.max）
  - 常用键给建议值下拉（tags 可自由输入）；老数据里不属于当前能力预置分组的键收进「自定义约束」区原样保留，能力切换保留已填值。
- **边界**：折扣活动匹配参数与计价维度下拉刻意不随新分组扩大——服务端 `ValidatePromotion`/`ValidateCreditCost` 目前只认 size/ratio/resolution/quality/duration 五个维度；要扩展须先放宽服务端白名单。
- **多产品下的能力管理**：能力维度是画布产品的模块内概念，不进 `/admin/meta` 的模块清单；其它产品（如 blog 的文章/分类管理）在各自后端的模块清单里表达，admin 前端按注册表 + meta 渲染。

## 明确不做

- 不在应用内提供正式环境与测试环境的一键切换。
- 不为多产品提前抽组件库或插件机制，等第二个产品立项再抽。
- 不把 refresh cookie 提到根域名（`.youc.online`）共享，避免任何子域都能拿到令牌。
- 不为 admin 单独换技术栈：不引入 Refine / react-admin 等后台脚手架，不用 Next.js 或 SSR，不上微前端；也不加图表库（仪表盘现为卡片数字，需要时再定）与组件二次封装层。
