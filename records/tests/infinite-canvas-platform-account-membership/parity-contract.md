---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T11 契约用例（对照 T02 奇偶清单 records/plans/PLAN-PLATFORM-ACCOUNT-MEMBERSHIP-parity-inventory.md）
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
recorded_at: 2026-09-18
source: 断言值全部来自 2026-09-18 工作区源码，引用 file:line
---

# 契约用例（parity contract）

> 对照基准：T02 奇偶清单。红线：错误 body 形状、业务码字符串、HTTP 状态一律 replicate（清单 §0.1）。
> 统一错误形状：`{error:{code,message,fields?,<extra键?>}}` + 可选 `Retry-After` 头（server/internal/errs/errs.go:98-110）。

## 文档与代码不一致点（编写本文件时核实，2026-09-18）

| # | 文档记录 | 代码现状 | 结论 |
| --- | --- | --- | --- |
| 1 | 计划与清单把 admin.go:274 `Table("users")`、authz.go:66 `db.Table("users")`、testutil barrier `refresh_tokens` 记为「待修断裂点」 | T03 改写已在工作区落地：ListUsers 改用 `model.PlatformUser` + `platform_users` JOIN（admin.go:274-316 区域）；LoadAdminAccess 改走 `idn.GetByID`（middleware/authz.go:57-77）；barrier 已是 `tableReadBarrier(g, "sessions", n)`（testutil_test.go:256，清单写的 255 行号已漂移） | 契约用例按「已切换」现状编写；T04 回归时以本文件为准 |
| 2 | 计划 M2 契约与 quota.go:33 注释写「ErrReadOnly→402」 | `errs.ErrReadOnly = New(403, "READ_ONLY", ...)`（errs/errs.go:70）；media_quota_test.go:81 断言 `http.StatusForbidden`（测试函数名 TestMediaUploadReadOnlyReturns402 有误导性，其 80 行注释明确 READ_ONLY 是 403） | 本文件按 **403** 编写；M2 若改 402 属用户可见行为变更，须评审门②前单独确认 |
| 3 | 清单 §1.4 把 GET /api/models 响应键标「待核实」 | 已核实：`c.JSON(http.StatusOK, gin.H{"items": models})`（handler/model.go:35），项键为 ModelSummary 13 键（service/catalog.go:259-273） | PAR-BILL-005 按核实值编写 |
| 4 | 清单引用的 handler 行号（如 auth.go:351-357、main.go:368-385、db.go:52） | 因 T01/T03 改动有 2-15 行漂移（如 refresh 竞争分支现为 auth.go:355-361，media 组现为 main.go:371-388，Migrate 在 db/db.go:49） | 语义均一致，本文件引用漂移后的现行行号 |

## 1. /api/auth/*（14 条）

#### PAR-AUTH-001 注册成功返回 sessionPayload 并三表同事务建行
- 前置：注册开关开启（site_settings 或环境变量，auth.go:52-57）；邮箱/用户名未被占用。
- 步骤：`POST /api/auth/register`，body `{"email":"a@b.com","username":"abc","password":"password123"}`。
- 期望：201；键 `user`/`accessToken`/`mustChangePassword`/`plan:{id,name}`（auth.go:429-440）；`user` 含 `id,email,username,displayName,avatarUrl,role,emailVerified,mustChangePassword`（auth.go:442-454）；Set-Cookie 下发 `ic_refresh` 与 `ic_media`（auth.go:414-415）；platform_users + credits（EnsureCredit）+ email_tokens 在一个事务写入（auth.go:181-192）。
- 对应任务：T03、T04（回归点①）。
- 执行方式：自动——`TestRegisterVerifyLoginRefreshLogout`（handler/auth_test.go:16）。

#### PAR-AUTH-002 注册参数校验带 fields
- 前置：无。
- 步骤：分别提交非法邮箱、非法用户名（非 3-32 位字母数字下划线连字符）、8-72 位以外密码。
- 期望：400 VALIDATION_FAILED，`error.fields` 分别含 `email`（"邮箱格式不正确"）、`username`（"用户名需为 3-32 位字母、数字、下划线或连字符"）、`password`（"密码长度需在 8-72 位之间"）（auth.go:143-156）。
- 对应任务：T03。
- 执行方式：自动——`TestRegisterVerifyLoginRefreshLogout` 内覆盖注册路径；fields 断言为 T04 手工补充项。

#### PAR-AUTH-003 注册关闭时 503
- 前置：站点设置 registrationEnabled=false（或环境变量关闭）。
- 步骤：`POST /api/auth/register` 合法 body。
- 期望：503 REGISTRATION_DISABLED（auth.go:157-160；errs/errs.go:66）。
- 对应任务：T03。
- 执行方式：自动——`TestSiteSettingsPersistAndGateRegistration`（handler/site_test.go:97）。

#### PAR-AUTH-004 注册冲突 409
- 前置：已存在同邮箱或同用户名账号。
- 步骤：再次注册同邮箱 / 同用户名。
- 期望：409 EMAIL_TAKEN（auth.go:194-196，按唯一索引列名判定）与 409 USERNAME_TAKEN（auth.go:197-200）。
- 对应任务：T03。
- 执行方式：自动——`TestAdminCreateUserRejectsDuplicateEmail` 覆盖唯一冲突判定；auth 侧 409 为 T04 手工补充项。

#### PAR-AUTH-005 登录成功写 last_login_at 并签发会话
- 前置：已注册 active 账号。
- 步骤：`POST /api/auth/login`，body `{"account":"a@b.com","password":"password123"}`（也用 username 试一遍）。
- 期望：200 sessionPayload；platform_users.last_login_at 被更新（auth.go:297）；失败计数清零（auth.go:296）；下发两枚 cookie。
- 对应任务：T03、T04。
- 执行方式：自动——`TestRegisterVerifyLoginRefreshLogout`。

#### PAR-AUTH-006 登录防枚举与账号锁定
- 前置：无（未知账号也适用）。
- 步骤：a) 错误密码登录 5 次；b) 第 6 次登录；c) 用不存在的账号登录。
- 期望：a) 每次失败均 401 INVALID_CREDENTIALS 并计数（auth.go:290-293）；b) 429 RATE_LIMITED + `Retry-After` 头（auth.go:261-264，锁 15 分钟，errs.go:99-101）；c) 未知账号与密码错误同为 401 INVALID_CREDENTIALS（auth.go:272-279，200ms 延迟防时序探测）；disabled 账号登录 403 ACCOUNT_DISABLED（auth.go:286-289）。
- 对应任务：T03、T04（回归点①）。
- 执行方式：自动——`TestLoginLocksAfterFiveFailures`、`TestLoginSuccessResetsFailureCount`（auth_test.go:202、225）。

