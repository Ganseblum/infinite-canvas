---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T11 五个回归点用例（计划「验证命令」表第 5 行定义：登录/充值/生成扣费/配额/注销）
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
recorded_at: 2026-09-18
source: 断言值全部来自 2026-09-18 工作区源码，引用 file:line
---

# 五个回归点用例（regression points）

> 验收口径：M1/M2 落地后画布行为与现状完全一致。每个回归点 = 自动测试（`cd server && go test ./...`）+ 真实 MySQL 环境手工步骤（T04/T06/T08 各回滚点前各执行一次，SQLite 单测不能替代）。
> 证据记录位置统一为 `records/tests/infinite-canvas-platform-account-membership/evidence/`，命名 `reg-<组>-<编号>.md`；手工步骤必须粘贴请求/响应原文（含响应头）；自动测试粘贴 `go test -run <函数名>` 输出。评审门③ 关闭前五组全部用例必须留痕。

## 回归点①：登录（REG-LOGIN，6 条）

#### REG-LOGIN-001 注册→登录全链
- 前置：真实 MySQL 已迁移启动，注册开关开启。
- 步骤：注册新账号 → 用邮箱登录 → 用用户名登录。
- 期望：两次登录均 200 sessionPayload，`plan.id="free"`；platform_users.last_login_at 更新（auth.go:297）；响应键与现状一致（PAR-AUTH-001/005）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestRegisterVerifyLoginRefreshLogout`；手工——真实 MySQL 起服务后 curl 一遍。
- 证据：`evidence/reg-login-001.md`。

#### REG-LOGIN-002 刷新轮换
- 前置：已登录持 ic_refresh。
- 步骤：`POST /api/auth/refresh` → 检查新 cookie 与旧会话状态。
- 期望：200 + 新 ic_refresh/ic_media；sessions 表旧行 revoked_at 非空、新行存在；refresh 响应键与登录一致。
- 对应任务：T03、T04。
- 执行方式：自动——`TestSessionCookiesIssuedRotatedAndCleared`（media_test.go:382）；手工——真实库核对 sessions 行。
- 证据：`evidence/reg-login-002.md`。

#### REG-LOGIN-003 并发刷新竞争
- 前置：同一账号同一 cookie，两个并发请求。
- 步骤：并发 POST /api/auth/refresh。
- 期望：一个 200 一个 401；401 方不撤销胜者新会话（auth.go:355-361）；sessions 表恰好新增一行。
- 对应任务：T03（barrier 已切 sessions，testutil_test.go:256）。
- 执行方式：自动——`TestConcurrentRefreshOnlyOneSucceeds`（auth_test.go:137）。
- 证据：`evidence/reg-login-003.md`。

#### REG-LOGIN-004 复用检测
- 前置：已轮换的旧 cookie。
- 步骤：用旧 cookie 刷新。
- 期望：401；该用户全部 sessions 撤销 + media_token_version 递增（auth.go:322-328、376-385）；旧 ic_media 失效。
- 对应任务：T03、T04。
- 执行方式：自动——`TestRefreshReuseRevokesAllTokens`（auth_test.go:91）；手工——真实库核对 media_token_version。
- 证据：`evidence/reg-login-004.md`。

#### REG-LOGIN-005 登出清 cookie
- 前置：已登录。
- 步骤：POST /api/auth/logout → 检查 Set-Cookie 与 sessions 行。
- 期望：204；两枚 cookie MaxAge=-1；对应 session 行 revoked_at 置位（identity/service.go:88-91）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestRegisterVerifyLoginRefreshLogout`；手工——确认浏览器端 cookie 清除。
- 证据：`evidence/reg-login-005.md`。

