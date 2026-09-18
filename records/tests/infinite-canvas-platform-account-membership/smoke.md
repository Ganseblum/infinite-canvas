---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T11 冒烟集（计划「验证命令」引用：冒烟集全过 + 评审门③/④ 前端到端用例全部通过）
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
recorded_at: 2026-09-18
source: 全部为 2026-09-18 工作区中真实存在的测试函数，grep `^func Test` 逐文件核实
---

# 冒烟集（smoke）

> 执行方式：`cd server && go test ./...`（Go 1.26.3 + gorm.io/driver/sqlite v1.6.0，见 server/go.mod:3、23）。测试统一由 `newTestDB`（handler/testutil_test.go:60-89）建内存 SQLite 并执行与生产一致的 `db.Migrate` + `db.SeedPlans` + `authz.Sync`。
> **红线**：SQLite 单测不能替代真实 MySQL 迁移启动（真实库检查项见本文件末节）。下列 74 个测试函数全部真实存在，按回归点分组；括号内为 `文件:测试函数起始行`。

## A. 回归点① 登录与会话（12 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestRegisterVerifyLoginRefreshLogout`（handler/auth_test.go:16） | 注册→验证→登录→刷新→登出全链（REG-LOGIN-001/002/005） |
| `TestRefreshReuseRevokesAllTokens`（handler/auth_test.go:91） | 复用检测撤销全部会话 + bump 媒体版本（REG-LOGIN-004） |
| `TestConcurrentRefreshOnlyOneSucceeds`（handler/auth_test.go:137） | 并发轮换竞争，barrier 表名已为 sessions（REG-LOGIN-003） |
| `TestLoginLocksAfterFiveFailures`（handler/auth_test.go:202） | 5 次失败锁 15 分钟 |
| `TestLoginSuccessResetsFailureCount`（handler/auth_test.go:225） | 登录成功清零计数 |
| `TestConcurrentEmailVerifyOnlyOneSucceeds`（handler/auth_test.go:260） | 邮箱令牌一次性并发消费 |
| `TestEmailTokenTTLByPurpose`（handler/auth_test.go:313） | 验证邮件 24h / 重置 1h |
| `TestRegisterRateLimitByIP`（handler/auth_test.go:355） | 注册 IP 限流 429 |
| `TestMailRateLimitByEmail`（handler/auth_test.go:383） | 同邮箱 3 次/小时限流 |
| `TestMailRateLimitByIP`（handler/auth_test.go:420） | 邮件 IP 限流 |
| `TestSessionCookiesIssuedRotatedAndCleared`（handler/media_test.go:382） | 双 cookie 签发/轮换/清除属性（PAR-AUTH-014） |
| `TestForcedPasswordChangeGate`（handler/admin_users_test.go:207） | 强制改密闸门与唯一清除点（PAR-MW-006） |

## B. 中间件与令牌边界（6 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestAuthMissingToken`（middleware/auth_test.go:55） | 无 Bearer 401 UNAUTHORIZED（PAR-MW-001） |
| `TestAuthInvalidToken`（middleware/auth_test.go:65） | 坏 token 401（PAR-MW-001） |
| `TestAuthExpiredToken`（middleware/auth_test.go:79） | 过期 401 TOKEN_EXPIRED（PAR-MW-002/PAR-BILL-007） |
| `TestAuthValidTokenWritesUserContext`（middleware/auth_test.go:90） | 合法 token 写入 user_id 上下文 |
| `TestMediaTokenScopeBoundary`（auth/auth_test.go:12） | scope=media 与业务 JWT 互斥（PAR-MW-003/PAR-MED-010） |
| `TestHashEmailIsNormalizedAndOpaque`（auth/auth_test.go:47） | 限流 key 邮箱哈希规范化 |

## C. 回归点② 充值与订单（7 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestCreateOrderSnapshotsPackage`（service/orders_test.go:62） | 下单快照与订单结构（REG-CHARGE-001） |
| `TestCallbackIdempotentAndAmountMismatch`（service/orders_test.go:92） | 回调幂等 + 金额不符拒绝（REG-CHARGE-002/003/004，CP2-002/003） |
| `TestCancelOrder`（service/orders_test.go:149） | 取消订单与状态条件更新（REG-CHARGE-006） |
| `TestExpirePendingOrdersQueriesProviderFirst`（service/orders_test.go:181） | 超时查单兜底（REG-CHARGE-005，CP2-001） |
| `TestPackagePriceMustBeWholeCents`（service/orders_test.go:208） | 非整分价格拒绝 |
| `TestAdminPackageValidation`（handler/billing_test.go:322） | 管理端档位校验 |
| `TestCreditsAndPackagesRequireAuth`（handler/billing_test.go:96） | billing 组鉴权（PAR-BILL-006） |