#### PAR-AUTH-007 刷新轮换与复用检测
- 前置：已登录持有 ic_refresh。
- 步骤：a) `POST /api/auth/refresh` 正常刷新；b) 用已轮换的旧 cookie 再刷；c) 无 cookie 刷新。
- 期望：a) 200 sessionPayload + 新 cookie 轮换（sessions 行旧令牌 `revoked_at` 置位、新行插入，auth.go:347-361、identity/service.go:95-100）；b) 401 UNAUTHORIZED，且复用检测撤销该用户全部会话并递增 media_token_version（auth.go:322-328 → revokeAllUserTokens auth.go:376-385）；c) 401 并清 cookie（auth.go:303-308）；过期令牌 401 且补撤销（auth.go:329-334）；disabled 用户刷新 403 ACCOUNT_DISABLED（auth.go:342-346）。
- 对应任务：T03、T04（回归点①）。
- 执行方式：自动——`TestRefreshReuseRevokesAllTokens`（auth_test.go:91）、`TestSessionCookiesIssuedRotatedAndCleared`（media_test.go:382）。

#### PAR-AUTH-008 并发轮换竞争失败方不触发复用检测
- 前置：两个并发请求持同一 ic_refresh（测试用读屏障对齐时序）。
- 步骤：并发发起两个 `POST /api/auth/refresh`。
- 期望：恰好一个 200；另一个 401 UNAUTHORIZED 且**不**撤销胜者新会话、**不**递增媒体令牌版本（auth.go:355-361；identity/service.go:95-100 条件更新 `revoked_at IS NULL` 保证 RowsAffected 语义）。
- 对应任务：T03（红线：行为奇偶而非键奇偶，清单 §④-3）。
- 执行方式：自动——`TestConcurrentRefreshOnlyOneSucceeds`（auth_test.go:137，barrier 表名已切 sessions，testutil_test.go:256）。

#### PAR-AUTH-009 登出恒 204
- 前置：有或无 ic_refresh 均可。
- 步骤：`POST /api/auth/logout`。
- 期望：恒 204 无 body（auth.go:367-374）；有 cookie 时 sessions 行按 token_hash 置 revoked_at（identity/service.go:88-91）；两枚 cookie 均被清除（MaxAge=-1，auth.go:82-92、113-123）。
- 对应任务：T03。
- 执行方式：自动——`TestRegisterVerifyLoginRefreshLogout`。

#### PAR-AUTH-010 重发验证邮件限流
- 前置：已登录未验证账号。
- 步骤：`POST /api/auth/verify-email/send` 连发 4 次。
- 期望：前 3 次 204；第 4 次 429 RATE_LIMITED + Retry-After（同邮箱 3 次/小时，auth.go:66、479-483）；已验证账号直接 204（auth.go:475-478）。
- 对应任务：T03。
- 执行方式：自动——`TestMailRateLimitByEmail`（auth_test.go:383）。

#### PAR-AUTH-011 邮箱验证一次性消费
- 前置：持有未使用验证令牌。
- 步骤：a) `POST /api/auth/verify-email` body `{"token":<明文>}`；b) 同 token 重放；c) 并发同 token 两请求；d) 缺 token / 未知 token / 过期 token。
- 期望：a) 200 `{user:userPayload}` 且 `emailVerified:true`，**不**签发 accessToken（auth.go:523-531）；b) 410 TOKEN_INVALID（auth.go:504-506）；c) 恰好一个成功（条件更新 `used_at IS NULL`，auth.go:509-521）；d) 均 410 TOKEN_INVALID（auth.go:492-507）。
- 对应任务：T03。
- 执行方式：自动——`TestConcurrentEmailVerifyOnlyOneSucceeds`（auth_test.go:260）、`TestEmailTokenTTLByPurpose`（auth_test.go:313，验证邮件 24h / 重置 1h）。

#### PAR-AUTH-012 忘记密码防枚举
- 前置：无。
- 步骤：`POST /api/auth/password/forgot`，分别用存在与不存在的邮箱；同一邮箱连发 11 次。
- 期望：无论邮箱是否存在均 204（auth.go:550-560）；超 10 次/小时（main.go:262 mailLimiter）429 + Retry-After（auth.go:545-548）。
- 对应任务：T03。
- 执行方式：自动——`TestMailRateLimitByEmail`、`TestMailRateLimitByIP`（auth_test.go:420）。

#### PAR-AUTH-013 重置密码联动安全事件
- 前置：持有 reset_password 令牌（1 小时有效）。
- 步骤：a) `POST /api/auth/password/reset` body `{"token":<明文>,"password":"newpass123"}`；b) 重放；c) 密码长度非法。
- 期望：a) 204；platform_users.password_hash 更新、media_token_version 递增、该用户全部 sessions 撤销，三步同事务（auth.go:607-618）；b) 410 TOKEN_INVALID（used_at 条件更新，auth.go:594-606）；c) 400 VALIDATION_FAILED fields.password（auth.go:574-576）；令牌缺失/未知/过期 410（auth.go:568-587）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestEmailTokenTTLByPurpose`（TTL 部分）；完整链路为 T04 手工补充项。

#### PAR-AUTH-014 会话 cookie 属性恒定
- 前置：任一登录/刷新/登出场景。
- 步骤：检查 Set-Cookie 响应头。
- 期望：`ic_refresh` Path=/api/auth（auth.go:27-28）、`ic_media` Path=/api/media（auth/auth.go:30-31）；两枚均 Host-only（无 Domain 属性）+ HttpOnly + SameSite=Lax + Secure=cfg.CookieSecure（auth.go:70-80、96-111）；M3 若将 Path 扩为 /api，本用例的 Path 断言值随 T09 设计评审更新（见 oidc-security.md OIDC-009）。
- 对应任务：T03、T09。
- 执行方式：自动——`TestSessionCookiesIssuedRotatedAndCleared`（media_test.go:382）。

## 2. /api/me/*（13 条）

#### PAR-ME-001 GET /api/me 键集完整
- 前置：已登录 active 用户。
- 步骤：`GET /api/me`。
- 期望：200，顶层键 `user,plan,credits,usage,mediaExpiry,deletion,readOnly,graceEndsAt`（account.go:79-115）；`user` 8 键（id,email,username,displayName,avatarUrl,role,emailVerified,createdAt）；`plan` 5 键（id,name,storageBytes,maxFileBytes,retentionDays）；`credits` 4 键（purchasedMicros,grantedMicros,totalMicros,paidUntil）；`usage` 3 键（storageBytes,freeImageTrialsUsed,freeVideoTrialsUsed）；`mediaExpiry` 2 键（nearestAt,expiringCount）；时间值 RFC3339Nano（account.go:127-132）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestMeIncludesCreditsPlanAndDeletion`（handler/billing_test.go:129）。