#### REG-LOGIN-006 登录锁定与错误码
- 前置：真实环境一个测试账号。
- 步骤：连错 5 次 → 第 6 次 → 正确登录。
- 期望：第 6 次 429 RATE_LIMITED + Retry-After（锁 15 分钟，auth.go:65、261-264）；正确登录清零计数后可再登录（auth.go:296）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestLoginLocksAfterFiveFailures`、`TestLoginSuccessResetsFailureCount`（auth_test.go:202、225）。
- 证据：`evidence/reg-login-006.md`。

## 回归点②：充值（REG-CHARGE，6 条）

#### REG-CHARGE-001 下单快照
- 前置：已登录；存在整分价格套餐。
- 步骤：POST /api/orders。
- 期望：201；订单行快照 priceMicros/purchasedMicros/grantedMicros/entitlementDays（orders.go:65-76）；providerOrderId 回写（orders.go:88-94）。
- 对应任务：T03、T04。
- 执行方式：自动——`TestCreateOrderSnapshotsPackage`（service/orders_test.go:62）。
- 证据：`evidence/reg-charge-001.md`。

#### REG-CHARGE-002 支付回调到账
- 前置：pending 订单。
- 步骤：构造合法签名回调 status=paid、金额一致。
- 期望：订单 pending→paid + paid_at；双桶入账 + paid_until 延长，同一事务（orders.go:214-239；credit.go:149-201）；流水 type=purchase、refType=order。
- 对应任务：T04（回归点②）、T06。
- 执行方式：自动——`TestCallbackIdempotentAndAmountMismatch`（orders_test.go:92）；手工——真实库核对 credits 与 credit_transactions。
- 证据：`evidence/reg-charge-002.md`。

#### REG-CHARGE-003 重复回调不重复入账
- 前置：已 paid 订单。
- 步骤：同单再发一次合法回调。
- 期望：幂等返回成功，余额、流水、paid_until 均无第二次变化（orders.go:220-226 已 paid 直接 return nil）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestCallbackIdempotentAndAmountMismatch`；手工——重复 curl 回调后核对流水条数。
- 证据：`evidence/reg-charge-003.md`。

#### REG-CHARGE-004 金额不符拒绝到账
- 前置：pending 订单。
- 步骤：回调金额 ≠ 本地快照（含解析结果为 0）。
- 期望：拒绝入账（orders.go:197-203 无条件比对），订单保持 pending，日志留痕。
- 对应任务：T04。
- 执行方式：自动——`TestCallbackIdempotentAndAmountMismatch`。
- 证据：`evidence/reg-charge-004.md`。

#### REG-CHARGE-005 查单兜底补到账
- 前置：超过 30 分钟未支付订单，渠道侧实际已支付。
- 步骤：触发订单超时扫描（main.go:430-436 每 5 分钟）。
- 期望：先向渠道 QueryOrder 再 markPaid（orders.go:249-289）；金额不符继续拒绝；渠道仍可支付则本地不判失败。
- 对应任务：T04、T08。
- 执行方式：自动——`TestExpirePendingOrdersQueriesProviderFirst`（orders_test.go:181）。
- 证据：`evidence/reg-charge-005.md`。

#### REG-CHARGE-006 订单超时关闭与取消
- 前置：pending 订单。
- 步骤：a) 手工 cancel；b) 渠道侧 closed 回调。
- 期望：a) 200 且 status=failed；b) markFailed 条件更新仅 pending 生效（orders.go:241-245）。
- 对应任务：T04。
- 执行方式：自动——`TestCancelOrder`（orders_test.go:149）。
- 证据：`evidence/reg-charge-006.md`。

## 回归点③：生成扣费（REG-GEN，6 条）

#### REG-GEN-001 成功扣点先 granted 后 purchased
- 前置：余额 purchased=10、granted=5（微元示例），报价 8。
- 步骤：POST /api/ai/images/generations 成功。
- 期望：granted 扣 5、purchased 扣 3（credit.go:67-75）；两条 consume 流水 refType=generation；余额与流水之和守恒。
- 对应任务：T04、T06。
- 执行方式：自动——`TestReserveAndRefundAcrossBuckets`（service/credit_test.go:56）、`TestImageGenerationReservesCreditsAndWritesGeneration`（ai_test.go:235）。
- 证据：`evidence/reg-gen-001.md`。

#### REG-GEN-002 失败退点原桶退回
- 前置：同上，上游返回失败。
- 步骤：触发一次失败生成。
- 期望：running→failed 条件更新一次（request.go:131-144）；consume 流水逐条退回原桶，refund 流水带 refund_of_transaction_id（credit.go:122-145、329-365）；余额恢复。
- 对应任务：T04、T06。
- 执行方式：自动——`TestImageGenerationRefundsOnUpstreamFailure`（ai_test.go:292）、`TestVideoTaskFailureRefundsOnce`（ai_video_test.go:141）。
- 证据：`evidence/reg-gen-002.md`。

