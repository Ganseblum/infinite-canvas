---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP-parity-inventory
plan_version: 0.1.0
status: draft
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T02（M1 接口奇偶清单，供 T03 切换实现与 T04 回归验收对照）
recorded_at: 2026-09-18
source: 以 2026-09-18 工作区源码为准（分支 feature-platform-account-membership），全部键名来自结构体 json tag 或 gin.H/map 字面量，未凭记忆或文档补写
---

# M1 接口奇偶清单（parity inventory）

> 目的：users/refresh_tokens → platform_users/sessions 切换（T03）前后，所有 API 的请求/响应键、错误码、HTTP 状态完全不变，仅值来源变化。本清单是 T03 实现与 T04 回归验收的对照基准。
> 枚举来源：路由注册 `server/cmd/server/main.go`、`server/cmd/server/admin_routes.go`（admin 共 51 条，由 `admin_routes_test.go:16` 的 `expectedAdminRouteCount = 51` 交叉验证）；handler 逐文件核对。

> **工作区状态说明（2026-09-18）**：本清单枚举期间，工作区有并行的 T01 改动落地（同一分支未提交）：`model.User → model.PlatformUser`、`model.RefreshToken → model.Session`，AutoMigrate 清单同步换为 `&model.PlatformUser{}`/`&model.Session{}`（db/db.go:53-54）。两者 struct 字段与列名逐项不变，且无 `TableName()` 覆盖（GORM 默认命名即 `platform_users`/`sessions`）。因此：
> 1. 本清单的请求/响应键、错误码、HTTP 状态不受该重命名影响，全部按当前代码有效；
> 2. 第 ② 节引用点表中的 `model.User` / `model.RefreshToken` 在当前工作区已对应改写为 `model.PlatformUser` / `model.Session`（36 个文件，逐文件 1:1 映射，无遗漏引用，`model.User`/`model.RefreshToken` 已全仓零残留）；
> 3. 第 ③ 节全部断裂点在重命名落地后逐处重新核实，位置与内容不变（裸 SQL/表名字符串不随模型改名消失）。

## 0. 全局约定（适用于所有端点）

### 0.1 统一错误响应形状（server/internal/errs/errs.go:98-110 `Abort`）

```json
{ "error": { "code": "<业务码>", "message": "<中文描述>", "fields": { "<字段>": "<说明>" }, "<extra键>": "..." } }
```

- `fields` 仅在校验失败带字段说明时出现；`extra` 仅在 `WithExtra` 时出现（如 READ_ONLY 的 planId/used/limit）。
- `RetryAfter > 0` 时附带 HTTP 头 `Retry-After: <秒>`（errs.go:99-101）。
- **红线：错误 body 形状、业务码字符串、HTTP 状态一律 replicate，一个都不能变。**

### 0.2 中间件级错误矩阵（身份切换的直接作用面）

| 中间件 | 位置 | 触发条件 → 响应 | 身份表访问 |
| --- | --- | --- | --- |
| `Auth` | middleware/middleware.go:118 | 无/坏 Bearer → 401 UNAUTHORIZED；过期 → 401 TOKEN_EXPIRED；scope=media 的 JWT → 401 UNAUTHORIZED（auth/auth.go:89-92） | 无（纯 JWT） |
| `MediaAuth` | middleware/middleware.go:138 | 写方法带 cookie → 401 UNAUTHORIZED；cookie 版本不匹配/查无此用户 → 401 UNAUTHORIZED | **users(R) media_token_version**（middleware.go:170） |
| `RequireActiveUser` | middleware/middleware.go:185 | 用户不存在 → 401 UNAUTHORIZED；status=disabled → 403 ACCOUNT_DISABLED | **users(R)** 全行（middleware.go:192-193） |
| `RequireNotPendingDeletion` | middleware/middleware.go:207 | status=pending_deletion → 403 ACCOUNT_PENDING_DELETION | 无（读上下文） |
| `RequirePasswordChanged` | middleware/middleware.go:230 | must_change_password=true 且不在白名单（logout/refresh/me/password）→ 403 PASSWORD_CHANGE_REQUIRED；用户不存在 → 401 UNAUTHORIZED | **users(R) must_change_password**（middleware.go:242） |
| `MaintenanceGate` | middleware/middleware.go:267 | 维护中+写方法+非豁免路径+无后台角色 → 503 MAINTENANCE_MODE | **users(R) role_key**（middleware.go:309） |
| `RateLimit` | middleware/middleware.go:89 | 超限 → 429 RATE_LIMITED（带 Retry-After） | 无 |
| `LoadAdminAccess` | middleware/authz.go:56 | 无后台角色 → 403 FORBIDDEN；查库失败 → 500 INTERNAL_ERROR；角色不存在/权限快照为空 → 403 FORBIDDEN | **users(R) role_key（裸表名断裂点 #2）** + roles/role_permissions(R)（authz.go:82-87，RBAC 表，M1 不动） |
| `RequirePermission` | middleware/authz.go:131 | 快照缺失/无权限 → 403 FORBIDDEN | 无 |

### 0.3 分页约定

- 页式（admin）：`page`（缺省 1）/`size`（缺省 20、上限 100，paging.go:16-17），越界 400 VALIDATION_FAILED；响应 `{items,total,page,size}`。
- 游标式（用户侧列表）：`cursor`/`size`（1-100，缺省 20，credit.go:154-163）；响应 `{items,nextCursor}`，`nextCursor` 无下一页时为 `null`。

### 0.4 会话负载约定（auth 域复用）

- `sessionPayload`（auth/auth.go:423-434）：`{user, accessToken, mustChangePassword, plan:{id,name}}`。
- `userPayload`（auth/auth.go:436-448）：`{id, email, username, displayName, avatarUrl, role, emailVerified, mustChangePassword}`。
- 会话 cookie：`ic_refresh`（Path=/api/auth，auth/auth.go:25-26）+ `ic_media`（Path=/api/media，auth/auth.go:30-31，媒体只读 JWT，带 media_token_version）；access token JWT claims `{sub, role, exp, iat}`（auth/auth.go:49-68），**role 取 user.Role 旧投影列**。

---

## ① 端点清单

奇偶列约定：**replicate = 键与状态码原样保留、仅换值来源（M1 默认）**；omit/defer 见第 ④ 节汇总。全表无特殊标注均为 replicate。