#### PAR-ME-002 GET /api/me 的 deletion 投影
- 前置：a) active 用户；b) pending_deletion 用户。
- 步骤：分别 GET /api/me。
- 期望：a) `deletion:{status:"none",scheduledAt:null}`；b) `deletion:{status:"pending",scheduledAt:<RFC3339Nano>}`（account.go:74-77）。
- 对应任务：T03、T04（回归点⑤）。
- 执行方式：自动——`TestAccountDeletionLifecycle`（billing_test.go:156）。

#### PAR-ME-003 GET /api/me 未登录 401
- 前置：无 Authorization 头。
- 步骤：`GET /api/me`。
- 期望：401 UNAUTHORIZED（middleware/middleware.go:118-130）。
- 对应任务：T03。
- 执行方式：自动——`TestAuthMissingToken`（middleware/auth_test.go:55）。

#### PAR-ME-004 PATCH /api/me 更新资料
- 前置：已登录。
- 步骤：a) body `{"displayName":"新昵称"}`；b) body `{}`；c) body 非法 JSON。
- 期望：a) 200 `{user:userPayload}`，display_name 落库（account.go:240-258）；b/c) 400 VALIDATION_FAILED（至少一项，account.go:247-250）。
- 对应任务：T03。
- 执行方式：自动——T04 手工补充项（现有测试未单列 PATCH /me）。

#### PAR-ME-005 改密成功联动会话与媒体令牌
- 前置：已登录且知道旧密码。
- 步骤：`POST /api/me/password` body `{"oldPassword":..., "newPassword":...}`。
- 期望：204 + 响应头 `X-Session-Refreshed: true`（account.go:314-316）；同一事务内 password_hash 与 must_change_password=false 更新、media_token_version 递增、全部 sessions 撤销（account.go:292-311）；重签新 refresh cookie；该接口是 must_change_password 唯一清除点。
- 对应任务：T03、T04。
- 执行方式：自动——`TestForcedPasswordChangeGate`（admin_users_test.go:207，覆盖置位→放行改密→清除标志链路）。

#### PAR-ME-006 改密错误分支
- 前置：已登录。
- 步骤：a) 旧密码错误；b) newPassword 少于 8 位。
- 期望：a) 401 INVALID_CREDENTIALS（account.go:282-285）；b) 400 VALIDATION_FAILED fields.newPassword（account.go:272-275）。
- 对应任务：T03。
- 执行方式：T04 手工补充项（现有测试未单列）。

#### PAR-ME-007 免费新人礼领取幂等
- 前置：已验证邮箱、FreeGrantEnabled=true。
- 步骤：`POST /api/me/free-grant/claim` 调两次；并发再调 N 次。
- 期望：两次均 200 且同形状 `{campaignId,status,imageTrials,videoTrials,grantedMicros}`（account.go:441-449，imageTrials=3、videoTrials=1、grantedMicros=0）；并发靠 free_grant_claims (user_id, campaign_id) 唯一约束兜底返回同一结论（account.go:371-377；model/model.go:62-63）。
- 对应任务：T03、T06（M2 归 billing 域后行为不变）。
- 执行方式：自动——`TestFreeGrantClaimIsIdempotent`、`TestFreeGrantClaimConcurrentUniqueConstraint`（account_test.go:32、137）。

#### PAR-ME-008 免费新人礼拒绝分支
- 前置：a) 未验证邮箱；b) 风控评分超阈值；c) 当日预算耗尽；d) 功能开关关闭。
- 步骤：分别调用 claim。
- 期望：均 403 FREE_GRANT_UNAVAILABLE（b/c/d，account.go:352-361）或 403 EMAIL_NOT_VERIFIED（a，account.go:330-333）；拒绝记录写 denied 行（account.go:385-393）。
- 对应任务：T03。
- 执行方式：自动——`TestFreeGrantRiskThresholdDenies`、`TestFreeGrantDailyBudgetDenies`（account_test.go:73、105）。

#### PAR-ME-009 申请注销
- 前置：已登录 active 用户。
- 步骤：a) `POST /api/me/deletion` body `{"password":<正确>}`；b) 再调一次。
- 期望：a) 200 `{"scheduledAt":<RFC3339Nano>}`，status 置 pending_deletion、deletion_scheduled_at=now+7 天（account.go:401-424；identity/service.go:141-155）；b) 幂等返回同一 scheduledAt，**不重置倒计时**（identity/service.go:146-148）。
- 对应任务：T03、T04（回归点⑤）。
- 执行方式：自动——`TestAccountDeletionLifecycle`。

#### PAR-ME-010 申请注销密码错误
- 前置：已登录。
- 步骤：`POST /api/me/deletion` 传错误密码。
- 期望：401 INVALID_CREDENTIALS（account.go:413-415）。
- 对应任务：T03。
- 执行方式：T04 手工补充项。

#### PAR-ME-011 撤销注销
- 前置：a) pending_deletion 用户；b) active 用户。
- 步骤：`POST /api/me/deletion/cancel`。
- 期望：a) 200 `{"status":"active"}`，status 回 active、deletion_scheduled_at 清空（account.go:427-439；identity/service.go:158-169）；b) 409 DELETION_NOT_PENDING（RowsAffected=0 → ErrNotPendingDeletion）。
- 对应任务：T03、T04（回归点⑤）。
- 执行方式：自动——`TestPendingDeletionCanLoginRefreshAndCancel`（billing_test.go:196）。

#### PAR-ME-012 个人数据导出
- 前置：已登录（pending_deletion 亦可）。
- 步骤：`GET /api/me/export`。
- 期望：200；响应头 `Content-Disposition: attachment; filename="youc-export.json"`（account.go:206）；顶层键 `exportedAt,profile,credits,orders,generations,canvases`（account.go:207-225）；orders 项 10 键、generations 项 8 键、canvases 项 6 键（account.go:167-205）；不含媒体二进制与画布正文。
- 对应任务：T03、T04（回归点⑤：冷静期可导出）。
- 执行方式：自动——`TestPendingDeletionCanLoginRefreshAndCancel`；键集为 T04 手工核对项。

