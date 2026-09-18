---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
plan_version: 0.1.0
status: approved
objective: 趁未上线把身份/会员/点数/空间收敛为 platform 四域并分三步迁移（M1 身份切换、M2 权益中心化、M3 OIDC Provider），全程画布前端零改动。
recorded_at: 2026-09-18T12:00:00+08:00
updated_at: 2026-09-18T22:17:00+08:00
---

# 平台账号与会员体系（M1-M3）执行计划

> 输入材料：`我的规划/平台账号与会员体系设计.md`（含 Picwand 实地调研，2026-09-18）；技术负责人架构门决策摘要（2026-09-18 定稿，本文全量采用）。
> 代码证据（2026-09-18 读取）：`server/internal/handler/admin.go:274` 与 `server/internal/middleware/authz.go:66`（裸 SQL 断裂点）；`server/internal/handler/testutil_test.go:251`（barrier 依赖表名 refresh_tokens）；`server/internal/db/db.go:52`（AutoMigrate 清单与 SeedPlans free/paid/sunset）；`server/internal/authz/catalog.go:19-42`（现有权限点）；`server/internal/service/request.go:98-112`（生成幂等 unique 查询）；`server/internal/service/orders.go:89-275`（provider_order_id 回写与查单兜底）；`server/internal/service/quota.go:31-237`（ErrReadOnly→402 语义）；`server/internal/handler/auth.go:25`（ic_refresh cookie 名）。
> 本计划是执行契约：status: approved。2026-09-18 用户确认「按规划全部实现开工」，D1-D7 按建议拍板，解除阻塞。

## 进展概览

进度：0/11 已完成、2 进行中（T01 模型改造、T02 奇偶清单）、0 任务阻塞；范围 = M1 身份切换 + M2 权益中心化 + M3 OIDC Provider（画布前端零改动红线贯穿全程）。D1-D7 已于 2026-09-18 由用户按建议拍板，计划提为 approved。

| 事项 | 业务侧解读 | 技术侧解读 | 交付效果 |
| --- | --- | --- | --- |
| M1 身份先行 | 用户账号升级为平台账号，一次注册全产品通用，登录/注销体验不变 | users→platform_users（主键值不变）、refresh_tokens→sessions，auth/middleware/admin/authz 全量切到 identity 域，API 键与值形状不变 | 画布 web/ 零 diff；登录/注销/封禁回归一致；真实 MySQL 迁移启动通过 |
| M2 权益中心化 | 会员、点数、空间变成平台级权益：买一次会员全产品生效，点数全产品通用，空间共享池 | membership/billing/storage 三域建表与域服务；档位派生、扣费、配额预检改调平台域（进程内）；流水带 product 维度 | /api/credits*、/api/orders、/api/plans 形状不变仅可加字段；admin 仅加法扩展；三条关键路径异常矩阵落地 |
| M3 OIDC Provider | 未来 blog 等新产品用标准 OAuth 接入平台登录，无需各造账号 | authorize/token/userinfo/jwks 四端点 + oauth_clients 表 + PKCE（RS256 15min） | 测试 client 全链路打通；admin SSO 管理页可用；authz 增 sso 权限点 |
| 前置与横切 | 套餐怎么拆、空间怎么管等 7 项拍板后才能定稿执行 | 接口奇偶清单（T02）与测试用例库（T11）先于/并行于实现 | 未决项关闭后计划可提为 approved；冒烟集进入验证命令 |

- 我们在哪：计划 v0.1.0（approved）；D1-D7 已按建议拍板，T02 奇偶清单与 T01 落表并行启动，M1-M3 代码未动。
- 下一步：T01/T02 完成 → T03 auth/middleware/admin/authz 切换 identity 域 → T04 真实 MySQL 迁移验收（评审门①）→ M2 → M3。
- 阻塞风险：M1 裸 SQL/字面量断裂点遗漏风险（缓解：grep 逐一确认 + 真实 MySQL 迁移一次，仓库红线）；M2 档位派生废除已获 D6 知情确认；如需新增超时/重试/退避等边界值，必须先报用户确认，不得静默加入。

## 任务清单