### 1.1 /api/auth/*（8 条，main.go:276-286）

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误（HTTP→业务码） | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| POST /api/auth/register | auth.go:133 `Register` | body：`email`,`username`,`password`（auth.go:127-131） | 201 `sessionPayload`（0.4） | 400→VALIDATION_FAILED(fields: email/username/password)；503→REGISTRATION_DISABLED；409→EMAIL_TAKEN / USERNAME_TAKEN；500→INTERNAL_ERROR | **users(RW)** 建号；credits(W) EnsureCredit；email_tokens(W)；refresh_tokens(W)；下发 ic_refresh+ic_media cookie | replicate |
| POST /api/auth/login | auth.go:249 `Login` | body：`account`,`password`（auth.go:244-247） | 200 `sessionPayload` | 429→RATE_LIMITED（账号连续 5 次失败锁 15 分钟，Retry-After）；401→INVALID_CREDENTIALS；403→ACCOUNT_DISABLED；500→INTERNAL_ERROR | **users(RW)**（读+写 last_login_at）；refresh_tokens(W) 签发 | replicate |
| POST /api/auth/refresh | auth.go:296 `Refresh` | cookie `ic_refresh`（无 body） | 200 `sessionPayload` | 401→UNAUTHORIZED（无 cookie/未知/过期/已撤销复用/并发轮换竞争失败）；403→ACCOUNT_DISABLED；500→INTERNAL_ERROR。复用检测同时撤销该用户全部令牌并递增 media_token_version（auth.go:316-321、372-379） | **refresh_tokens(RW)** 按 token_hash 查、轮换条件更新 `revoked_at IS NULL`（auth.go:343-345）；**users(RW)**（读 + last_login_at/media_token_version） | replicate（轮换竞争语义：RowsAffected=0 → 401 且不触发复用检测，auth.go:351-357） |
| POST /api/auth/logout | auth.go:363 `Logout` | cookie `ic_refresh` | 204 无 body | 无业务错误（恒 204） | **refresh_tokens(W)** 按 token_hash 置 revoked_at | replicate |
| POST /api/auth/verify-email/send | auth.go:462 `VerifyEmailSend` | 无 body（Bearer） | 204 无 body（已验证也 204） | 401→UNAUTHORIZED；429→RATE_LIMITED（同邮箱 3 次/小时，Retry-After） | **users(R)**；email_tokens(W)；中间件 users(R) | replicate |
| POST /api/auth/verify-email | auth.go:486 `VerifyEmail` | body：`token`（auth.go:482-484） | 200 `{user: userPayload}`（无 accessToken，不签会话） | 410→TOKEN_INVALID（缺 token/未知/已用/过期）；500→INTERNAL_ERROR | **users(W) email_verified_at**；email_tokens(RW) 一次性消费 | replicate |
| POST /api/auth/password/forgot | auth.go:532 `ForgotPassword` | body：`email`（auth.go:528-530） | 204 无 body（邮箱是否存在均 204，防枚举） | 400→VALIDATION_FAILED；429→RATE_LIMITED（Retry-After） | **users(R)** 按 email 查；email_tokens(W)；下发重置邮件 | replicate |
| POST /api/auth/password/reset | auth.go:562 `ResetPassword` | body：`token`,`password`（auth.go:557-560） | 204 无 body | 410→TOKEN_INVALID；400→VALIDATION_FAILED(fields: password 长度 8-72)；500→INTERNAL_ERROR。成功同时递增 media_token_version 并撤销全部 refresh token（auth.go:601-611） | **users(W)** password_hash/media_token_version；**refresh_tokens(W)** 全撤销；email_tokens(RW) | replicate |

### 1.2 /api/me/*（7 条，main.go:291-301）

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误（HTTP→业务码） | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/me | account.go:43 `GetMe` | 无 | 200：`user:{id,email,username,displayName,avatarUrl,role,emailVerified,createdAt}`、`plan:{id,name,storageBytes,maxFileBytes,retentionDays}`、`credits:{purchasedMicros,grantedMicros,totalMicros,paidUntil}`、`usage:{storageBytes,freeImageTrialsUsed,freeVideoTrialsUsed}`、`mediaExpiry:{nearestAt,expiringCount}`、`deletion:{status,scheduledAt}`、`readOnly`、`graceEndsAt`（account.go:78-114） | 401→UNAUTHORIZED；500→INTERNAL_ERROR | **users(R)**（含 status/deletion_scheduled_at）；credits/usage_records/media_files(R) 派生 | replicate |
| PATCH /api/me | account.go:232 `UpdateMe` | body：`displayName`,`avatarUrl`（均可空可选，至少一项，account.go:227-230） | 200 `{user: userPayload}` | 400→VALIDATION_FAILED（body 非法或全空）；500→INTERNAL_ERROR | **users(W)** display_name/avatar_url（account.go:250） | replicate |
| POST /api/me/password | account.go:265 `ChangePassword` | body：`oldPassword`,`newPassword`（account.go:260-263） | 204 无 body + 响应头 `X-Session-Refreshed: true`（account.go:316） | 400→VALIDATION_FAILED(fields: newPassword)；401→INVALID_CREDENTIALS（旧密码错）；401→UNAUTHORIZED；500→INTERNAL_ERROR。成功：清 must_change_password、递增 media_token_version、撤销全部 refresh token、重签 cookie（account.go:291-315） | **users(RW)** password_hash/must_change_password/media_token_version；**refresh_tokens(W)** 全撤销 | replicate（唯一强制改密出口，白名单见 0.2） |
| POST /api/me/free-grant/claim | account.go:321 `ClaimFreeGrant` | 无 body | 200 `{campaignId,status,imageTrials,videoTrials,grantedMicros}`（account.go:443-451；幂等重放同形状） | 403→FREE_GRANT_UNAVAILABLE（开关关闭/风控/预算）；403→EMAIL_NOT_VERIFIED；401→UNAUTHORIZED；500→INTERNAL_ERROR | **users(R)** EmailVerifiedAt；free_grant_claims(RW) | replicate |
| POST /api/me/deletion | account.go:403 `RequestDeletion` | body：`password`（account.go:397-399） | 200 `{scheduledAt}`（RFC3339Nano） | 400→VALIDATION_FAILED；401→INVALID_CREDENTIALS；401→UNAUTHORIZED；500→INTERNAL_ERROR | **users(R)** PasswordHash + **users(W)** status/deletion_scheduled_at（service/deletion.go:49-52）；冷静期中间件依赖该值 | replicate |
| POST /api/me/deletion/cancel | account.go:429 `CancelDeletion` | 无 body | 200 `{status:"active"}`（account.go:440） | 409→DELETION_NOT_PENDING；500→INTERNAL_ERROR | **users(W)**（service/deletion.go:58-60） | replicate |
| GET /api/me/export | account.go:135 `ExportMe` | 无 | 200：`{exportedAt, profile:{id,email,username,displayName,avatarUrl,emailVerified,createdAt}, credits:{purchasedMicros,grantedMicros}, orders:[...], generations:[...], canvases:[...]}` + 头 `Content-Disposition: attachment; filename="youc-export.json"`；orders 项键 `id,provider,packageId,priceMicros,currency,purchasedMicros,grantedMicros,status,paidAt,createdAt`；generations 项键 `id,kind,status,prompt,model,durationMs,moderationStatus,createdAt`；canvases 项键 `id,title,nodeCount,connectionCount,createdAt,updatedAt`（account.go:166-224） | 401→UNAUTHORIZED；500→INTERNAL_ERROR | **users(R)**；orders/generations/canvases/credits(R) | replicate |

### 1.3 /api/media/* 与 /api/media-download（6 条，main.go:368-385）