#### PAR-ME-013 注销冷静期的中间件口径
- 前置：pending_deletion 用户已登录。
- 步骤：a) 登录；b) 刷新；c) `POST /api/ai/images/generations`；d) `POST /api/orders`；e) `GET /api/canvases`。
- 期望：a/b) 放行（Login auth.go:284-289 与 Refresh auth.go:341-346 只拦 disabled）；c/d) 403 ACCOUNT_PENDING_DELETION（middleware/middleware.go:211-220；路由挂载 main.go:360、403）；e) 放行（读操作不拦）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestPendingDeletionCanLoginRefreshAndCancel`。

## 3. 点数/档位/模型目录（7 条）

#### PAR-BILL-001 GET /api/credits 键集
- 前置：已登录（credits 行可不存在，按零值处理）。
- 步骤：`GET /api/credits`。
- 期望：200 `{purchasedMicros,grantedMicros,totalMicros,paidUntil,recent}`（credit.go:47-53）；recent 为最近 5 条流水，项键 `id,bucket,type,amountMicros,balanceAfterMicros,createdAt` + 可选 `refType,refId,note`（credit.go:83-106）。
- 对应任务：T03、T06（切换 billing 域后形状不变仅可加字段）。
- 执行方式：自动——`TestCreditsAndPackagesRequireAuth`（billing_test.go:96）覆盖鉴权；键集为 T04 手工核对项。

#### PAR-BILL-002 GET /api/credits/transactions 游标分页
- 前置：已登录且有流水。
- 步骤：a) `GET /api/credits/transactions?size=1`；b) size=0 / size=101；c) 非法 cursor；d) type=consume。
- 期望：a) 200 `{items,nextCursor}`，nextCursor 无下一页为 null（credit.go:76-80）；b) 400 VALIDATION_FAILED fields.size（1-100，credit.go:154-163）；c) 400 VALIDATION_FAILED fields.cursor；d) 仅返回 type=consume。
- 对应任务：T03。
- 执行方式：自动——T04 手工补充项。

#### PAR-BILL-003 GET /api/credit-packages 键集
- 前置：已登录；存在已上架档位。
- 步骤：`GET /api/credit-packages`。
- 期望：200 `{items,providers}`；项 7 键 `id,name,priceMicros,purchasedMicros,bonusMicros,entitlementDays,currency`（credit.go:117-129）；仅含 enabled=true 按 sort,id 升序（credit.go:112）；providers 为已配置渠道名数组。
- 对应任务：T03。
- 执行方式：自动——`TestCreditsAndPackagesRequireAuth`。

#### PAR-BILL-004 GET /api/plans 三档
- 前置：已登录；plans seed 存在。
- 步骤：`GET /api/plans`。
- 期望：200 `{items:[...]}`，项 5 键 `id,name,storageBytes,maxFileBytes,retentionDays`（credit.go:140-150）；seed 含 free/paid/sunset（db/db.go:84-101）。
- 对应任务：T03；M2 plans 表退役后本用例改核对 membership_plans 派生（PAR-M2-022）。
- 执行方式：自动——T04 手工核对项 + 真实 MySQL 启动检查 seed。

#### PAR-BILL-005 GET /api/models 键集（清单「待核实」项已核实）
- 前置：已登录。
- 步骤：`GET /api/models`。
- 期望：200 `{items:[ModelSummary]}`（handler/model.go:35）；项 13 键 `id,name,displayName,capability,provider,constraints,creditCost,channelIds,freeTrialEligible,enabled,sort,createdAt,updatedAt`（service/catalog.go:259-273）。
- 对应任务：T03。
- 执行方式：自动——T04 手工核对项。

#### PAR-BILL-006 billing 组未带凭证 401
- 前置：无 Authorization 头。
- 步骤：`GET /api/credits`（同组其余 4 端点同口径）。
- 期望：401 UNAUTHORIZED（main.go:391-398 组级 Auth；middleware.go:103-116）。
- 对应任务：T03。
- 执行方式：自动——`TestCreditsAndPackagesRequireAuth`。

#### PAR-BILL-007 过期 access token 401 TOKEN_EXPIRED
- 前置：持有过期 JWT（AccessTokenTTL=15 分钟，auth/auth.go:14）。
- 步骤：带过期 token 访问任一业务端点。
- 期望：401 TOKEN_EXPIRED（middleware.go:109-114；errs/errs.go:52）。
- 对应任务：T03、T10（OIDC token 15min 同口径参照）。
- 执行方式：自动——`TestAuthExpiredToken`（middleware/auth_test.go:79）。

## 4. /api/orders 与支付回调（7 条）

#### PAR-ORD-001 下单成功
- 前置：已登录 active、非冷静期；存在 enabled 套餐且价格为整分。
- 步骤：`POST /api/orders` body `{"packageId":<id>,"provider":"alipay"}`。
- 期望：201 `{order:订单项, payment:{type,payload}}`（order.go:69-72）；订单行快照价格/双桶/权益天数（orders.go:65-76）；providerOrderId 有值时写入订单行（orders.go:88-94）。
- 对应任务：T03、T04（回归点②）、T06。
- 执行方式：自动——`TestCreateOrderSnapshotsPackage`（service/orders_test.go:62）。

#### PAR-ORD-002 下单错误分支
- 前置：已登录。
- 步骤：a) provider 未注册；b) packageId 不存在或已下架；c) 冷静期账号下单；d) 同用户 1 小时内第 11 次下单。
- 期望：a) 400 VALIDATION_FAILED fields.provider（order.go:45-48）；b) 400 VALIDATION_FAILED fields.packageId（order.go:59-60）；c) 403 ACCOUNT_PENDING_DELETION（order.go:57-58；orders.go:43-45）；d) 429 RATE_LIMITED（10 次/小时按用户，main.go:263、403-405）。
- 对应任务：T03、T04。
- 执行方式：自动——b/c 见 `TestCreateOrderSnapshotsPackage` 与 `TestPendingDeletionCanLoginRefreshAndCancel`；d 为 T04 手工补充项。

#### PAR-ORD-003 订单列表游标与状态过滤
- 前置：已登录且有多笔订单。
- 步骤：a) `GET /api/orders?size=1&status=pending`；b) status=invalid；c) size 越界。
- 期望：a) 200 `{items,nextCursor}`（order.go:95-103）；b) 400 VALIDATION_FAILED fields.cursor（orders.go:105-108 仅接受 pending/paid/failed/refunded）；c) 400 fields.size（order.go:80-84）。
- 对应任务：T03。
- 执行方式：自动——T04 手工补充项。

#### PAR-ORD-004 订单详情与跨用户隔离
- 前置：用户 A 有一笔订单。
- 步骤：a) A `GET /api/orders/:id`；b) B 访问同一 id；c) 非法 uuid。
- 期望：a) 200 `{order:订单项}`（order.go:126）；b) 404 NOT_FOUND（防探测，orders.go:164-175）；c) 404（order.go:111-115）。
- 对应任务：T03。
- 执行方式：自动——T04 手工补充项。

#### PAR-ORD-005 取消订单
- 前置：a) 一笔 pending 订单；b) 一笔 paid 订单。
- 步骤：`POST /api/orders/:id/cancel`。
- 期望：a) 200 `{order}` 且 status=failed（orders.go:140-146 条件更新 pending→failed）；b) 409 ORDER_ALREADY_PAID（order.go:142-143）；不存在 404（order.go:144-145）。
- 对应任务：T03。
- 执行方式：自动——`TestCancelOrder`（service/orders_test.go:149）。

#### PAR-ORD-006 支付回调响应契约
- 前置：支付渠道已注册。
- 步骤：a) 未知渠道回调；b) 验签失败；c) 未知订单号；d) alipay/easypay 成功；e) wechat 成功。
- 期望：a) 404 NOT_FOUND（order.go:188-191）；b) 400 INVALID_SIGNATURE（order.go:193-201）；c) 404（orders.go:182-190，非成功响应让渠道重试）；d) 200 裸文本 `success`（order.go:219-221）；e) 200 `{"code":"SUCCESS","message":"成功"}`（order.go:222-223）——**不套统一错误形状**。
- 对应任务：T03、T04（回归点②）。
- 执行方式：自动——`TestCallbackIdempotentAndAmountMismatch`（orders_test.go:92）覆盖金额分支；响应体格式为 T04 手工核对项。

#### PAR-ORD-007 订单项键集（orderPayload）
- 前置：任一订单接口返回。
- 步骤：核对任一订单响应。
- 期望：固定 12 键 `id,provider,packageId,priceMicros,currency,purchasedMicros,grantedMicros,entitlementDays,status,paidAt,createdAt,updatedAt` + 可选 `providerOrderId`（order.go:155-174）。
- 对应任务：T03、T06（M2 后仅可加字段）。
- 执行方式：自动——`TestCreateOrderSnapshotsPackage`。

## 5. /api/media/* 与 /api/media-download（10 条）

#### PAR-MED-001 HEAD 响应头契约
- 前置：已上传文件且登录态可用（Bearer 或 ic_media cookie）。
- 步骤：`HEAD /api/media/:storageKey`。
- 期望：200，头 `Content-Type,Content-Length,ETag,Cache-Control,X-Content-Type-Options,X-Checksum`；存在干净原件（orig）时省略 `X-Checksum`（media.go:65 起，inventory §1.3）；非法 storageKey 400 VALIDATION_FAILED；不存在 404。
- 对应任务：T03。
- 执行方式：自动——`TestMediaLocalUploadHeadGetDelete`（media_test.go:42）、`TestMediaDeliveryCacheHeaderGrading`（media_watermark_test.go:130）。

#### PAR-MED-002 GET 三种交付形态
- 前置：同上。
- 步骤：a) local 存储直读；b) 带 If-None-Match 命中 ETag；c) S3 存储。
- 期望：a) 200 二进制；b) 304 无 body（media_watermark_test.go:236 覆盖）；c) 302 Location 预签名（media.go:179 起）。
- 对应任务：T03。
- 执行方式：自动——`TestMediaLocalUploadHeadGetDelete`、`TestMediaDelivery304SkipsBodyRead`、`TestMediaS3RedirectAndCacheHeaders`（media_test.go:339）。

#### PAR-MED-003 PUT 成功键集
- 前置：已验证邮箱的登录用户；Content-Type 在白名单。
- 步骤：`PUT /api/media/image:X` body PNG 字节。
- 期望：201 `{storageKey,bytes,checksum,mimeType}`（media.go:468-473）；media_files 行与 usage_records 计数同事务更新。
- 对应任务：T03、T04（回归点④）。
- 执行方式：自动——`TestMediaLocalUploadHeadGetDelete`。

#### PAR-MED-004 PUT 未验证邮箱 403
- 前置：邮箱未验证。
- 步骤：`PUT /api/media/image:X`。
- 期望：403 EMAIL_NOT_VERIFIED（media.go:317-320）；只拦上传写路径，GET/HEAD 不受影响。
- 对应任务：T03。
- 执行方式：自动——`TestMediaEmailNotVerifiedBlocksUploadOnly`（media_test.go:160）。

#### PAR-MED-005 PUT 配额三态错误体
- 前置：档位上限已知（free 默认 50MiB，db/db.go:86）。
- 步骤：a) used>limit 时上传；b) used+incoming>limit 时上传；c) 单文件超 maxFileBytes。
- 期望：a) **403** READ_ONLY + extra `{planId,used,limit,message}`（quota.go:224-232、235-251；errs.go:70）——计划写 402 与代码不符，见文件头不一致点 #2；b) 507 STORAGE_QUOTA_EXCEEDED + extra `{used,limit}`；c) 413 FILE_TOO_LARGE（media.go:335-338）。
- 对应任务：T03、T04（回归点④）、T06（M2 body 逐字节一致）。
- 执行方式：自动——`TestMediaUploadReadOnlyReturns402`（media_quota_test.go:64，断言 403）、`TestMediaUploadQuotaCountsAndRejects`（media_quota_test.go:14，断言 507 与 used/limit 值）、`TestMediaFileTooLargeBoundary`（media_test.go:185）。

#### PAR-MED-006 PUT 内容侧错误
- 前置：已登录已验证。
- 步骤：a) 声明 image/* 但 body 非 PNG 魔数；b) 带 X-Checksum 与实际不符；c) 审核服务不可用。
- 期望：a) 400 VALIDATION_FAILED（嗅探拒绝）；b) 409 CHECKSUM_MISMATCH 且不留任何落盘与计数（media_test.go:223）；c) 503 MODERATION_UNAVAILABLE（422 CONTENT_REJECTED 为拒载分支）。
- 对应任务：T03。
- 执行方式：自动——`TestMediaChecksumMismatchLeavesNothing`、`TestModerationRejectsUploadWithoutQuota`（moderation_test.go:110）。

#### PAR-MED-007 DELETE 幂等
- 前置：已登录。
- 步骤：a) 删除存在文件；b) 再删一次；c) 删不存在 key。
- 期望：a/b/c 均 204（不存在也 204，media.go:476-490）；删除后 usage_records 计数回退（media_quota_test.go:54-61）。
- 对应任务：T03、T04（回归点④）。
- 执行方式：自动——`TestMediaLocalUploadHeadGetDelete`、`TestMediaUploadQuotaCountsAndRejects`。

#### PAR-MED-008 申请下载两态响应
- 前置：a) 非付费或无干净原件；b) 付费用户有 orig。
- 步骤：`POST /api/media/:storageKey/download`。
- 期望：a) 200 `{url:<原媒体路径>,expiresAt:null}`；b) 200 `{url:"/api/media-download/<token>",expiresAt:<时间>}`（media_download.go:128-178）；归属不符 404 防探测；60 次/小时超限 429（main.go:267）。
- 对应任务：T03。
- 执行方式：自动——`TestDownloadRequestTwoStateResponse`、`TestDownloadRequestStrictOwnership`（media_download_test.go:72、49）。

#### PAR-MED-009 取件令牌防探测
- 前置：已签发取件链接。
- 步骤：a) 正常取件；b) 篡改 token；c) 过期 token；d) 换用户取件；e) 行已删。
- 期望：a) 200 二进制 + `Content-Disposition: attachment`；b/c/d/e) 一律 404 NOT_FOUND（media_download.go:183 起，验签/过期/跨用户/行删除同码）。
- 对应任务：T03。
- 执行方式：自动——`TestDownloadTokenValidationChain`（media_download_test.go:145）。

#### PAR-MED-010 MediaAuth 双凭据边界
- 前置：已登录用户持 ic_media。
- 步骤：a) GET 带 cookie；b) PUT 只带 cookie；c) PUT 带 Bearer；d) cookie 版本落后（改密后）；e) 用 ic_media 的 JWT 当业务接口 Bearer。
- 期望：a) 200；b) 401 UNAUTHORIZED（写方法不认 cookie，middleware.go:155-158）；c) 200（Bearer 优先）；d) 401（media_token_version 校验，middleware.go:169-179）；e) 401（scope=media 拒绝，auth/auth.go:89-92）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestMediaReadAuthBearerAndCookie`（media_test.go:265）、`TestMediaTokenScopeBoundary`（auth/auth_test.go:12）、`TestMediaPutOverwriteAndDeleteRemoveOrig`（media_watermark_test.go:268）。