## D. 回归点③ 生成扣费（12 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestQuoteRejectsUnsupportedParams`（handler/ai_test.go:196） | 报价参数校验 |
| `TestImageGenerationReservesCreditsAndWritesGeneration`（handler/ai_test.go:235） | 成功预扣与请求行（REG-GEN-001） |
| `TestImageGenerationRefundsOnUpstreamFailure`（handler/ai_test.go:292） | 失败退点（REG-GEN-002，CP1-001） |
| `TestImageGenerationIdempotencyAndMissingKey`（handler/ai_test.go:333） | 幂等不双扣（REG-GEN-003，CP1-002） |
| `TestImageGenerationInsufficientCredits`（handler/ai_test.go:373） | 余额不足 402（REG-GEN-004，CP1-003） |
| `TestVideoTaskLifecycleAndRefund`（handler/ai_video_test.go:73） | 视频任务生命周期与退款（CP1-004） |
| `TestVideoTaskFailureRefundsOnce`（handler/ai_video_test.go:141） | 视频失败只退一次（CP1-001） |
| `TestChatNonStreamAndRequestConvergence`（handler/ai_video_test.go:197） | 对话请求收敛 |
| `TestReserveAndRefundAcrossBuckets`（service/credit_test.go:56） | 先 granted 后 purchased + 原桶退回（REG-GEN-001） |
| `TestReserveInsufficientCredits`（service/credit_test.go:106） | 余额不足不落流水（CP1-003） |
| `TestConcurrentReserveNeverOverdraws`（service/credit_test.go:124） | 并发预扣不超扣（CP1-003） |
| `TestAdminAdjustRejectsNegative`（service/credit_test.go:165） | 管理端扣减穿透拒绝（PAR-ADM-009） |

## E. 回归点④ 配额与媒体（15 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestMediaUploadQuotaCountsAndRejects`（handler/media_quota_test.go:14） | 507 + used/limit、覆盖 delta、删除回退（REG-QUOTA-001/003，CP3-002/003） |
| `TestMediaUploadReadOnlyReturns402`（handler/media_quota_test.go:64） | 只读态 403 READ_ONLY（函数名历史遗留，断言 403；REG-QUOTA-002） |
| `TestAdminRecalculateStorageHealsCounter`（handler/media_quota_test.go:91） | 重算治愈漂移（REG-QUOTA-004，CP3-001） |
| `TestMediaLocalUploadHeadGetDelete`（handler/media_test.go:42） | 上传/HEAD/GET/DELETE 全链（PAR-MED-001/003/007） |
| `TestMediaFileTooLargeBoundary`（handler/media_test.go:185） | 413 边界 |
| `TestMediaChecksumMismatchLeavesNothing`（handler/media_test.go:223） | 409 且无残留 |
| `TestMediaEmailNotVerifiedBlocksUploadOnly`（handler/media_test.go:160） | 403 EMAIL_NOT_VERIFIED 只拦写（PAR-MED-004） |
| `TestMediaReadAuthBearerAndCookie`（handler/media_test.go:265） | 双凭据读取边界（PAR-MED-010） |
| `TestMediaPutOverwriteAndDeleteRemoveOrig`（handler/media_watermark_test.go:268） | 覆盖/删除连带 orig（CP3-002） |
| `TestMediaDelivery304SkipsBodyRead`（handler/media_watermark_test.go:236） | 304 交付 |
| `TestDownloadRequestTwoStateResponse`（handler/media_download_test.go:72） | 申请下载两态（PAR-MED-008） |
| `TestDownloadRequestStrictOwnership`（handler/media_download_test.go:49） | 归属严格校验 404 |
| `TestDownloadTokenValidationChain`（handler/media_download_test.go:145） | 取件令牌防探测（PAR-MED-009） |
| `TestPlanOfDerivation`（service/credit_test.go:182） | 档位派生口径（REG-QUOTA-005，PAR-ADM-003） |
| `TestCleanupRemovesOrphansKeepsReferenced`（service/cleanup_test.go:106） | 孤儿回收保留引用（PAR-ADM-011） |