媒体读路径（GET/HEAD）额外认 `ic_media` cookie（MediaAuth，0.2）；写路径只认 Bearer。

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误（HTTP→业务码） | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| HEAD /api/media/:storageKey | media.go:65 `Head` | 路径 storageKey（正则 media.go:29） | 200 头：`Content-Type`,`Content-Length`,`ETag`,`Cache-Control`,`X-Content-Type-Options`,`X-Checksum`（出 orig 时省略 X-Checksum，media.go:96-99），无 body | 400→VALIDATION_FAILED(fields: storageKey)；404→NOT_FOUND；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 **users(R) media_token_version**；media_files(R) | replicate |
| GET /api/media/:storageKey | media.go:103 `Get` | 同上 | 200 二进制（local 直读，支持 304 If-None-Match）或 302 Location（S3 预签名）；头同 HEAD | 400/404/401/500 同上；304（ETag 命中） | 同上 + credits(R) 档位派生（selectBytes media.go:236-274） | replicate |
| PUT /api/media/:storageKey | media.go:293 `Put` | 头：`Content-Type`（白名单 media.go:37-42）、`X-Checksum`（可选）；body：二进制 | 201 `{storageKey,bytes,checksum,mimeType}`（media.go:468-473） | 400→VALIDATION_FAILED(fields: storageKey/Content-Type)；403→EMAIL_NOT_VERIFIED（media.go:318-321）；413→FILE_TOO_LARGE；403→READ_ONLY(+extra: planId,used,limit)；507→STORAGE_QUOTA_EXCEEDED(+extra: used,limit)；409→CHECKSUM_MISMATCH；422→CONTENT_REJECTED；503→MODERATION_UNAVAILABLE；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；**users(R) EmailVerifiedAt**（media.go:313-321）；media_files(RW)、usage_records(W) | replicate |
| DELETE /api/media/:storageKey | media.go:476 `Delete` | 路径 storageKey | 204 无 body（不存在也 204，幂等，media.go:487-490） | 400→VALIDATION_FAILED；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；media_files(W)、usage_records(W) | replicate |
| POST /api/media/:storageKey/download | media_download.go:128 `RequestDownload` | 路径 storageKey | 200 `{url,expiresAt}`（非付费或无干净原件时 `expiresAt:null`、url 为原媒体路径；有原件时 url=/api/media-download/<token>，media_download.go:155-178） | 400→VALIDATION_FAILED；404→NOT_FOUND（归属不符/不存在，防探测）；401→UNAUTHORIZED；429→RATE_LIMITED（60 次/小时）；500→INTERNAL_ERROR | 中间件 users(R)；media_files(R)、credits(R) 档位派生 | replicate |
| GET /api/media-download/:token | media_download.go:183 `ServeDownload` | 路径 token（HMAC 取件令牌） | 200 二进制（local，头 `Content-Disposition: attachment; filename="ic-<id><ext>"`）或 302 Location（S3） | 404→NOT_FOUND（验签/过期/跨用户/行已删一律 404，防探测）；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 **users(R) media_token_version**；media_files(R) | replicate |

### 1.4 点数/档位/模型目录（billing 组 5 条，main.go:388-395）

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误（HTTP→业务码） | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/credits | credit.go:28 `GetBalance` | 无 | 200 `{purchasedMicros,grantedMicros,totalMicros,paidUntil,recent:[流水项]}`；流水项键 `id,bucket,type,amountMicros,balanceAfterMicros,createdAt` + 可选 `refType,refId,note`（credit.go:47-105） | 401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；credits/credit_transactions(R) | replicate |
| GET /api/credits/transactions | credit.go:56 `ListTransactions` | query：`cursor`,`size`,`type` | 200 `{items:[流水项],nextCursor}` | 400→VALIDATION_FAILED(fields: size/cursor)；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；credit_transactions(R) | replicate |
| GET /api/credit-packages | credit.go:110 `ListPackages` | 无 | 200 `{items:[{id,name,priceMicros,purchasedMicros,bonusMicros,entitlementDays,currency}],providers:[string]}`（credit.go:117-129） | 401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；credit_packages(R) | replicate |
| GET /api/plans | credit.go:133 `ListPlans` | 无 | 200 `{items:[{id,name,storageBytes,maxFileBytes,retentionDays}]}`（credit.go:140-150） | 401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；plans(R) | replicate |
| GET /api/models | handler/model.go（`catalogHandler.List`） | 无 | 200 `{items:[ModelSummary]}`（service/catalog.go:259-273） | 401→UNAUTHORIZED | 中间件 users(R)；model_catalogs(R) | replicate（键清单未逐字段核对，**待核实** handler/model.go） |

### 1.5 /api/orders（4 条，main.go:398-406）

订单项键 `orderPayload`（order.go:155-174）：`id,provider,packageId,priceMicros,currency,purchasedMicros,grantedMicros,entitlementDays,status,paidAt,createdAt,updatedAt` + 可选 `providerOrderId`。

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误（HTTP→业务码） | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| POST /api/orders | order.go:34 `Create` | body：`packageId`,`provider`（order.go:29-32） | 201 `{order:订单项, payment:{type,payload}}`（order.go:69-72） | 400→VALIDATION_FAILED(fields: provider/packageId)；403→ACCOUNT_PENDING_DELETION；429→RATE_LIMITED（10 次/小时）；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；**users(R)** 读全行传 OrderService（order.go:49-54）；orders(W) | replicate |
| GET /api/orders | order.go:75 `List` | query：`cursor`,`size`,`status` | 200 `{items:[订单项],nextCursor}` | 400→VALIDATION_FAILED(fields: size/cursor/status)；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；orders(R) | replicate |
| GET /api/orders/:id | order.go:106 `Get` | 路径 id | 200 `{order:订单项}` | 404→NOT_FOUND；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；orders(R) | replicate |
| POST /api/orders/:id/cancel | order.go:129 `Cancel` | 路径 id | 200 `{order:订单项}` | 409→ORDER_ALREADY_PAID；404→NOT_FOUND；401→UNAUTHORIZED；500→INTERNAL_ERROR | 中间件 users(R)；orders(W) | replicate |

补录（同订单域、范围外前缀）：POST /api/payments/webhook/:provider（order.go:186 `Webhook`，公开验签回调；未知渠道/订单 404→NOT_FOUND，验签失败 400→INVALID_SIGNATURE，内部错误 500；成功响应按渠道裸文本 `success` / `{"code":"SUCCESS","message":"成功"}` / 200 空体，不套统一错误形状，order.go:217-227）。身份表读写：无。replicate。

### 1.6 /api/admin/*（51 条，main.go:413-414 + admin_routes.go:67-132）

组级中间件链：`Auth → RequireActiveUser → RequirePasswordChanged → LoadAdminAccess`（main.go:413）。除特别标注外所有端点共享：401→UNAUTHORIZED/TOKEN_EXPIRED、403→ACCOUNT_DISABLED、403→PASSWORD_CHANGE_REQUIRED、403→FORBIDDEN（无后台角色或无权限点，LoadAdminAccess/RequirePermission）、500→INTERNAL_ERROR。下表错误列只写 handler 业务错误。