#### REG-GEN-003 同 idempotency_key 不双扣
- 前置：已成功一次生成并携带 Idempotency-Key。
- 步骤：同用户同 key 重发。
- 期望：命中 ai_requests (user_id, idempotency_key) unique（model/ai.go:29-30；request.go:97-116）返回首次结果，不重复 Reserve；余额只扣一次。
- 对应任务：T04、T06。
- 执行方式：自动——`TestImageGenerationIdempotencyAndMissingKey`（ai_test.go:333）。
- 证据：`evidence/reg-gen-003.md`。

#### REG-GEN-004 余额不足明确拒绝
- 前置：余额小于报价。
- 步骤：发起生成。
- 期望：402 INSUFFICIENT_CREDITS，余额不变、无请求行与流水（credit.go:63-65、request.go 计费前置）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestImageGenerationInsufficientCredits`（ai_test.go:373）、`TestReserveInsufficientCredits`（service/credit_test.go:106）。
- 证据：`evidence/reg-gen-004.md`。

#### REG-GEN-005 免费试用占用与退还
- 前置：已领取新人礼（granted 记录存在）。
- 步骤：a) 用免费额度生成成功；b) 失败一次；c) 并发占用。
- 期望：a) usage_records 计数 +1（上限 image=3/video=1）；b) 失败退还 -1（RefundFreeTrial 条件递减，request.go:166-177、quota.go:194-200）；c) 条件自增 RowsAffected 判定，不超发（request.go:39-50）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestFreeTrialTakesPriorityOverDiscount`（service/credit_test.go:402）。
- 证据：`evidence/reg-gen-005.md`。

#### REG-GEN-006 滞留请求收敛
- 前置：人工插入一条超时 running 请求。
- 步骤：重启服务（或触发收敛）。
- 期望：按 2×timeout 收敛为 failed 并幂等退还（request.go:219-241）；refund_pending 请求可由管理端重试（request.go:244-256）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestVideoTaskLifecycleAndRefund`（ai_video_test.go:73）覆盖任务侧；收敛逻辑为 T04 手工补充项。
- 证据：`evidence/reg-gen-006.md`。

## 回归点④：配额（REG-QUOTA，5 条）

#### REG-QUOTA-001 超限上传 507 body 一致
- 前置：free 档存储上限已知（db/db.go:86）。
- 步骤：上传至超限后再次上传。
- 期望：507 STORAGE_QUOTA_EXCEEDED + extra `{used,limit}`（quota.go:228-231、244-248）；body 与现状逐字节一致（红线）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestMediaUploadQuotaCountsAndRejects`（media_quota_test.go:14，断言 limit=40、used=66 的场景值）。
- 证据：`evidence/reg-quota-001.md`。

#### REG-QUOTA-002 只读态 403 READ_ONLY body 一致
- 前置：人为把 usage 计数写到超过档位上限。
- 步骤：上传任意文件。
- 期望：**403** READ_ONLY + extra `{planId,used,limit,message}`（quota.go:225-227、237-243；errs.go:70）——计划写 402 与代码不符，按 403 验收（见 parity-contract.md 不一致点 #2）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestMediaUploadReadOnlyReturns402`（media_quota_test.go:64，函数名与实际断言 403 不符，以断言为准）。
- 证据：`evidence/reg-quota-002.md`。

#### REG-QUOTA-003 计数按增量与删除回退
- 前置：已有文件。
- 步骤：a) 覆盖上传更大文件；b) 删除文件。
- 期望：a) 计数按 delta 增加（media_write.go:67-86）；b) 计数回退（media_quota_test.go:41-61）。
- 对应任务：T04、T06。
- 执行方式：自动——`TestMediaUploadQuotaCountsAndRejects`。
- 证据：`evidence/reg-quota-003.md`。