| ID | 任务 | 优先级 | 代码落点 | 依赖 | 状态 | 证据回写 |
| --- | --- | --- | --- | --- | --- | --- |
| T01 | 【M1】platform_users 落表与模型改造：users→platform_users（主键值不变）、refresh_tokens→sessions，业务表列名不改（user_id 语义即平台 id）；同步 model 定义与 db.Migrate AutoMigrate 清单 | P0 | server/internal/model/model.go、server/internal/db/db.go | — | ☐ 未开始 | — |
| T02 | 【M1】接口奇偶清单（parity inventory，先于切换）：从真实源码枚举 /api/auth/*、/api/me/*、/api/media/*、/api/credits*、/api/orders、/api/plans 及 admin 相关端点的请求/响应键、错误码与 body，逐项决定 replicate / omit（写明理由）/ defer；清单作为 T03/T06 的对照基准 | P0 | server/internal/handler/（auth.go、account.go、media.go、order.go、admin.go） | — | ☐ 未开始 | — |
| T03 | 【M1】auth/middleware/admin/authz 全量切换到 identity 域：/api/auth/*、/api/me/* 键不变、值来源换域服务；清裸 SQL 断裂点（handler/admin.go:274、middleware/authz.go:66，grep 逐一确认无遗漏）；testutil barrier 表名 refresh_tokens→sessions | P0 | server/internal/handler/auth.go、server/internal/middleware/、server/internal/handler/admin.go、server/internal/handler/testutil_test.go | T01, T02 | ☐ 未开始 | — |
| T04 | 【M1】真实 MySQL 迁移验收 + 全量回归：go test ./... 全绿；真实 MySQL 起服务完成迁移一次（字符串列长度/保留字列名红线）；五个回归点（登录/充值/生成扣费/配额 507/注销）过一遍；web/ 零 diff 机器核对；评审门① 在本任务前 | P0 | server/cmd/server/main.go、server/internal/db/db.go | T03 | ☐ 未开始 | — |
| T05 | 【M2】三域建表与域服务：membership_plans（seed 保留 free/paid/sunset id）+ membership_subscriptions；credit_accounts 替换 credits（移除 paid_until，付费身份由 subscription.period_end 表达）；credit_transactions/orders/media_files 加 product varchar(32) default 'youc-canvas'；credit_packages 按两张表拆分；storage_accounts/storage_usage 新表；free_grant_claims 归 billing；usage_records 仅留试用计数（storage_bytes 指标退役）；plans 表 M2 退役；四域接口按契约表签名落地；零物理外键延续 | P0 | server/internal/platform/{identity,membership,billing,storage}/、server/internal/model/、server/internal/db/db.go | T04 | ☐ 未开始 | — |
| T06 | 【M2】画布业务切换：档位派生 purchased>0⇒paid 废除，改为 membership.ActivePlan（只有点数无订阅回落 free 档但可消费；graceEndsAt=period_end+60 天），映射对照表随本任务交付；生成扣费走 billing.Reserve/Refund；配额预检/复核走 storage.Check/Commit（507/402 body 不变）；试用走 ConsumeFreeTrial/FreeTrialRemaining；跨域写由编排层传 tx 单事务；评审门② 在本任务前 | P0 | server/internal/service/{credit.go,quota.go,free_grant.go,media_write.go,orders.go}、server/internal/handler/{ai.go,media.go} | T05 | ☐ 未开始 | — |
| T07 | 【M2】admin 加法扩展：/api/admin/users 响应追加 products/membership/storage 字段（纯加法）；新增会员管理模块（订阅查询/权益调整/补偿）；authz 目录增 membership.read/write 权限点；流水与用量页增 product 筛选；/admin/meta 形状不变 | P1 | server/internal/handler/admin.go、server/internal/authz/catalog.go、admin/src/ | T05 | ☐ 未开始 | — |
| T08 | 【M2】关键路径异常矩阵落地验证 + 夜间对账任务：生成扣费/支付回调/媒体落盘记账三条路径按异常矩阵逐行验证并留证据；夜间对账任务比对 media_files.bytes 聚合 vs storage_usage vs storage_accounts，不平走 storage.Recalculate 修正 + 告警（告警通道见未决项 D9）；评审门③ 在本任务前 | P0 | server/internal/service/cleanup.go（或新增对账任务文件）、server/internal/platform/storage/ | T06, T07 | ☐ 未开始 | — |
| T09 | 【M3】OIDC Provider 四端点 + oauth_clients：GET /api/oidc/authorize（支持 PKCE；ic_refresh cookie Path 从 /api/auth 扩到 /api，仍 host-only+Lax+HttpOnly）、POST /api/oidc/token（RS256 15min）、GET /api/oidc/userinfo、GET /api/oidc/jwks.json；oauth_clients 表（product 与 /admin/meta product.id 同值）；authz 增 sso.read/write | P1 | server/internal/handler/oidc.go（新增）、server/internal/model/、server/internal/authz/catalog.go、server/internal/handler/auth.go | T06 | ☐ 未开始 | — |
| T10 | 【M3】admin SSO 管理页 + 集成测试：admin 新增 OAuth client 管理页（sso.read/write）；集成测试用测试 client（client_id/secret/redirect_uri/PKCE）跑通 authorize→token→userinfo→jwks 全链路，含未注册 redirect_uri 拒绝、PKCE 缺失拒绝、token 过期用例；评审门④ 在本任务前 | P1 | admin/src/、server/internal/handler/（oidc 集成测试） | T09 | ☐ 未开始 | — |
| T11 | 【横切】测试设计（test-planner）：产出用例库 records/tests/infinite-canvas-platform-account-membership/（契约用例对照 T02 奇偶清单、五个回归点、三条关键路径异常矩阵、OIDC 安全用例）；冒烟集供「验证命令」引用；端到端用例须在评审门③/④ 关闭前全部通过 | P1 | records/tests/infinite-canvas-platform-account-membership/ | T02 | ☐ 未开始 | — |

### 未决项

| 编号 | 决策/事项 | 内容与选项 | 建议 | 依赖 | 状态 | Owner |
| --- | --- | --- | --- | --- | --- | --- |
| D1 | 空间共享池 | 平台共享池 / 各产品独立配额 | 共享池（业界一致，体验最好） | T05, T06 | ✅ 已拍板（按建议） | 用户 |
| D2 | 平台会员 | 平台会员一次订阅全产品生效 / 各产品各自会员 | 平台会员 | T05, T07 | ✅ 已拍板（按建议） | 用户 |
| D3 | 点数共享余额 | 一个余额全产品消耗（流水带 product 维度）/ 各产品独立钱包 | 共享余额 | T05, T06 | ✅ 已拍板（按建议） | 用户 |
| D4 | 平台新人礼 | 注册即赠平台统一发放 / 各产品自行发放；并确认每日免费额度、永久买断档是否加入 backlog | 平台新人礼；每日额度与买断档仅入 backlog | T05, T06 | ✅ 已拍板（按建议） | 用户 |
| D5 | 套餐两张表拆分 | membership_plans（订阅）+ credit_packages（点数包）分表，不再一张表混装 | 是 | T05, T07 | ✅ 已拍板（按建议） | 用户 |
| D6 | M2 行为变更知情 | purchased>0⇒paid 派生废除：只有点数、无有效订阅的用户回落 free 档（仍可消费），graceEndsAt=period_end+60 天；需用户知情确认后才可切换 | 按技术负责人方案执行，用户确认即视为拍板 | T06 | ✅ 已拍板（按建议） | 用户 |
| D7 | 奖励中心/积分任务体系是否立项 | 签到递增、任务积分、兑换码等平台级增长模块；本期仅 backlog，不影响 M1-M3 实现 | 先入 backlog，后续单独立项 | — | ✅ 已拍板（按建议） | 用户 |
| D8 | ic_refresh Cookie Path 扩面顺序 | M3 直接将 Path 从 /api/auth 扩为 /api，是否需要旧 Path 双发兼容期及灰度顺序 | 待 T09 设计评审定，倾向直接切换（未上线无兼容包袱） | T09 | ☐ 未开始 | 技术负责人 |
| D9 | 夜间对账任务告警通道 | 对账不平时的通知方式（邮件/IM/admin 站内）与值守口径 | 待 T08 设计评审定 | T08 | ☐ 未开始 | 技术负责人 |

说明：D1-D7 已于 2026-09-18 由用户按建议拍板（✅）；D8-D9 为非阻塞技术未决项，随对应任务的设计评审关闭。

## 接口与共享机制

### 四域接口契约

| 域 | 接口签名摘要 | 注册点（代码落点） | 引入任务 |
| --- | --- | --- | --- |
| identity.Service | GetByID(id)、GetByEmail(email)、SetStatus(id, status)、BumpMediaTokenVersion(id)、RevokeSessions(id)、RequestDeletion(id)、CancelDeletion(id)、AnonymizeExpired(清理回调) | server/internal/platform/identity/service.go | T01 建域，T03 起为唯一消费入口 |
| billing.Service | EnsureAccount(tx, userID)、Balance(userID)、Reserve(tx, userID, points, bizKey)、Refund(tx, userID, points, bizKey)、Purchase(tx, userID, packageID)、Adjust(tx, userID, points, reason)、ConsumeFreeTrial(tx, userID, kind)、RefundFreeTrial(tx, userID, kind)、FreeTrialRemaining(userID)、HasGrantedClaim(userID, campaign)；服务构造时绑定 product（画布侧固定 "youc-canvas"） | server/internal/platform/billing/service.go | T05 |
| storage.Service | Snapshot(userID)、Check(userID, deltaBytes)、Commit(tx, userID, product, deltaBytes)、Recalculate(userID)；错误语义保留：ErrReadOnly→402、ErrQuotaExceeded→507 | server/internal/platform/storage/service.go | T05 |
| membership.Service | ActivePlan(userID)（含 60 天日落宽限，graceEndsAt=period_end+60 天）、GrantFromOrder(tx, order)、Compensate(tx, userID, plan, reason)、SyncQuota(tx, userID)（回写 storage_accounts.quota） | server/internal/platform/membership/service.go | T05 |

### 共享机制（跨域约定）

| 机制 | 约定 | 注册点 |
| --- | --- | --- |
| 跨域写事务 | 跨域写 = 一个 MySQL 事务，由编排层传入 *gorm.DB tx；域内不得自开事务拼接跨域写入 | 各域服务首个参数 tx；T05/T06 落地 |
| 零物理外键 | 延续现状：表级隔离 + 应用层校验，不建 FOREIGN KEY | server/internal/model/ 全部新表 |
| product 维度 | credit_transactions / orders / media_files / storage_usage 均加 product varchar(32) default 'youc-canvas'；oauth_clients.product 与 /admin/meta product.id 同值 | T05/T09 迁移定义 |
| 免费新人礼幂等 | free_grant_claims (user_id, campaign_id) unique，归 billing 域收口 | 现状延续（model.FreeGrantClaim），T05 归域 |
| authz 权限点 | M2 增 membership.read / membership.write；M3 增 sso.read / sso.write | server/internal/authz/catalog.go，T07/T09 |
| 会话表 | sessions 替换 refresh_tokens；并发轮换竞争用例的 barrier 表名随 T03 同步 | server/internal/handler/testutil_test.go:251 |

### 关键路径异常矩阵

幂等前置基线（现状延续，2026-09-18 代码核实）：生成扣费 = ai_requests (user_id, idempotency_key) unique（server/internal/service/request.go:112）；支付回调 = orders.provider_order_id unique + 订单状态条件更新（server/internal/service/orders.go）；媒体记账 = media_files 行为事实源，记账随写库事务。

路径一：生成扣费（Reserve → 上游生成 → 成功落账 / 失败 Refund）

| 异常类别 | 检测 | 处置 | 重试责任方/次数/退避 | 幂等前置 | 用户可见结果 |
| --- | --- | --- | --- | --- | --- |
| 超时（上游生成超时/断连） | handler 感知上游错误返回 | 生成失败 → billing.Refund 回滚预留点数 | 画布编排层；上游重试沿用现有 upstream 策略（不改次数/退避）；Refund 为进程内本地事务，无退避 | 请求唯一键保证 Refund 只对应一次 Reserve | 生成失败提示，点数原路退回 |
| 重复请求（同 idempotency_key） | ai_requests unique 冲突命中既有记录 | 直接返回首次请求的结果，不重复扣费 | N/A（无重试，幂等返回） | ai_requests (user_id, idempotency_key) unique | 与首次请求一致 |
| 状态冲突（余额不足/只读态） | Reserve 返回余额不足；quota 判定 ErrReadOnly | 拒绝并返回明确错误码，不落生成请求 | N/A（用户补点/充值后重发新请求） | Reserve 在行锁内先查后扣，并发串行化 | 明确错误码，余额不变 |
| 回调失败 | N/A（生成扣费为同步链路，无异步回调） | N/A | N/A | N/A | N/A |

路径二：支付回调（网关回调/查单兜底 → markPaid → 加点 + membership.GrantFromOrder）

| 异常类别 | 检测 | 处置 | 重试责任方/次数/退避 | 幂等前置 | 用户可见结果 |
| --- | --- | --- | --- | --- | --- |
| 超时（回调未达/延迟） | 订单长期非终态被查单兜底路径发现（现状已有 active query，orders.go:275） | 主动查单 → markPaid 补齐加点和发会员 | billing 编排的后台轮询，沿用现状节奏（次数/退避不改） | provider_order_id unique + 状态条件更新 | 到账可能延迟但不丢失 |
| 重复请求（同单重复回调） | 条件更新影响行数 = 0，识别已处理 | 幂等返回成功，不重复加点/发会员 | N/A（无重试，幂等返回） | provider_order_id unique + 仅 待支付→已支付 条件更新 | 无差异 |
| 状态冲突（金额不符/订单非待支付） | 验签 + 金额比对失败 | 拒绝入账并记录，必要时走退款补偿 | 退款补偿 = RefundPending 标记后台重试（沿用现状机制）；如需新增重试次数/退避属行为边界值，须在评审门② 前报用户确认 | 退款以 credit_transactions 流水判定幂等 | 异常单进入后台补偿/客服流程 |
| 回调失败（处理中异常） | markPaid 事务报错 | 加点、GrantFromOrder、订单状态更新在同一 MySQL 事务（编排层传 tx），任一失败整体回滚，下次回调幂等重放 | 依赖网关重发 + 查单兜底，次数由网关侧决定 | 同一事务 + provider_order_id unique | 无感或短暂延迟到账 |

路径三：媒体落盘记账（storage.Check 预检 → 落盘 → 同事务写 media_files + storage_usage + storage_accounts）

| 异常类别 | 检测 | 处置 | 重试责任方/次数/退避 | 幂等前置 | 用户可见结果 |
| --- | --- | --- | --- | --- | --- |
| 超时（落盘成功但事务失败/进程中断） | 夜间对账任务发现三层记账不平 | 比对 media_files.bytes 聚合 vs storage_usage vs storage_accounts → storage.Recalculate 修正 + 告警 | 平台后台任务每日一次；告警通道见未决项 D9 | Recalculate 以聚合事实源重算，天然幂等 | 短暂误差自动收敛 |
| 重复请求（同文件重复上传） | 沿用现状媒体写入语义（media_write.go）判重 | 不重复记账 | N/A（写入侧幂等） | 记账以 media_files 行存在性判定 | 无差异 |
| 状态冲突（配额不足/只读态） | 上传前 storage.Check 预检返回 ErrQuotaExceeded / ErrReadOnly | 拒绝上传，507 / 402 响应 body 与现状逐字节一致 | N/A（用户清理空间或升级档位后重试） | Check 只读不记账 | 与现状一致的错误体 |
| 回调失败 | N/A（进程内链路，无外部回调） | N/A | N/A | N/A | N/A |

### 发布顺序 × 回滚点

| 步骤 | 内容 | 回滚点 | 回滚动作 |
| --- | --- | --- | --- |
| S1 | M1 身份先行（T01→T04） | M1 验收通过后按仓库发版流程打版本 tag | 回退到上一 tag 重启；测试库重建后按旧 AutoMigrate 清单起库；前端零改动无需回发 |
| S2 | M2 权益中心化（T05→T08） | M2 验收通过后打版本 tag | 回退到 S1 tag；测试库直接重建（未上线无数据迁移包袱） |
| S3 | M3 OIDC Provider（T09→T10） | M3 验收通过后打版本 tag | 回退到 S2 tag；oauth_clients 随库重建消失 |

每个回滚点的前置条件：该步已通过真实 MySQL 迁移启动一次与全量 go test。发布顺序固定 S1→S2→S3，不并行上线。

### 评审门

| 评审门 | 时机（在哪个任务前） | 评审人 | 审什么 |
| --- | --- | --- | --- |
| ① auth 边界 | T04 前 | 独立 reviewer（非实现者） | platform_users/sessions 切换后的登录、刷新轮换、封禁、注销链路；裸 SQL 断裂点清零（admin.go:274、middleware/authz.go:66、testutil barrier）；/api/auth/* 与 /api/me/* 对照 T02 奇偶清单逐项核对 |
| ② billing 事务边界 | T06 前 | 技术负责人 + 独立 reviewer | 编排层传 tx 的跨域单事务边界；Reserve/Refund 路径与幂等前置；档位派生映射对照表（purchased>0⇒paid 废除、graceEndsAt 口径）；如涉及新增边界值已报用户确认 |
| ③ 全量回归 | T08 前 | 独立 reviewer | go test 全绿；真实 MySQL 迁移启动一次；web/ 零 diff 与 admin/ 仅加法机器核对；异常矩阵三条路径逐行落地证据；T11 用例库端到端用例全部通过 |
| ④ OIDC 安全审查 | T10 前 | 独立 reviewer（安全视角） | PKCE 强制与 code 一次性、RS256 私钥保管、redirect_uri 精确匹配、token 15min 有效期、cookie Path 扩面后仍 host-only+Lax+HttpOnly、oauth_clients secret 的存储与展示口径 |

## 验证命令

| 命令/检查 | 预期 |
| --- | --- |
| `cd server && go test ./...` | 全部通过（含 T03 barrier 改名后的并发轮换竞争用例、T05-T08 三域服务用例、T09-T10 OIDC 集成用例） |
| 真实 MySQL 迁移启动一次：`cd server && DATABASE_URL='mysql://<user>:<pass>@127.0.0.1:3306/<db>' go run ./cmd/server`（每步回滚点前各执行一次） | 服务启动完成、AutoMigrate 无错；新列（product 等）与保留字列名风险只在真库暴露；membership_plans seed 存在 free/paid/sunset id。SQLite 单测不能替代本步（仓库红线） |
| `git diff --stat <S1 起点 commit>..HEAD -- web/` | 输出为空——画布前端零改动红线（/api/auth/*、/api/me/*、/api/media/*、/api/credits*、/api/orders、/api/plans 形状不变仅可加字段） |
| `git diff --numstat <S2 起点 commit>..HEAD -- admin/` | 每行 added>0 且 deleted=0（仅加法：users 追加字段、membership/sso 模块、权限点）；/admin/meta 形状不变由 handler 契约测试覆盖 |
| 五个回归点（对照 T02 奇偶清单 + T11 冒烟集逐项执行） | ① 登录：注册/登录/刷新轮换/并发刷新竞争/登出清 cookie，行为与现状一致；② 充值：下单→模拟支付回调→点数到账与会员发放，重复回调不重复入账；③ 生成扣费：成功扣点、失败退点、同 idempotency_key 不双扣；④ 配额：超限上传 507、只读态 402，body 与现状逐字节一致；⑤ 注销：申请→pending_deletion→撤销或到期匿名化，跨表清理完整 |
| 用例库冒烟集：records/tests/infinite-canvas-platform-account-membership/（T11 产出） | 冒烟集全过；端到端用例在评审门③/④ 关闭前全部通过，评审门不因实现者自测通过而豁免 |

验收口径总结：M1/M2 落地后画布行为与现状完全一致（上述五点 + web/ 零 diff 为硬标准）；M3 新增的 OIDC 面只增不改，不影响既有回归点。