#### 1.6.1 引导与总览（6 条）

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/admin/me | admin_roles.go:31 `Me` | 无 | 200 `{role:{key,name,isSystem}, permissions:[string]}` | （仅组级错误） | 中间件 **users(R) role_key（断裂点 #2）**+roles(R) | replicate |
| GET /api/admin/meta | admin_meta.go:15 `AdminMeta` | 无 | 200 `{product:{id,name,version}, modules:[...]}` | 403→FORBIDDEN（快照缺失） | 同上 | replicate |
| GET /api/admin/stats | admin.go:612 `Stats` | 无 | 200 `{userTotal,userToday,storageBytes,generationToday,orderToday,revenueMicrosToday,creditsTotal}`（admin.go:625-633） | （仅组级） | 中间件 users(R)；**users 计数（model.User，admin.go:617-618）** | replicate（值口径不变） |
| GET /api/admin/stats/revenue | admin_system.go:330 `RevenueStats` | 无 | 200 `{revenueMicrosToday,ordersToday,revenueMicrosWeek,ordersWeek,revenueMicrosTotal,ordersTotal,paidUsers,userTotal,conversionRate}` | （仅组级） | 中间件 users(R)；**users 计数（admin_system.go:347）** | replicate |
| GET /api/admin/analytics/usage | admin_analytics.go:41 `UsageAnalytics` | query：`days`(7/30/90，缺省 30) | 200 `{summary:{requests,succeeded,failed,successRate,costMicros,activeUsers,activeSessions}, daily:[...], byModel:[...], byCapability:[...], bySpec:[...], topUsers:[{userId,email,displayName,requests,costMicros}]}`（admin_analytics.go:203-218） | 400→VALIDATION_FAILED(fields: days) | 中间件 users(R)；ai_requests(R)；**users(R) email/display_name（admin_analytics.go:181-190，GORM 模型）** | replicate |
| GET /api/admin/settings | admin_system.go:42 `GetSettings` | 无 | 200 `Snapshot` 全量：`announcement,registrationEnabled,maintenanceMode,maintenanceNotice,communityEnabled,checkinEnabled,checkinRewardMicros,inviteEnabled,inviteRewardMicros,inviteeRewardMicros,maxUploadBytes,generationConcurrency`（service/site_setting.go:169-180） | （仅组级） | 中间件 users(R)；site_settings(R) | replicate |

#### 1.6.2 用户管理（9 条）—— 身份切换核心面

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/admin/users | admin.go:251 `ListUsers` | query：`page`,`size`,`sort`(createdAt/purchasedMicros/grantedMicros/storageBytes，前缀 - 倒序),`status`(active/disabled/pending_deletion),`planId`(free/paid/sunset),`q` | 200 `{items:[{id,email,username,role,roleKey,status,emailVerified,planId,purchasedMicros,grantedMicros,paidUntil,storageBytes,createdAt}],total,page,size}`（admin.go:327-346） | 400→VALIDATION_FAILED(fields: page/size/sort/status/planId) | **裸 SQL 断裂点 #1：Table("users") + 硬编码列清单 + planIDExpr**（admin.go:274-316）；JOIN credits/usage_records | replicate（改写后键、planId 派生口径不变） |
| POST /api/admin/users | admin.go:79 `CreateUser` | body：`email`,`displayName`,`roleKey`（admin.go:71-75） | 201 `{user:{id,email,username,displayName,roleKey,status,emailVerified,mustChangePassword,createdAt}, temporaryPassword}`（admin.go:173-187，明文只出现一次） | 400→VALIDATION_FAILED(fields: email/displayName/roleKey)；409→EMAIL_TAKEN / USERNAME_TAKEN；500→INTERNAL_ERROR | **users(W) 建号**（must_change_password=true、email_verified_at=now、role 投影）；credits(W)；admin_audit_logs(W) | replicate |
| GET /api/admin/users/:id | admin.go:349 `GetUser` | 路径 id | 200 `{user:{id,email,username,role,roleKey,status,emailVerified,createdAt,lastLoginAt,planId,planName,storageLimit,maxFileBytes,retentionDays,purchasedMicros,grantedMicros,paidUntil,storageBytes,mediaCount,readOnly}}`（admin.go:367-388） | 404→NOT_FOUND | **users(R)**（含 last_login_at）；credits/usage_records/media_files(R) | replicate（lastLoginAt 值来源需保留） |
| PATCH /api/admin/users/:id | admin.go:401 `PatchUser` | body：`status`(active/disabled)（admin.go:397-399） | 200 `{id,status}`（admin.go:464） | 400→VALIDATION_FAILED(fields: status；含「最后一个系统角色 active 用户」拦截 admin.go:453-457)；404→NOT_FOUND | **users(W) status**；封禁同时 **users(W) media_token_version** + **refresh_tokens(W) 全撤销**（admin.go:438-448）；admin_audit_logs(W) | replicate |
| POST /api/admin/users/:id/password | admin.go:471 `ResetPassword` | body：`password`（admin.go:467-469） | 204 无 body | 400→VALIDATION_FAILED(fields: password 长度 8-72)；404→NOT_FOUND | **users(W)** password_hash/must_change_password（管理员重置他人密码置位，admin.go:489-495）+ media_token_version（admin.go:499-501）；**refresh_tokens(W) 全撤销**（admin.go:503-506）；admin_audit_logs(W) | replicate |
| POST /api/admin/users/:id/credits | admin.go:525 `AdjustCredits` | body：`bucket`(purchased/granted),`amountMicros`,`note`（admin.go:519-523） | 200 `{purchasedMicros,grantedMicros}`（admin.go:569-572） | 400→VALIDATION_FAILED(fields: bucket/amountMicros/note)；402→INSUFFICIENT_CREDITS（扣减穿透）；404→NOT_FOUND | 中间件 users(R)；credits/credit_transactions(W)；admin_audit_logs(W) | replicate |
| POST /api/admin/users/:id/usage/recalculate | admin.go:575 `RecalculateUsage` | 无 body | 200 `{storageBytes}`（admin.go:586） | 404→NOT_FOUND | 中间件 users(R)；usage_records(W) | replicate |
| POST /api/admin/users/:id/media/reclaim | admin.go:589 `ReclaimMedia` | query：`dryRun`(=true) | 200 回收报告：`{userId,dryRun,scanned,reclaimed,freedBytes,storageUsed,storageLimit,items:[{storageKey,bytes,reason}]}`（service/cleanup.go:30-44） | 404→NOT_FOUND | 中间件 users(R)；media_files/storage；credits(R) 档位派生 | replicate |
| PATCH /api/admin/users/:id/role | admin_roles.go:341 `AssignUserRole` | body：`roleKey`(string 或 null=清空)（admin_roles.go:335-337） | 200 `{id,roleKey}`（roleKey 可为 null；角色未变化时不写库不撤销会话，admin_roles.go:370-374） | 400→VALIDATION_FAILED(fields: roleKey/id；防锁死与「不能改自己」)；404→NOT_FOUND | **users(W) role_key/role**（authz/assign.go:22-27）；角色实际变化时 **refresh_tokens(W) 全撤销**（admin_roles.go:394-399）；roles(R)；admin_audit_logs(W) | replicate |