## 6. /api/admin/* 身份关键端点（13 条）

> 组级中间件链 `Auth → RequireActiveUser → RequirePasswordChanged → LoadAdminAccess`（main.go:416）；无后台角色 403 FORBIDDEN（middleware/authz.go:70-77）。路由总数 51 由 `TestAdminRoutesAllCarryRegisteredPermission`（cmd/server/admin_routes_test.go:16、24-32）锁定。

#### PAR-ADM-001 GET /api/admin/me
- 前置：持后台角色的管理员登录。
- 步骤：`GET /api/admin/me`。
- 期望：200 `{role:{key,name,isSystem},permissions:[string]}`（清单 §1.6.1，admin_roles.go:31 起）；普通用户 403 FORBIDDEN。
- 对应任务：T03、T07。
- 执行方式：自动——`TestAdminMeReturnsRoleAndPermissions`、`TestPermissionDeniedForUserWithoutRole`（admin_roles_test.go:72、103）。

#### PAR-ADM-002 GET /api/admin/meta 形状不变
- 前置：管理员登录。
- 步骤：`GET /api/admin/meta`。
- 期望：200 `{product:{id,name,version},modules:[...]}`；T07/T09 仅可在 modules 加法扩展，既有键不减不改（红线：/admin/meta 形状不变）。
- 对应任务：T07、T09。
- 执行方式：自动——`TestAdminMetaFiltersModulesByPermission`、`TestAdminMetaRejectsNonAdmin`（admin_meta_test.go:33、115）。