## F. 回归点⑤ 注销与免费赠送（8 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestAccountDeletionLifecycle`（handler/billing_test.go:156） | 申请→冷静期→GetMe 投影（REG-DELETE-001/002） |
| `TestPendingDeletionCanLoginRefreshAndCancel`（handler/billing_test.go:196） | 冷静期可登录刷新取消、写操作 403（REG-DELETE-002/003，PAR-ME-013） |
| `TestAnonymizeExpiredDeletesMediaObjectsAndOrigs`（service/deletion_test.go:24） | 到期匿名化跨表清理（REG-DELETE-004） |
| `TestAnonymizeExpiredOrigDeleteFailureBestEffort`（service/deletion_test.go:62） | 对象删除失败不丢账（REG-DELETE-005） |
| `TestFreeGrantClaimIsIdempotent`（handler/account_test.go:32） | 新人礼幂等（PAR-ME-007） |
| `TestFreeGrantClaimConcurrentUniqueConstraint`（handler/account_test.go:137） | 并发领取唯一约束兜底 |
| `TestFreeGrantRiskThresholdDenies`（handler/account_test.go:73） | 风控拒绝 403（PAR-ME-008） |
| `TestFreeGrantDailyBudgetDenies`（handler/account_test.go:105） | 日预算拒绝 |

## G. 管理端身份链路（10 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestAdminCreateUserReturnsOneTimeTemporaryPassword`（handler/admin_users_test.go:65） | 建号一次性临时密码（PAR-ADM-004） |
| `TestAdminResetPasswordSetsMustChangeAndRevokesTokens`（handler/admin_users_test.go:326） | 重置他人密码联动撤销（PAR-ADM-008） |
| `TestAdminResetOwnPasswordKeepsUnforced`（handler/admin_users_test.go:369） | 自重置不置位 |
| `TestAdminUserListAndCreditAdjust`（handler/billing_test.go:270） | 用户列表 + 点数调整（PAR-ADM-003/009） |
| `TestAdminMeReturnsRoleAndPermissions`（handler/admin_roles_test.go:72） | /admin/me（PAR-ADM-001） |
| `TestPermissionDeniedForUserWithoutRole`（handler/admin_roles_test.go:103） | 无角色 403（PAR-ADM-013） |
| `TestRoleChangeTakesEffectImmediately`（handler/admin_roles_test.go:196） | 角色变更立即生效并撤销会话（PAR-ADM-012） |
| `TestLoadAdminAccessDeniesUsersWithoutRole`（middleware/authz_test.go:113） | LoadAdminAccess fail-closed（PAR-ADM-013） |
| `TestLoadAdminAccessDeniesDanglingRoleKey`（middleware/authz_test.go:126） | 悬空角色 403 |
| `TestAdminRoutesRejectNonAdmin`（handler/billing_test.go:255） | 管理组整体拒绝非管理员 |

## H. 基础契约守护（4 个）

| 测试函数 | 覆盖点 |
| --- | --- |
| `TestMeIncludesCreditsPlanAndDeletion`（handler/billing_test.go:129） | /api/me 键集（PAR-ME-001） |
| `TestSiteSettingsPersistAndGateRegistration`（handler/site_test.go:97） | 注册开关 503（PAR-AUTH-003） |
| `TestEnsureAdminWritesSystemRoleKey`（db/db_test.go:37） | EnsureAdmin 写系统角色 |
| `TestAdminRoutesAllCarryRegisteredPermission`（cmd/server/admin_routes_test.go:16） | 51 条管理路由计数与权限点锁定（PAR-ADM 组级红线） |

## 真实 MySQL 启动检查（与上述单测互补，仓库红线）

`cd server && DATABASE_URL='mysql://<user>:<pass>@127.0.0.1:3306/<db>' go run ./cmd/server` 每个回滚点前执行一次：

1. 服务启动完成、AutoMigrate 无错（db/db.go:49-83）；`platform_users`/`sessions` 表建成。
2. `plans` seed 存在 free/paid/sunset 三行（db/db.go:84-101）。
3. 唯一索引在真库生效：sessions.token_hash、orders.provider_order_id、credit_transactions.refund_of_transaction_id、ai_requests (user_id, idempotency_key)、free_grant_claims (user_id, campaign_id)。
4. 五个回归点的手工步骤各过一遍（regression-points.md），证据落 `evidence/`。

**合计：74 个现有测试函数**（A 12 + B 6 + C 7 + D 12 + E 15 + F 8 + G 10 + H 4）。