#### 1.6.3 模型目录与折扣（7 条）

| 方法+路径 | handler（文件:行） | 请求键（粗） | 成功响应键（粗） | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/admin/models | admin.go:638 | 无 | 200 `{items:[ModelSummary]}` | （仅组级） | 中间件 users(R) | replicate |
| POST /api/admin/models | admin.go:664 | body：`name,displayName,capability,provider,constraints,creditCost,channelIds,freeTrialEligible,enabled,sort`（admin.go:651-662） | 201 ModelSummary | 400→VALIDATION_FAILED(fields: name/capability/constraints/creditCost/channelIds) | 中间件 users(R)；model_catalogs(W) | replicate |
| PATCH /api/admin/models/:id | admin.go:729 | 同上（部分更新） | 200 ModelSummary | 400/404 | 中间件 users(R) | replicate |
| DELETE /api/admin/models/:id | admin.go:828 | 路径 id | 204 无 body | 400→VALIDATION_FAILED(fields: id「已产生过流水」)；404 | 中间件 users(R) | replicate |
| GET /api/admin/model-promotions | admin.go:877 | query：`modelId`,`status` | 200 `{items:[promotionPayload]}`（键见 admin.go:1059-1074：id,modelId,name,matchParams,discountBps,priority,version,status,startsAt,endsAt,createdAt,updatedAt） | 400→VALIDATION_FAILED(fields: modelId) | 中间件 users(R) | replicate |
| POST /api/admin/model-promotions | admin.go:915 | body：`modelId,name,matchParams,discountBps,priority,startsAt,endsAt,status,reason`（admin.go:903-913） | 201 promotionPayload | 400→VALIDATION_FAILED(fields: startsAt/endsAt/promotion)；409→PROMOTION_CONFLICT | 中间件 users(R) | replicate |
| PATCH /api/admin/model-promotions/:id | admin.go:972 | 同上（部分更新，status=disabled 停用） | 200 promotionPayload | 400/404/409 同上 | 中间件 users(R) | replicate |

#### 1.6.4 充值档位与订单（5 条）

| 方法+路径 | handler（文件:行） | 请求键（粗） | 成功响应键（粗） | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/admin/credit-packages | admin.go:1090 | 无 | 200 `{items:[packagePayload]}`（admin.go:1104-1116，比用户侧多 enabled/sort） | （仅组级） | 中间件 users(R) | replicate |
| POST /api/admin/credit-packages | admin.go:1128 | body：`id,name,priceMicros,bonusMicros,entitlementDays,enabled,sort`（admin.go:1118-1126） | 201 packagePayload | 400→VALIDATION_FAILED(fields: id/priceMicros) | 中间件 users(R) | replicate |
| PATCH /api/admin/credit-packages/:id | admin.go:1171 | 同上（部分更新） | 200 packagePayload | 400/404 | 中间件 users(R) | replicate |
| GET /api/admin/orders | admin.go:1263 | query：`page,size,sort(createdAt/priceMicros),status(pending/paid/failed/refunded),provider,userId` | 200 `{items:[订单项+userId],total,page,size}`（admin.go:1305-1311） | 400→VALIDATION_FAILED(fields: page/size/sort/status/userId) | 中间件 users(R)；orders(R) | replicate |
| POST /api/admin/requests/refunds/retry | admin_channel.go:214 `RetryRefunds` | 无 body | 200 `{retried:int}` | （仅组级） | 中间件 users(R) | replicate |

#### 1.6.5 渠道管理（4 条）

| 方法+路径 | handler（文件:行） | 请求键（粗） | 成功响应键（粗） | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/admin/channels | admin_channel.go:21 | 无 | 200 `{items:[{id,name,baseUrl,apiFormat,priority,enabled,hasKey,createdAt,updatedAt}]}`（admin_channel.go:35-47） | （仅组级） | 中间件 users(R) | replicate |
| POST /api/admin/channels | admin_channel.go:58 | body：`name,baseUrl,apiFormat,apiKey,priority,enabled`（admin_channel.go:49-56） | 201 channelPayload | 400→VALIDATION_FAILED(fields: apiFormat 等) | 中间件 users(R) | replicate |
| PATCH /api/admin/channels/:id | admin_channel.go:103 | 同上（部分更新，apiKey 空则不改） | 200 channelPayload | 400/404 | 中间件 users(R) | replicate |
| DELETE /api/admin/channels/:id | admin_channel.go:168 | 路径 id | 204 无 body | 400→VALIDATION_FAILED(fields: id「仍被模型引用」)；404 | 中间件 users(R) | replicate |

#### 1.6.6 内容审核（6 条，键为粗粒度）

| 方法+路径 | handler（文件:行） | 请求键（粗） | 成功响应键（粗） | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| GET /api/admin/moderation/records | admin_moderation.go:37 | query：`page,size,sort,stage,decision,reviewStatus,userId,label` | 200 `{items:[moderationRecordPayload],total,page,size}`（键含 id,userId,stage,contentType,contentHash,provider,providerRequestId,policyVersion,decision,riskLabels,reviewStatus,reviewRevision,reviewNote,compensatedMicros,createdAt） | 400→VALIDATION_FAILED(fields: page/size/sort/userId) | 中间件 users(R)；moderation_records(R) | replicate |
| GET /api/admin/moderation/records/:id | admin_moderation.go:90 | 路径 id | 200 记录详情（含隔离原件预览信息，键**待核实** admin_moderation.go:90-123） | 404 | 中间件 users(R) | replicate |
| GET /api/admin/moderation/records/:id/preview | admin_moderation.go:124 | 路径 id | 二进制预览 | 404 | 中间件 users(R) | replicate |
| PATCH /api/admin/moderation/records/:id | admin_moderation.go:150 `ReviewModerationRecord` | body：review 动作相关键（**待核实** admin_moderation.go:150-203） | 200 记录详情 | 400/404/409→MODERATION_ALREADY_REVIEWED | 中间件 users(R) | replicate |
| POST /api/admin/moderation/records/:id/compensate | admin_moderation.go:258 `CompensateModeration` | body 补偿参数（**待核实**） | 200 补偿结果 | 400/404/409 | 中间件 users(R) | replicate |
| GET /api/admin/moderation/stats | admin_moderation.go:318 | 无 | 200 统计对象（service.ModerationService.Stats 返回，键**待核实**） | 500（服务未注入） | 中间件 users(R) | replicate |

#### 1.6.7 站点设置写、旧管理员接口、角色与权限、审计、社区（14 条）