#### PAR-ADM-003 GET /api/admin/users 列表键与 planId 派生口径
- 前置：管理员登录；用户余额三种形态（purchased>0 / 权益未过期 / 过期 60 天内 / 其余）。
- 步骤：`GET /api/admin/users?page=1&size=20`；带 `planId=sunset`、`status=disabled`、`q=` 过滤。
- 期望：200 `{items,total,page,size}`；项 13 键 `id,email,username,role,roleKey,status,emailVerified,planId,purchasedMicros,grantedMicros,paidUntil,storageBytes,createdAt`（admin.go:327-346 区域，inventory §1.6.2）；planId 派生与 `service.PlanOf` 同口径：purchased>0→paid、paid_until 过期 60 天内→sunset、其余 free（admin.go:254-258 planIDExpr；quota.go:45-59）；非法 sort/status/planId 400 VALIDATION_FAILED。
- 对应任务：T03（断裂点 #1 已改写为 model 查询）、T04。
- 执行方式：自动——`TestAdminUserListAndCreditAdjust`（billing_test.go:270）、`TestPlanOfDerivation`（service/credit_test.go:182）。

#### PAR-ADM-004 POST /api/admin/users 一次性临时密码
- 前置：管理员登录。
- 步骤：`POST /api/admin/users` body `{"email":...,"displayName":...,"roleKey":"user"}`。
- 期望：201 `{user:{...},temporaryPassword}`，明文只出现这一次（admin.go:173-187 区域）；建号 must_change_password=true、email_verified_at=now；409 EMAIL_TAKEN/USERNAME_TAKEN；未知 roleKey 400。
- 对应任务：T03、T07。
- 执行方式：自动——`TestAdminCreateUserReturnsOneTimeTemporaryPassword`、`TestAdminCreateUserRejectsDuplicateEmail`、`TestAdminCreateUserRejectsUnknownRole`（admin_users_test.go:65、139、159）。

#### PAR-ADM-005 GET /api/admin/users/:id
- 前置：管理员登录。
- 步骤：`GET /api/admin/users/:id`。
- 期望：200 `{user:{...}}` 含 20 键（id,email,username,role,roleKey,status,emailVerified,createdAt,lastLoginAt,planId,planName,storageLimit,maxFileBytes,retentionDays,purchasedMicros,grantedMicros,paidUntil,storageBytes,mediaCount,readOnly）；lastLoginAt 来自 platform_users.last_login_at（Login/Refresh 双写点 auth.go:297、362）；不存在 404。
- 对应任务：T03。
- 执行方式：自动——`TestAdminUserListAndCreditAdjust`；键集为 T04 手工核对项。

#### PAR-ADM-006 PATCH /api/admin/users/:id 封禁联动
- 前置：管理员登录。
- 步骤：a) status=disabled；b) status=active 恢复；c) status=pending_deletion。
- 期望：a) 200 `{id,status}`；同一事务置状态 + media_token_version 递增 + 全部 sessions 撤销（admin.go:427-448）；封禁后旧 ic_media 立即失效、15 分钟内 access token 被每请求状态检查拦截（middleware.go:190-209）；b) 恢复不撤销会话；c) 400 VALIDATION_FAILED fields.status（只接受 active/disabled，admin.go:411-413）。
- 对应任务：T03、T04（回归点①封禁链路）。
- 执行方式：自动——`TestAdminUserListAndCreditAdjust`；联动细节 T04 手工补充。

#### PAR-ADM-007 封禁最后一个系统角色成员被拦截
- 前置：系统角色仅剩一个 active 用户。
- 步骤：PATCH 该用户 status=disabled。
- 期望：400 VALIDATION_FAILED，fields.status 含「系统角色必须保留至少一个 active 用户」（admin.go:430-437、455-460）。
- 对应任务：T03。
- 执行方式：自动——`TestLockoutGuards`（admin_roles_test.go:232）。

#### PAR-ADM-008 POST /api/admin/users/:id/password
- 前置：管理员登录。
- 步骤：a) 管理员重置他人密码；b) 管理员重置自己密码；c) 密码长度非法。
- 期望：a) 204；password_hash 更新、must_change_password=true、media_token_version 递增、全部 sessions 撤销（admin.go:490-507）；b) must_change_password 保持 false（不把自己锁进改密流程，admin.go:487-488）；c) 400 VALIDATION_FAILED fields.password（8-72）。
- 对应任务：T03。
- 执行方式：自动——`TestAdminResetPasswordSetsMustChangeAndRevokesTokens`、`TestAdminResetOwnPasswordKeepsUnforced`（admin_users_test.go:326、369）。

