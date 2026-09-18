---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T11 OIDC 安全用例（评审门④ 安全审查输入；T09/T10 落地前全部「待实现验证」）
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
recorded_at: 2026-09-18
source: 现状基线引用现有 cookie/JWT 代码（auth/auth.go、handler/auth.go）；OIDC 目标行为引用计划 T09/T10 与评审门④定义
---

# OIDC Provider 安全用例（oidc security）

> **状态：全部「待实现验证」。** M3 的 /api/oidc/* 四端点与 oauth_clients 表尚无代码（`server/internal/platform/` 仅 identity，main.go 无 oidc 路由，2026-09-18 grep 核实），本文件按计划 T09/T10 契约与评审门④清单预写，由 T09/T10 落地后补自动化（集成测试用测试 client 跑通 authorize→token→userinfo→jwks 全链路）。
> 现状基线：ic_refresh cookie 为 host-only + Path=/api/auth + Lax + HttpOnly + Secure=cfg.CookieSecure（handler/auth.go:70-80）；access token HS256 15 分钟（auth/auth.go:14）；这两处是 OIDC 用例的对照锚点。

## A. 授权端点（authorize）

#### OIDC-001 PKCE 强制：无 code_challenge 拒绝
- 前置：已注册测试 client（public 客户端）。
- 步骤：`GET /api/oidc/authorize?client_id=...&redirect_uri=...&response_type=code`，不带 code_challenge。
- 期望：拒绝授权（invalid_request 或按 RFC 7636 要求 code_challenge 的错误页），不签发 code。
- 对应任务：T09、T10（评审门④：PKCE 强制）。
- 执行方式：待实现验证——T10 集成测试新增用例。
- 证据：`evidence/oidc-001.md`。

#### OIDC-002 PKCE verifier 不匹配拒绝
- 步骤：authorize 携带 S256 challenge，token 阶段提交错误 code_verifier。
- 期望：token 响应 400 `invalid_grant`，不签发 access token / id token。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-002.md`。

#### OIDC-003 authorization code 一次性
- 步骤：正常换取 token 后，用同一 code 第二次请求 token。
- 期望：第二次 400 `invalid_grant`；code 表记录已被标记消费（一次性，与现状 email token `used_at IS NULL` 条件更新同构，auth.go:509-521 可作实现参照）。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-003.md`。

#### OIDC-004 code 短有效期
- 步骤：取得 code 后等待超过有效期（建议 ≤60s，落地时按实现值回填本用例）再换 token。
- 期望：400 `invalid_grant`。**注意：有效期具体数值属行为边界值，T09 设计评审时需向用户确认后回填。**
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-004.md`。

## B. redirect_uri 精确匹配

#### OIDC-005 未注册 redirect_uri 拒绝
- 步骤：authorize 与 token 均携带未注册的 redirect_uri。
- 期望：拒绝且**不**把错误重定向到攻击者 URI（直接错误页/400）；T10 集成测试覆盖「未注册 redirect_uri 拒绝」（计划 T10 原文）。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-005.md`。

#### OIDC-006 redirect_uri 变体不通过前缀/宽松匹配
- 步骤：用注册 URI 的变体（尾斜杠、大小写、额外 path、不同 scheme、query 注入）请求。
- 期望：全部拒绝——匹配必须是完整字符串精确比较。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-006.md`。

#### OIDC-007 未注册 client_id 拒绝
- 步骤：authorize 传不存在或已停用的 client_id。
- 期望：400/错误页，不泄露任何已注册 client 信息（与登录防枚举口径一致，auth.go:272-279 参照）。
- 对应任务：T09。
- 执行方式：待实现验证。证据：`evidence/oidc-007.md`。

## C. token 端点与 JWT

#### OIDC-008 access token 有效期 15 分钟
- 步骤：解码 token 响应中的 access_token（RS256 JWT）。
- 期望：`exp - iat = 900` 秒（计划 T09「RS256 15min」）；与画布内部 access token 同周期但签名算法不同（现状 HS256，auth/auth.go:49-56），两套密钥/算法不得互通。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-008.md`。

#### OIDC-009 签名算法锁定 RS256，拒绝 none/HS256 混淆
- 步骤：a) 用 `alg=none` 的伪造 token 调 userinfo；b) 用 HS256 + 公钥作为 HMAC 密钥伪造。
- 期望：全部 401 invalid_token（现状 ParseAccessToken 的算法白名单写法可作参照：auth/auth.go:58-66 强制 `SigningMethodHS256`，OIDC 侧应强制 RS256）。
- 对应任务：T09、T10（评审门④：RS256 私钥保管）。
- 执行方式：待实现验证。证据：`evidence/oidc-009.md`。

#### OIDC-010 jwks.json 只暴露公钥
- 步骤：`GET /api/oidc/jwks.json`。
- 期望：200 JSON，仅含公钥参数（kty/n/e/kid）；响应不含任何私钥材料；kid 与签发的 token header 一致。
- 对应任务：T09。
- 执行方式：待实现验证。证据：`evidence/oidc-010.md`。

#### OIDC-011 userinfo 需要有效 bearer token
- 步骤：a) 无 token；b) 过期 token；c) 画布内部 HS256 token 冒充。
- 期望：a/b/c 均 401（userinfo 只接受本 provider 签发的 RS256 token；跨体系 token 不得互通，现状 scope 隔离思想同构，auth/auth.go:89-92）。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-011.md`。

## D. Cookie 与会话扩面

#### OIDC-012 ic_refresh Path 扩面后仍 host-only + Lax + HttpOnly
- 步骤：M3 将 RefreshCookiePath 从 `/api/auth` 扩为 `/api`（auth.go:28 常量改值）后，完成一次登录/刷新/登出。
- 期望：Set-Cookie 仍无 Domain（host-only）、HttpOnly、SameSite=Lax、Secure=cfg.CookieSecure（auth.go:70-80 属性组不变，仅 Path 值变化）；D8 未决项若要求旧 Path 双发兼容期，本用例改为断言双 cookie 行为（评审门④确认后回填）。
- 对应任务：T09（评审门④：cookie Path 扩面后仍 host-only+Lax+HttpOnly）。
- 执行方式：待实现验证；现状基线由 `TestSessionCookiesIssuedRotatedAndCleared`（media_test.go:382）锁定。
- 证据：`evidence/oidc-012.md`。

## E. oauth_clients 与 secret 存储

#### OIDC-013 client secret 存储与展示口径
- 步骤：a) admin SSO 管理页创建 client；b) 列表/详情接口查询；c) 查库核对。
- 期望：secret 只在创建响应中出现一次（现状临时密码同口径：admin.go:173-187 明文只交付一次）；落库为哈希或加密密文（现状渠道 API Key 同口径：AES-256-GCM 加密落库，model/ai.go:10-19）；列表响应不回显 secret；审计日志不含明文。
- 对应任务：T07、T09、T10（评审门④：oauth_clients secret 的存储与展示口径）。
- 执行方式：待实现验证。证据：`evidence/oidc-013.md`。

#### OIDC-014 oauth_clients.product 与 /admin/meta product.id 同值
- 步骤：创建 client 后分别查询 GET /api/admin/meta 与 oauth_clients 行。
- 期望：`product.id` 与 client.product 同值（计划「共享机制」表：oauth_clients.product 与 /admin/meta product.id 同值）；client 认证失败（错误 client_secret）返回 `invalid_client`，不泄露其他 client 信息。
- 对应任务：T09、T10。
- 执行方式：待实现验证。证据：`evidence/oidc-014.md`。

## 汇总

| 编号 | 主题 | 评审门④条目 | 状态 |
| --- | --- | --- | --- |
| OIDC-001/002 | PKCE 强制与校验 | PKCE 强制 | 待实现验证（T09/T10） |
| OIDC-003/004 | code 一次性与有效期 | code 一次性 | 待实现验证 |
| OIDC-005/006/007 | redirect_uri 精确匹配 | redirect_uri 精确匹配 | 待实现验证 |
| OIDC-008/009/010/011 | RS256 15min / jwks / userinfo | token 15min、RS256 私钥保管 | 待实现验证 |
| OIDC-012 | cookie Path 扩面 | host-only+Lax+HttpOnly | 待实现验证 |
| OIDC-013/014 | secret 存储与 product 同值 | secret 存储与展示口径 | 待实现验证 |