| 方法+路径 | handler（文件:行） | 请求键 | 成功响应键 | 错误 | 身份表读写 | 奇偶 |
| --- | --- | --- | --- | --- | --- | --- |
| PATCH /api/admin/settings | admin_system.go:66 `UpdateSettings` | body：`announcement,registrationEnabled,maintenanceMode,maintenanceNotice,communityEnabled,checkinEnabled,checkinRewardMicros,inviteEnabled,inviteRewardMicros,inviteeRewardMicros,maxUploadBytes,generationConcurrency`（全部可选，至少一项，admin_system.go:50-63） | 200 更新后 Snapshot（键同 GET /admin/settings） | 400→VALIDATION_FAILED(fields: checkinRewardMicros 等/全空) | 中间件 users(R)；site_settings(W)；admin_audit_logs(W) | replicate |
| GET /api/admin/admins | admin_system.go:148 `ListAdmins` | 无 | 200 `{items:[{id,email,username,displayName,status,roleKey,createdAt,lastLoginAt}]}`（admin_system.go:155-168） | （仅组级） | 中间件 users(R)；**users(R) role_key=admin 全行（含 last_login_at）** | replicate |
| POST /api/admin/admins | admin_system.go:176 `AddAdmin` | body：`email`（admin_system.go:171-173） | 200 `{id,role,roleKey}`（role/roleKey 均 "admin"，admin_system.go:218） | 400→VALIDATION_FAILED(fields: email「尚未注册」)；409→VALIDATION_FAILED（AddConflict「已是管理员」，errs.go:93-95）；500 | **users(RW)** role_key/role（AssignRole）；**refresh_tokens(W) 全撤销**（admin_system.go:204-209）；admin_audit_logs(W) | replicate |
| DELETE /api/admin/admins/:id | admin_system.go:222 `RemoveAdmin` | 路径 id | 204 无 body | 400→VALIDATION_FAILED(fields: id「不能撤销自己」「至少保留一个管理员」)；404→NOT_FOUND（非系统角色成员也 404） | **users(W)** 清 role_key；**refresh_tokens(W) 全撤销**（admin_system.go:253-258）；admin_audit_logs(W) | replicate |
| GET /api/admin/roles | admin_roles.go:48 `ListRoles` | 无 | 200 `{items:[rolePayload]}`：`{key,name,description,isSystem,memberCount,permissions,createdAt,updatedAt}`（admin_roles.go:419-430） | （仅组级） | 中间件 users(R)；**users(R) role_key 成员计数（admin_roles.go:59-62）**；role_permissions(R) | replicate |
| POST /api/admin/roles | admin_roles.go:141 `CreateRole` | body：`key,name,description`（admin_roles.go:134-138） | 201 rolePayload（permissions=[]、memberCount=0） | 400→VALIDATION_FAILED(fields: key/name/description) | 中间件 users(R)；roles(W)；admin_audit_logs(W) | replicate |
| PATCH /api/admin/roles/:key | admin_roles.go:183 `UpdateRole` | body：`name`,`description`,`permissions`（全可选，admin_roles.go:175-179） | 200 rolePayload | 400→VALIDATION_FAILED(fields: name/description/permissions/key「系统角色不可编辑权限」)；404 | 中间件 users(R)；roles/role_permissions(W)；admin_audit_logs(W) | replicate |
| DELETE /api/admin/roles/:key | admin_roles.go:283 `DeleteRole` | 路径 key | 204 无 body | 400→VALIDATION_FAILED(fields: key「系统角色不可删除」「仍有成员」)；404 | 中间件 users(R)；**users(R) role_key 成员计数（admin_roles.go:303）**；roles/role_permissions(W) | replicate |
| GET /api/admin/permissions | admin_roles.go:105 `ListPermissions` | 无 | 200 `{items:[{module,moduleName,permissions:[{key,name,description,sort}]}]}`（admin_roles.go:105-132） | （仅组级） | 中间件 users(R)（无库读，纯代码注册表） | replicate |
| GET /api/admin/audit-logs | admin_system.go:277 `ListAuditLogs` | query：`page,size,action,targetType` | 200 `{items:[{id,actorUserId,action,targetType,targetId,requestId,reason,beforeSummary,afterSummary,createdAt}],total,page,size}`（admin_system.go:301-316） | 400→VALIDATION_FAILED(fields: page/size) | 中间件 users(R)；admin_audit_logs(R) | replicate |
| GET /api/admin/community/works | admin_community.go:18 | query：`page,size,status,q,userId` | 200 `{items,total,page,size}`（项键**待核实** admin_community.go:190 起） | 400→VALIDATION_FAILED(fields: page/size/userId) | 中间件 users(R)；community_works/assets/users(R)（GORM 模型） | replicate |
| PATCH /api/admin/community/works/:id | admin_community.go:62 | body：`status` | 200 `{id,status}`（admin_community.go:94） | 400→VALIDATION_FAILED(fields: status)；404 | 中间件 users(R) | replicate |
| GET /api/admin/community/reports | admin_community.go:98 | query：`page,size,status` | 200 `{items,total,page,size}`（项键**待核实**） | 400 | 中间件 users(R) | replicate |
| PATCH /api/admin/community/reports/:id | admin_community.go:150 | body：`status`(handled/dismissed) | 200 `{id,status}`（admin_community.go:187） | 400→VALIDATION_FAILED(fields: status)；404 | 中间件 users(R) | replicate |

### 1.7 范围外补录：受身份中间件影响的其他组（37 条，键不在本清单展开）

这些端点不直接读写身份表，但整条链路经过 `Auth/RequireActiveUser/RequirePasswordChanged/MaintenanceGate/MediaAuth`（即 users/sessions 的读面），T04 冒烟需覆盖「登录态可用」即可；键与状态码在 M1 不动，全部 replicate。

| 组 | 端点 | 身份接触点 |
| --- | --- | --- |
| /api/canvases（main.go:303-311） | GET ""、POST ""、GET/PUT/PATCH/DELETE :id（6） | 中间件 users(R)；业务行 user_id 语义不变 |
| /api/assets（main.go:313-320） | GET ""、POST ""、GET/PATCH/DELETE :id（5） | 同上 |
| /api/generations（main.go:322-327） | GET ""、GET :id、DELETE :id（3） | 同上 |
| /api/community（main.go:330-341） | works 列表/发布/详情/删除/like/unlike/report（8）+ GET users/:id（1，community.go:365 裸列投影 id,username,display_name,avatar_url,created_at——**新发现断裂点 #4**，见第 ③ 节） | 同上 |
| /api/activity（main.go:344-350） | GET/POST checkin、GET invite、POST invite/bind（4；activity.go:169/213 裸列 invite_code 读写——**断裂点 #5**） | 同上 |
| /api/ai（main.go:357-365） | quote、images/generations、videos/generations、videos/tasks/:id、audio/speech、chat/completions（6；ai.go:1214-1227 currentUser 读 users 全行并检查 EmailVerifiedAt） | 同上 |
| /api/settings/public（main.go:353） | GET（1，admin_system.go:26） | 无鉴权 |
| /healthz、/readyz（main.go:238-252） | GET（2） | 无鉴权，仅 DB ping |

---

## ② users / refresh_tokens 引用点全景

grep 范围：`server/` 全部 .go（含测试）。下表按枚举时的标识符记录（`model.User`/`model.RefreshToken`，重命名后对应 `model.PlatformUser`/`model.Session`，映射见顶部说明）；非测试代码按文件归并如下；测试文件仅列表名字符串与关键 fixture。