#### PAR-ADM-009 POST /api/admin/users/:id/credits
- 前置：管理员登录。
- 步骤：a) bucket=granted amountMicros=+1000000 note=必填；b) 扣减穿透（余额不足以扣）；c) bucket 非法 / amountMicros=0 / note 空。
- 期望：a) 200 `{purchasedMicros,grantedMicros}`（admin.go:569-572），写 type=grant 流水；b) 402 INSUFFICIENT_CREDITS（admin.go:556-559；credit.go:219-221）；c) 400 VALIDATION_FAILED fields（admin.go:540-549）。
- 对应任务：T03、T06（M2 后走 billing.Adjust，行为不变）。
- 执行方式：自动——`TestAdminUserListAndCreditAdjust`、`TestAdminAdjustRejectsNegative`（service/credit_test.go:165）。

#### PAR-ADM-010 POST /api/admin/users/:id/usage/recalculate
- 前置：管理员登录；用户有 media_files 行且计数被人为写错。
- 步骤：调用重算。
- 期望：200 `{"storageBytes":<SUM(bytes)>}`（admin.go:572-585）；计数被 media_files 聚合覆盖（quota.go:203-220）。
- 对应任务：T03、T08（对账修正的服务端原型）。
- 执行方式：自动——`TestAdminRecalculateStorageHealsCounter`（media_quota_test.go:91）。

#### PAR-ADM-011 POST /api/admin/users/:id/media/reclaim
- 前置：管理员登录；用户存在孤儿/过期媒体。
- 步骤：a) `?dryRun=true`；b) 不带 dryRun。
- 期望：a/b) 200 回收报告 8 键 `userId,dryRun,scanned,reclaimed,freedBytes,storageUsed,storageLimit,items`，items 项 3 键 `storageKey,bytes,reason`（service/cleanup.go:29-45）；a) 不删任何数据；b) 删除后重算 storageUsed。
- 对应任务：T03、T08。
- 执行方式：自动——`TestCleanupDryRunKeepsEverything`、`TestCleanupRemovesOrphansKeepsReferenced`、`TestCleanupDowngradeStopsAtLimit`（service/cleanup_test.go:185、106、209）。

#### PAR-ADM-012 PATCH /api/admin/users/:id/role
- 前置：管理员登录；目标为非自己用户。
- 步骤：a) roleKey 设为自定义角色；b) roleKey=null 清空；c) 改自己；d) 角色实际未变化时重复提交。
- 期望：a/b) 200 `{id,roleKey}`（roleKey 可为 null）；角色实际变化时撤销该用户全部 sessions（admin_roles.go:394-399 区域）；c) 400 VALIDATION_FAILED（防锁死与「不能改自己」）；d) 不写库不撤销会话。
- 对应任务：T03、T07。
- 执行方式：自动——`TestRoleChangeTakesEffectImmediately`、`TestRoleChangesWriteAuditWithTextSnapshot`（admin_roles_test.go:196、316）、`TestAssignRoleWritesProjection`（authz/sync_test.go:224）。

#### PAR-ADM-013 admin 组级错误矩阵
- 前置：普通用户（无 role_key）登录。
- 步骤：访问任一管理端点。
- 期望：403 FORBIDDEN（LoadAdminAccess，authz.go:70-77）；悬空 role_key 一并 403 fail-closed（authz.go:87-94）；无权限点 403（RequirePermission）。
- 对应任务：T03。
- 执行方式：自动——`TestAdminRoutesRejectNonAdmin`（billing_test.go:255）、`TestLoadAdminAccessDeniesUsersWithoutRole`、`TestLoadAdminAccessDeniesDanglingRoleKey`（middleware/authz_test.go:113、126）。

## 7. 中间件错误矩阵（8 条）

#### PAR-MW-001 无/坏 Bearer 401 UNAUTHORIZED
- 步骤：不带 Authorization 或头格式错误访问受保护端点。
- 期望：401 UNAUTHORIZED（middleware.go:103-116）。
- 对应任务：T03。执行方式：自动——`TestAuthMissingToken`、`TestAuthInvalidToken`（middleware/auth_test.go:55、65）。

#### PAR-MW-002 过期 token 401 TOKEN_EXPIRED
- 期望：401 TOKEN_EXPIRED（middleware.go:109-114）。对应任务：T03。执行方式：自动——`TestAuthExpiredToken`。

#### PAR-MW-003 scope=media 的 JWT 不得用于业务接口
- 期望：401 UNAUTHORIZED（auth/auth.go:89-92，ParseAccessToken 拒绝 scope=media）。对应任务：T03。执行方式：自动——`TestMediaTokenScopeBoundary`。

#### PAR-MW-004 disabled 用户 403 ACCOUNT_DISABLED
- 期望：每请求按库内状态拒绝，不依赖 15 分钟内仍有效的 access token（middleware.go:190-209）。对应任务：T03。执行方式：自动——`TestLoadAdminAccessGrantsSystemRoleAndAssignedPermissions` 之外的 `TestPendingDeletionCanLoginRefreshAndCancel` 覆盖登录侧；业务侧为 T04 手工补充。

#### PAR-MW-005 pending_deletion 写操作 403 ACCOUNT_PENDING_DELETION
- 期望：`POST /api/orders`、`/api/ai/*`、`POST /api/me/free-grant/claim` 被 403 ACCOUNT_PENDING_DELETION 拦截（路由挂载 main.go:299、360、403；middleware.go:211-220；errs.go:71）。对应任务：T03。执行方式：自动——`TestPendingDeletionCanLoginRefreshAndCancel`。

#### PAR-MW-006 强制改密闸门与白名单
- 前置：must_change_password=true 用户。
- 步骤：a) 访问 `POST /api/auth/logout`、`POST /api/auth/refresh`、`POST /api/me/password`；b) 访问其它任意登录接口。
- 期望：a) 放行（白名单三条，middleware.go:224-228）；b) 403 PASSWORD_CHANGE_REQUIRED（middleware.go:254-257；errs.go:89）；改密成功后下一请求即放行。
- 对应任务：T03。执行方式：自动——`TestForcedPasswordChangeGate`（admin_users_test.go:207）。

#### PAR-MW-007 维护模式写拦截与豁免
- 前置：maintenanceMode=true，普通用户。
- 步骤：a) `POST /api/canvases`；b) `POST /api/auth/login`；c) `POST /api/payments/webhook/alipay`；d) `GET /api/me`。
- 期望：a) 503 MAINTENANCE_MODE（middleware.go:262-296）；b/c) 豁免放行（main.go:282-284 前缀豁免）；d) 放行（读方法）。
- 对应任务：T03。执行方式：T04 手工补充项（现有测试未覆盖 MaintenanceGate 全部分支）。

