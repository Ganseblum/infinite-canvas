---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T11（测试设计用例库，供 T03/T04 回归、T06/T08 评审门、T10 OIDC 安全审查与验证命令冒烟集引用）
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
recorded_at: 2026-09-18
source: 全部断言值取自 2026-09-18 工作区源码（分支 feature-platform-account-membership），引用均给 file:line；文档与代码不一致点已在各文件标注
---

# 平台账号与会员体系测试用例库（T11）

本用例库是 `records/plans/PLAN-PLATFORM-ACCOUNT-MEMBERSHIP.md`（T11 任务）的交付物，覆盖 M1 身份切换、M2 权益中心化、M3 OIDC Provider 三个里程碑的验证面。所有断言值（键名、状态码、错误码、唯一约束）均来自真实源码并标注 `file:line`，不是从计划文档转抄。

## 文件清单

| 文件 | 内容 | 条数 |
| --- | --- | --- |
| [parity-contract.md](./parity-contract.md) | 契约用例：对照 T02 奇偶清单的请求/响应键、状态码与错误码用例，含中间件错误矩阵与 M2 三域契约（待实现验证） | 82 |
| [regression-points.md](./regression-points.md) | 五个回归点：登录 / 充值 / 生成扣费 / 配额 / 注销，各含步骤、前置、期望与证据记录位置 | 27 |
| [critical-paths.md](./critical-paths.md) | 三条关键路径异常矩阵用例：生成扣费 Reserve/Refund、支付回调 markPaid 幂等、媒体落盘记账三层对账 | 12 |
| [oidc-security.md](./oidc-security.md) | OIDC 安全用例：PKCE、code 一次性、redirect_uri、token 有效期、cookie、secret 存储口径（全部待实现验证） | 14 |
| [smoke.md](./smoke.md) | 冒烟集：`cd server && go test ./...` 可执行 + 真实 MySQL 启动检查，全部引用真实存在的测试函数 | 60 函数 |

## 工作区状态前提（2026-09-18 核实）

- T01 已在工作区落地：`model.PlatformUser`（model/model.go:11）、`model.Session`（model/model.go:39），AutoMigrate 清单为 `&model.PlatformUser{}`/`&model.Session{}`（db/db.go:49-52）。
- T03 的 identity 域切换已在工作区落地：`platform/identity/service.go` 已存在并被 auth/middleware/admin 消费；计划记录的三处裸 SQL 断裂点（admin.go:274、middleware/authz.go:66、testutil barrier）在当前代码中已改写完毕（详见 parity-contract.md 开头的「文档与代码不一致点」）。
- M2 三域（membership/billing/storage）与 M3 OIDC **尚无代码**：`server/internal/platform/` 下只有 identity；main.go 无 /api/oidc 路由。相关用例一律标「待实现验证」，由 T05/T06/T09/T10 落地后补自动化。
- 错误码口径：统一错误形状 `{error:{code,message,fields?,<extra?>}}`（errs/errs.go:98-110）；**READ_ONLY 实际是 403**（errs/errs.go:70），计划 M2 契约写的「ErrReadOnly→402」与代码不符，用例按 403 编写并在引用处标注。

## 如何执行

### 自动测试（SQLite 内存库，单元/集成级）

```
cd server && go test ./...
```

- go.mod（server/go.mod:3、23）确认：Go 1.26.3 + `gorm.io/driver/sqlite v1.6.0`。测试统一用 `newTestDB`（handler/testutil_test.go:60-89）建内存 SQLite、跑 `db.Migrate` + `db.SeedPlans` + `authz.Sync`，与生产启动同一套迁移代码。
- 每条用例的「执行方式」列给出对应测试函数名（全部真实存在，索引见 smoke.md）。
- **红线**：SQLite 单测不能替代真实 MySQL 迁移（计划「验证命令」明确）。

### 真实 MySQL 迁移启动（T04/T06/T08 回滚点前各一次）

```
cd server && DATABASE_URL='mysql://<user>:<pass>@127.0.0.1:3306/<db>' go run ./cmd/server
```

- 检查项：启动完成无错、`platform_users`/`sessions`/`free_grant_claims` 等表建成、`plans` seed 存在 free/paid/sunset（db/db.go:84-101）、字符串列长度与保留字列名不报错。
- 五个回归点中的手工步骤（见 regression-points.md）在此环境执行一遍。

### 证据记录

- 自动测试证据：粘贴到 `evidence/` 下对应文件（命名约定见 regression-points.md 各用例）。
- 手工/真实 MySQL 证据：请求与响应原文（含响应头）记录到 `evidence/`，评审门③/④ 关闭前全部用例必须留痕。

## 与计划的映射

| 计划任务 | 用例覆盖 | 文件 |
| --- | --- | --- |
| T02 奇偶清单 | 全部 PAR-* 契约用例以清单为对照基准 | parity-contract.md |
| T03 identity 切换 | PAR-AUTH / PAR-ME / PAR-ADM / PAR-MW | parity-contract.md |
| T04 真实 MySQL + 五回归点 | REG-* 全部 + 真实库手工步骤 | regression-points.md |
| T05/T06 三域与画布切换 | PAR-M2（待实现验证）+ CP1/CP2/CP3 | parity-contract.md、critical-paths.md |
| T07 admin 加法扩展 | PAR-ADM 加法断言（仅增不改） | parity-contract.md |
| T08 异常矩阵落地 + 夜间对账 | CP1/CP2/CP3 逐行 | critical-paths.md |
| T09/T10 OIDC | OIDC-*（待实现验证） | oidc-security.md |
| 验证命令冒烟集 | 全部自动测试函数索引 | smoke.md |

## 数量核对

- 契约用例：82 条（≥40 达标；覆盖 /api/auth 8、/api/me 7、billing 5、orders 4+webhook、media 6、admin 身份端点 11、中间件错误矩阵 8、M2 三域 22）。
- 回归点：5 组共 27 条（每组 ≥4 达标）。
- 异常矩阵：3 路径 × 4 行 = 12 条（逐行覆盖，含「回调失败 N/A」行的前提验证）。
- OIDC：14 条（≥10 达标）。
- 冒烟集：60 个真实测试函数。