> 重命名落地后复核：`model.User`/`model.RefreshToken` 全仓（含测试）零残留，36 个文件已引用 `model.PlatformUser`/`model.Session`；`Table("users")` 与 `"refresh_tokens"` 字符串仍在（见第 ③ 节），尚无任何代码以字符串形式引用 `"sessions"`/`"platform_users"` 表名。

### 2.1 非测试代码（生产路径）

| 文件:行 | 引用 | 用途 | 断裂风险 |
| --- | --- | --- | --- |
| handler/auth.go:166,261,304,329,366,374-378,382-413,415,423,436,464,493,505,517,523,544,573,589,603-610 | model.User / model.RefreshToken / model.EmailToken | 注册建号、登录、刷新轮换、登出、改密撤销、媒体令牌版本递增、会话签发 | GORM 模型引用，T01 改模型后自动跟随；**轮换条件更新 revoked_at（343-345）与 token_hash 查询（305）语义必须在 sessions 上等价复刻** |
| handler/account.go:45,137,250,255,276,293-307,327,410 | model.User / model.RefreshToken | GetMe/ExportMe/UpdateMe/ChangePassword/ClaimFreeGrant/RequestDeletion；改密撤销令牌 | GORM 模型；`Updates(map)` 裸列 display_name/avatar_url/password_hash/must_change_password/media_token_version（列名不变则安全） |
| handler/admin.go:120,195,354,420,434-448,492-506,617-618 | model.User / model.RefreshToken | 管理员建号、用户名挑选、用户详情、封禁（撤销令牌+媒体版本递增）、重置密码、统计计数 | GORM 模型；**admin.go:274 裸 SQL 断裂点 #1** |
| handler/admin_roles.go:59,278,303,365,395,568 | model.User / model.RefreshToken | 角色成员计数、AssignUserRole（变更撤销令牌）、防锁死计数 | GORM 模型 |
| handler/admin_system.go:149,183,205,232,254,347 | model.User / model.RefreshToken | ListAdmins、AddAdmin/RemoveAdmin（撤销令牌）、收入统计 | GORM 模型 |
| handler/admin_analytics.go:175-190 | model.User | 用量分析 topUsers 取 email/displayName | GORM 模型 |
| handler/ai.go:1095,1214-1227,1314,1332,1362,1468,1516 | model.User（值传递） | currentUser 读全行、EmailVerifiedAt 预检、扣费与落盘 | GORM 模型 |
| handler/community.go:365-371,437-443 | model.User | 用户主页（裸列 Select）、作品作者信息 | **裸列投影断裂点 #4**（community.go:366-368） |
| handler/activity.go:156,169,212-224 | model.User | 邀请码懒生成、邀请人/被邀请人校验 | **裸列 invite_code 断裂点 #5**（activity.go:169,213） |
| handler/media.go:313 | model.User | 上传前 EmailVerifiedAt 校验 | GORM 模型 |
| handler/order.go:49 | model.User | 下单读全行 | GORM 模型 |
| handler/testutil_test.go:292-299 | model.User | 测试 fixture | 测试内，随模型改造 |
| middleware/middleware.go:170,192,242,309 | model.User | MediaAuth 版本校验、RequireActiveUser、RequirePasswordChanged、MaintenanceGate | GORM 模型 + 裸列 media_token_version/must_change_password/role_key |
| middleware/authz.go:66 | **Table("users")** | LoadAdminAccess 读 role_key | **断裂点 #2** |
| service/deletion.go:41,49-60,73-123,163 | model.User / model.RefreshToken / model.EmailToken | 注销申请/撤销/到期匿名化（含令牌 IP/UA 清空 121-123） | GORM 模型；匿名化直写 users 六列 + sessions 化后痕迹匿名化落点需同步 |
| service/orders.go:42 | model.User（参数） | 下单 | GORM 模型 |
| authz/assign.go:23-26 | model.User + 裸列 role_key/role | 唯一角色写入助手 | 裸列，列名不变则安全 |
| authz/sync.go:154-166 | model.User + 裸列 role/role_key | 启动期角色回填与投影修复 | 裸列，同上 |
| auth/auth.go:56-68 | model.User（参数） | IssueAccessToken 读 ID/Role | GORM 模型 |
| db/db.go:53-55 | model.User / model.RefreshToken / model.EmailToken | AutoMigrate 清单 | **T01 已完成**：清单现为 `&model.PlatformUser{}`/`&model.Session{}`（2026-09-18 并行改动核实） |
| db/db.go:116-147 | model.User | EnsureAdmin | GORM 模型 |
| db/seed_test_data.go:25-33 | model.User | SEED_TEST_DATA 测试账号 | GORM 模型 |

### 2.2 测试代码中的表名字符串与模型引用

| 文件:行 | 引用 | 用途 | 断裂风险 |
| --- | --- | --- | --- |
| handler/testutil_test.go:254-256（字符串在 255） | **"refresh_tokens"** | refreshReadBarrier：并发刷新轮换竞争用例的读屏障注册表名 | **断裂点 #3**（计划文档写 251，实为 255，251 是注释行） |
| handler/auth_test.go:272 | "email_tokens" | verify-email 一次性消费并发用例的读屏障 | M1 不改 email_tokens 表则无需动；若随 identity 域统一改名需同步（**待确认**） |
| handler/admin_meta_test.go:105 | "users" | 模块名断言（authz 目录 key，非表名） | 无风险 |
| 其余 15 个测试文件 | model.User/model.RefreshToken | fixture 与断言（auth_test、testutil_test、admin_users_test、billing_test、media_watermark_test、middleware/auth_test、admin_roles_test、auth/auth_test、site_test、ai_test、db/db_test、service/deletion_test、authz/sync_test、admin_meta_test、service/aitask_watermark_test、service/credit_test、middleware/authz_test） | 随模型改造与 go test 全量回归覆盖 |

---

## ③ 断裂点核对结论

对计划文档记录的三个断裂点逐一核实，另发现两个同类新断裂点。**以下全部 5 处在并行 T01 重命名（model.User→model.PlatformUser、model.RefreshToken→model.Session）落地后的工作区中逐处重新核实过，位置与内容均不变。**