#### PAR-MW-008 限流 429 带 Retry-After
- 期望：429 RATE_LIMITED + `Retry-After: <秒>` 响应头（errs.go:99-101、34-38；各路由 limiter 挂载 main.go:260-267）。对应任务：T03。执行方式：自动——`TestRegisterRateLimitByIP`、`TestMailRateLimitByIP`（auth_test.go:355、420）。

## 8. M2 三域契约用例（22 条，全部「待实现验证」）

> 依据计划「四域接口契约」表。M2 的 membership/billing/storage 三域尚无代码（`server/internal/platform/` 下仅 identity），以下用例按计划契约签名预写，由 **T05/T06 落地后补自动化**；落地前任何一条都无法执行。现状基线引用现有 service 层实现，作为切换后的行为对照。

#### PAR-M2-001 billing.EnsureAccount(tx, userID) 建零值账本行
- 期望：注册事务内建行，OnConflict DoNothing 幂等（现状基线 credit.go:40-42）。状态：待实现验证（T05）。
#### PAR-M2-002 billing.Balance(userID) 双桶余额
- 期望：返回 purchased/granted 微元值；行不存在按零值（现状 credit.go:45-49、handler credit.go:33-35）。状态：待实现验证（T05）。
#### PAR-M2-003 billing.Reserve 先扣 granted 后扣 purchased
- 期望：跨桶扣减时 granted 清零后才动 purchased；余额不足不落任何流水返回错误（现状 credit.go:53-119 先 granted 后 purchased 分配）。状态：待实现验证（T05/T06）。
#### PAR-M2-004 billing.Refund 按 refund_of_transaction_id 幂等
- 期望：重复退款命中 `refund_of_transaction_id` 唯一索引（model/billing.go:29）按成功幂等处理，不双退（现状 credit.go:122-145）。状态：待实现验证（T05/T06）。
#### PAR-M2-005 billing.Purchase 双桶入账 + paid_until 叠加
- 期望：从 max(当前值, now()) 起叠加权益天数，权益期内充值不缩短到期（现状 credit.go:149-201）；M2 后付费身份改由 subscription.period_end 表达、credits.paid_until 退役。状态：待实现验证（T05/T06）。
#### PAR-M2-006 billing.Adjust 扣到负数拒绝
- 期望：任一桶扣为负返回余额不足错误（现状 credit.go:204-221），402 INSUFFICIENT_CREDITS。状态：待实现验证（T05/T07）。
#### PAR-M2-007 billing.ConsumeFreeTrial / RefundFreeTrial / FreeTrialRemaining
- 期望：条件自增 + RowsAffected 判定防并发超发（request.go:30-51）；退还条件递减 `value > 0` 才 -1（quota.go:194-200）；上限 image=3、video=1（quota.go:24-26）。状态：待实现验证（T05/T06）。
#### PAR-M2-008 billing.HasGrantedClaim 归域收口
- 期望：free_grant_claims (user_id, campaign_id) unique（model/model.go:62-63）由 billing 域收口，风控 denied 不计（quota.go:184-190）。状态：待实现验证（T05）。
#### PAR-M2-009 storage.Snapshot(userID)
- 期望：返回用量与配额快照供前端渲染；替代 GetMe 的 storageBytes+plan 组合读取（现状 account.go:52-70）。状态：待实现验证（T05/T06）。
#### PAR-M2-010 storage.Check 预检只读不记账
- 期望：Check 只读不写任何计数（现状 quota.go:87-103、224-232 同口径）。状态：待实现验证（T05/T06）。
#### PAR-M2-011 storage 错误语义保留：只读态与超额
- 期望：只读态 403 READ_ONLY + extra {planId,used,limit,message}；超额 507 + extra {used,limit}，body 与现状逐字节一致（计划原文写「ErrReadOnly→402」与代码不符——现状 errs.go:70 为 403，落地时以 403 为准并在评审门②确认）。状态：待实现验证（T05/T06）。
#### PAR-M2-012 storage.Commit 带 product 维度
- 期望：storage_usage 记账带 product（默认 'youc-canvas'），跨产品共享池按用户聚合。状态：待实现验证（T05/T06）。
#### PAR-M2-013 storage.Recalculate 以聚合事实源重算
- 期望：media_files.bytes 聚合覆盖计数，天然幂等（现状 quota.go:203-220）。状态：待实现验证（T05/T08）。
#### PAR-M2-014 membership.ActivePlan 含日落宽限
- 期望：graceEndsAt = period_end + 60 天（计划 D6；现状对应 sunsetGraceDays=60，quota.go:20、106-115）；只有点数无有效订阅回落 free 档但可消费。状态：待实现验证（T05/T06，D6 已拍板）。
#### PAR-M2-015 membership.GrantFromOrder 支付发会员
- 期望：markPaid 事务内调用，与加点同事务（现状 orders.go:214-239 的编排结构）。状态：待实现验证（T05/T06）。
#### PAR-M2-016 membership.Compensate 补偿
- 期望：补偿写入带 reason，流水可追溯。状态：待实现验证（T05/T07）。
#### PAR-M2-017 membership.SyncQuota 回写配额
- 期望：订阅变化后回写 storage_accounts.quota。状态：待实现验证（T05）。
#### PAR-M2-018 跨域写 = 编排层单事务
- 期望：跨域写由编排层传同一 *gorm.DB tx，域内不自开事务拼接（计划「共享机制」表）；现状同构：markPaid 单事务（orders.go:215-239）、改密单事务（account.go:292-311）。状态：待实现验证（T05/T06，评审门②）。
#### PAR-M2-019 product 列默认 'youc-canvas'
- 期望：credit_transactions/orders/media_files/storage_usage 新增 product varchar(32) default 'youc-canvas'；既有行默认值回填。状态：待实现验证（T05）。
#### PAR-M2-020 零物理外键延续
- 期望：全部新表无 FOREIGN KEY，表级隔离 + 应用层校验。状态：待实现验证（T05）。
#### PAR-M2-021 档位派生废除映射
- 期望：purchased>0⇒paid 派生废除（现状 quota.go:45-59 与 admin.go:254-258 planIDExpr 一并替换）；/api/me 与 admin users 的 planId 键仍存在，值来源换 subscription。状态：待实现验证（T05/T06，需映射对照表）。
#### PAR-M2-022 plans 表退役与 /api/plans 契约
- 期望：M2 后 /api/plans 数据源换 membership_plans 派生，响应 5 键不变（credit.go:133-151）；seed 保留 free/paid/sunset id（db/db.go:84-101）。状态：待实现验证（T05/T06）。