#### REG-QUOTA-004 重算治愈漂移
- 前置：人为把 usage_records.storage_bytes 写错（999）。
- 步骤：管理员 POST /api/admin/users/:id/usage/recalculate（或对账任务触发 Recalculate）。
- 期望：计数被 media_files SUM(bytes) 覆盖（quota.go:203-220；admin.go:572-585）。
- 对应任务：T04、T08。
- 执行方式：自动——`TestAdminRecalculateStorageHealsCounter`（media_quota_test.go:91）。
- 证据：`evidence/reg-quota-004.md`。

#### REG-QUOTA-005 档位派生口径
- 前置：四种余额形态（purchased>0 / 权益内 / 过期 60 天内 / 其余）。
- 步骤：分别 GET /api/me 与 admin users 列表。
- 期望：派生结果 free/paid/sunset 与 PlanOf 一致（quota.go:45-59），GetMe.graceEndsAt 仅日落期非空（quota.go:106-115）；admin planIDExpr 与 PlanOf 同口径（admin.go:254-258）。
- 对应任务：T04、T06（M2 后映射对照）。
- 执行方式：自动——`TestPlanOfDerivation`（service/credit_test.go:182）。
- 证据：`evidence/reg-quota-005.md`。

## 回归点⑤：注销（REG-DELETE，5 条）

#### REG-DELETE-001 申请注销进入冷静期
- 前置：已登录 active。
- 步骤：POST /api/me/deletion（正确密码）→ GET /api/me。
- 期望：200 scheduledAt=now+7 天（identity/service.go:20、149-154）；状态 pending_deletion；GetMe.deletion.status="pending"。
- 对应任务：T04（回归点⑤）。
- 执行方式：自动——`TestAccountDeletionLifecycle`（billing_test.go:156）。
- 证据：`evidence/reg-delete-001.md`。

#### REG-DELETE-002 冷静期行为边界
- 前置：pending_deletion 用户。
- 步骤：登录、刷新、导出、下单、生成。
- 期望：登录/刷新/导出放行；下单与生成 403 ACCOUNT_PENDING_DELETION（orders.go:43-45；main.go:360、403）；重复申请不重置倒计时（identity/service.go:146-148）。
- 对应任务：T04。
- 执行方式：自动——`TestPendingDeletionCanLoginRefreshAndCancel`（billing_test.go:196）。
- 证据：`evidence/reg-delete-002.md`。

#### REG-DELETE-003 撤销注销
- 前置：pending_deletion 用户。
- 步骤：POST /api/me/deletion/cancel。
- 期望：200 {status:"active"}；status 回 active、deletion_scheduled_at 清空；非冷静期调用 409 DELETION_NOT_PENDING（identity/service.go:158-169）。
- 对应任务：T04。
- 执行方式：自动——`TestPendingDeletionCanLoginRefreshAndCancel`。
- 证据：`evidence/reg-delete-003.md`。

#### REG-DELETE-004 到期匿名化跨表清理
- 前置：把 deletion_scheduled_at 改到已过期。
- 步骤：触发匿名化任务（main.go 后台调度）。
- 期望：同一事务内：platform_users 置 disabled + 占位 email/username + 清空密码哈希（identity/service.go:192-206）；全部 sessions 撤销且 IP/UA 置空（identity/service.go:207-218）；画布/素材/生成/媒体索引业务清理 + 存储计数扣减（service/deletion.go:35 起）；媒体对象与 orig 在事务提交后物理删除。
- 对应任务：T04、T06。
- 执行方式：自动——`TestAnonymizeExpiredDeletesMediaObjectsAndOrigs`、`TestAnonymizeExpiredOrigDeleteFailureBestEffort`（service/deletion_test.go:24、62）。
- 证据：`evidence/reg-delete-004.md`。

#### REG-DELETE-005 对象删除失败不丢账
- 前置：匿名化时对象存储不可用。
- 步骤：注入删除失败后触发匿名化。
- 期望：行清理不受阻、对象残留由保留期清理任务兜底（deletion_test.go:62；cleanup.go:129-133 同口径：对象删除失败保留数据库记录，不静默丢账）。
- 对应任务：T04、T08。
- 执行方式：自动——`TestAnonymizeExpiredOrigDeleteFailureBestEffort`。
- 证据：`evidence/reg-delete-005.md`。