| # | 位置 | 内容 | 核实结论 |
| --- | --- | --- | --- |
| 1 | server/internal/handler/admin.go:274（含 274-316） | ListUsers `Table("users")` + JOIN credits/usage_records + 硬编码列清单（users.id/email/username/role/role_key/status/email_verified_at/created_at，admin.go:310-316）+ planIDExpr（admin.go:244-249，引用 credits 列） | **确认存在**。断裂面比记录的更大：不止表名，还有列名清单与跨表表达式。T03 必须整体改写并保持 planId 派生口径（service.PlanOf） |
| 2 | server/internal/middleware/authz.go:66 | LoadAdminAccess `db.Table("users").Select("role_key")` | **确认存在**。同文件 82-87 的 `Table("roles")`/`role_permissions` 裸表名属 RBAC 域，M1 不动，改写时不要误伤 |
| 3 | server/internal/handler/testutil_test.go:255 | `tableReadBarrier(g, "refresh_tokens", n)`（refreshReadBarrier，254-256；计划写的 251 是其上方注释行） | **确认存在，实际行号 255**。T03 需把屏障表名换成 sessions（并确认 T01 的 sessions 模型表名注册后 Statement.Table 值一致） |
| 4（新发现） | server/internal/handler/community.go:366-368 | UserProfile `Select("id","username","display_name","avatar_url","created_at")` 裸列投影 + `First(&user, "id = ?")`（GORM 模型表名） | 表名走模型不断；**列名是裸字符串**。platform_users 若保持同列名则安全；若改名则 500。同类还有：middleware.go:170/242/309、authz/assign.go:23-26、authz/sync.go:154-166、deletion.go:49-123 的 map 裸列、activity.go:169/213（#5） |
| 5（新发现） | server/internal/handler/activity.go:169,213 | 邀请码懒生成 `Update("invite_code", ...)` 与 `Where("invite_code = ?", ...)` 裸列 | 同上：列名裸字符串，platform_users 保留 invite_code 列即可 |

其余 raw SQL 排查结论（`db.Raw`/`db.Exec` 全量 grep）：

- handler/media.go:683-693：media_files × assets × community_works 联查（社区公开只读放行），**不含身份表**，M1 不受影响。
- service/request.go:39,66,73：ai_requests UPSERT；service/quota.go:123,138,195：usage_records UPSERT——均不含身份表。
- `Table("...")` 全量 grep：除断裂点 #1/#2 外仅 asset.go:97,386（asset_tags，业务域）与 authz.go:82（roles，RBAC 域），均不在本次切换范围。
- 结论：**users/refresh_tokens 的裸 SQL/表名字符串断裂点共 5 处（2 处生产裸 SQL + 1 处测试表名 + 2 处裸列投影），无更多遗漏**；广义「身份列名裸字符串」分布见 2.1，在 platform_users 列名保持不变的前提下均安全。

---

## ④ 提议 omit / defer 的项汇总

**无。** 全部 118 条端点（主范围 81 + 范围外补录 37）均为 replicate：

- M1 是纯值来源切换，未发现任何「属于身份域外且切换后自然消失或无法保留」的键、错误码或状态码。
- 需要留意的三个「看似可 omit、实际必须 replicate」的项：
  1. `userPayload.role` / access token `role` claim（旧 users.role 投影列）：当前无授权消费方（admin 鉴权走 role_key），但 web 端与 JWT 形状依赖该键存在，platform_users 必须保留 role 投影列并继续由 AssignRole 同步。
  2. `sessionPayload.plan`（读 plans 表 "free"）：属 billing 域（M2 才动），M1 期间必须照常返回。
  3. refresh 轮换的并发竞争分支（401 且不触发复用检测，auth.go:351-357）：sessions 表实现必须复刻 RowsAffected==0 语义，否则并发刷新会误撤销新会话——这是行为奇偶而非键奇偶，但属红线。

---

## ⑤ 影响 T03 实现的风险点（实现前必读）

1. **media_token_version 生命周期是跨表断链高危点**：写入 5 处（auth.go:375、auth.go:605、admin.go:441、admin.go:500、account.go:302）+ 签发 1 处（auth.go:92-107 `IssueMediaToken(user.MediaTokenVersion)`）+ 校验 1 处（middleware.go:170）。platform_users 必须保留该列，否则 ic_media cookie 校验全断。
2. **must_change_password 读取面**：中间件每请求读（middleware.go:242）+ 登录/刷新响应（auth.go:428/445）+ 管理员建号/重置写（admin.go:128/492-495）+ 唯一清除点 ChangePassword（account.go:293-297）。platform_users 需保留该列，否则 PASSWORD_CHANGE_REQUIRED 闸门失效。
3. **轮换与复用语义**：token_hash 唯一查询、`revoked_at IS NULL` 条件轮换、复用检测（撤销该用户全部令牌 + media_token_version+1，auth.go:316-321）、并发竞争失败不触发复用（auth.go:351-357）、过期即撤销（auth.go:323-327）。sessions 表需逐条等价复刻，T04 回归点①依赖 auth_test.go 现有用例全绿。
4. **last_login_at 双写点**（Login auth.go:291 与 Refresh auth.go:358）：GetUser/ListAdmins 展示该值；sessions 若自带 last_seen 字段，M1 仍应直写 platform_users.last_login_at 以保持行为（**待确认**：是否由 sessions 派生）。
5. **注销匿名化的会话痕迹**（deletion.go:115-123）：sessions 化后「撤销全部令牌 + IP/UA 置空」两步要落到 sessions 表；users 上的六列覆写（email/username/password_hash/display_name/avatar_url/status）保留在 platform_users。
6. **状态机字符串不变**：`active`/`disabled`/`pending_deletion` 在 middleware.go:197、auth.go:280/336、admin.go:262/411、deletion.go 全链使用；platform_users.status 取值集合不能变。
7. **注册事务三表联动**：auth.go:177-188 一个事务内建 users 行 + credits 行（EnsureCredit）+ email_tokens 行；T01 改表后仍是三表同事务。
8. **READ_ONLY 状态码口径不一致（文档/注释 vs 代码）**：计划文档 M2 节写「ErrReadOnly→402」，quota.go:37 注释也写「映射为 402」，但实际 errs.ErrReadOnly 是 **403**（errs.go:70），且 media_quota_test.go:81 断言 403。M1 按 403 现状 replicate；M2 若改 402 属用户可见行为变更，需在评审门②前单独确认（**待确认**）。
9. **ListUsers 的 planId 派生**：断裂点 #1 改写时不得改变 `planIdExpr` 语义（purchased>0→paid、paid_until 过期 60 天内→sunset），该口径与 service.PlanOf（quota.go:45-64）必须一致。
10. **admin_routes_test.go:16 的 51 条路由计数**：路由表测试会因新增/删除管理路由而 panic/失败，T03 若调整注册方式需保持 spec 机制与计数断言。

## 待核实项汇总

| 项 | 位置 | 说明 |
| --- | --- | --- |
| GET /api/models 响应键 | server/internal/handler/model.go | ModelSummary 键来自 service/catalog.go:259-273，handler 包装形状未逐行核对 |
| GET /api/admin/moderation/records/:id 响应键 | server/internal/handler/admin_moderation.go:90-123 | 详情含预览信息，键未逐字段列出 |
| PATCH moderation review / compensate 请求键 | server/internal/handler/admin_moderation.go:150-317 | 请求体结构未逐字段列出 |
| GET /api/admin/community/works、reports 项键 | server/internal/handler/admin_community.go:190 起 | 粗粒度收录，键未逐字段列出 |
| moderation/stats 响应键 | server/internal/service/moderation.go（Stats 方法） | 未逐字段列出 |
| last_login_at 在 sessions 化后的值来源 | auth.go:291,358 | 见风险点 4，需 T03 设计时拍板 |
| email_tokens 是否随 identity 域改名 | db/db.go:55、handler/auth_test.go:272 | 计划未提及；不改名则 auth_test.go:272 无需动 |
