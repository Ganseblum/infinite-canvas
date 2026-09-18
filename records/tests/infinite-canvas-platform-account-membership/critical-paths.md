---
plan_id: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
task: T11 关键路径异常矩阵用例（T08 落地验证 + 评审门③证据；矩阵定义见计划「关键路径异常矩阵」）
parent: PLAN-PLATFORM-ACCOUNT-MEMBERSHIP
recorded_at: 2026-09-18
source: 矩阵行的幂等前置已逐条对照代码；断言值来自 2026-09-18 工作区源码
---

# 三条关键路径异常矩阵用例（critical paths）

> 每条用例对应异常矩阵的一行，字段「检测 / 处置 / 重试责任方 / 幂等前置 / 用户可见结果」直接引自计划矩阵并对照代码核实。
> 证据记录位置：`evidence/cp<路径>-<行号>.md`（例如 `evidence/cp1-002.md`）。T08 评审门③ 前三条路径逐行留痕。

## 路径一：生成扣费 Reserve → 上游生成 → 成功落账 / 失败 Refund（CP1）

幂等基线（代码核实）：ai_requests (user_id, idempotency_key) unique（model/ai.go:29-30；request.go:97-116 冲突返回既有行）；退款按 refund_of_transaction_id 唯一索引幂等（model/billing.go:29；credit.go:122-145）。

#### CP1-001 超时（上游生成超时/断连）
- 检测：handler 感知上游错误返回（现状同步链路）。
- 处置：生成失败 → 请求行 running→failed → Refund 回滚预留点数（request.go:131-164）；免费试用退还计数（request.go:166-177）。
- 重试责任方：画布编排层；Refund 为进程内本地事务无退避。
- 幂等前置：状态收敛条件更新 `status='running'` 才执行（request.go:136-142），并发收敛/多实例不重复退款；退款唯一索引兜底。
- 用户可见结果：生成失败提示，点数原路退回原桶。
- 对应任务：T06、T08。
- 执行方式：自动——`TestImageGenerationRefundsOnUpstreamFailure`（ai_test.go:292）、`TestVideoTaskFailureRefundsOnce`（ai_video_test.go:141）。
- 证据：`evidence/cp1-001.md`。

#### CP1-002 重复请求（同 idempotency_key）
- 检测：ai_requests unique 冲突命中既有记录（request.go:104-116）。
- 处置：直接返回首次请求的结果，不重复扣费。
- 重试责任方：N/A（无重试，幂等返回）。
- 幂等前置：ai_requests (user_id, idempotency_key) unique（model/ai.go:29-30）；维度分析字段幂等命中时不回写（model/ai.go:48 注释）。
- 用户可见结果：与首次请求一致。
- 对应任务：T06、T08。
- 执行方式：自动——`TestImageGenerationIdempotencyAndMissingKey`（ai_test.go:333）。
- 证据：`evidence/cp1-002.md`。

#### CP1-003 状态冲突（余额不足/只读态）
- 检测：Reserve 返回余额不足（credit.go:63-65 ErrInsufficientCredits）；并发场景行锁内先查后扣（credit.go:316-326 lockCredit SELECT FOR UPDATE）。
- 处置：拒绝并返回 402 INSUFFICIENT_CREDITS，不落生成请求与流水。
- 重试责任方：N/A（用户补点后重发新请求）。
- 幂等前置：Reserve 在行锁内先查后扣，并发串行化（credit.go:58-118；`TestConcurrentReserveNeverOverdraws`）。
- 用户可见结果：明确错误码，余额不变。
- 对应任务：T06、T08。
- 执行方式：自动——`TestImageGenerationInsufficientCredits`（ai_test.go:373）、`TestReserveInsufficientCredits`、`TestConcurrentReserveNeverOverdraws`（service/credit_test.go:106、124）。
- 证据：`evidence/cp1-003.md`。

#### CP1-004 回调失败（矩阵行 N/A 的前提验证）
- 检测/处置/重试/幂等：N/A——生成扣费为同步链路，无异步回调（计划矩阵原文）。
- 本用例验证前提成立：图片生成在请求生命周期内完成 Reserve→上游→落账/Refund，失败即时 MarkFailed；视频异步任务的视频任务轮询（aitask.go）仍由服务端主动轮询收敛，不存在「回调」入口；无任何 /api/ai 回调路由（main.go:360-368）。
- 对应任务：T06、T08。
- 执行方式：自动——`TestVideoTaskLifecycleAndRefund`（ai_video_test.go:73）+ 路由表检查（main.go 人工核对）。
- 证据：`evidence/cp1-004.md`。

## 路径二：支付回调 markPaid 幂等（CP2）

幂等基线（代码核实）：orders.provider_order_id unique（model/billing.go:58）+ 订单状态条件更新（orders.go:214-239 markPaid：行锁 → `status=='paid'` 直接 nil → 非 pending 不入账）。

#### CP2-001 超时（回调未达/延迟 → 查单兜底）
- 检测：订单长期非终态被超时扫描发现（main.go:430-436 每 5 分钟，30 分钟阈值；orders.go:249-289）。
- 处置：主动 QueryOrder → markPaid 补齐加点和（M2 后）membership.GrantFromOrder。
- 重试责任方：后台轮询，沿用现状节奏（不改次数/退避）。
- 幂等前置：provider_order_id unique + 状态条件更新；到账走与回调同一条 markPaid。
- 用户可见结果：到账可能延迟但不丢失。
- 对应任务：T04、T06、T08。
- 执行方式：自动——`TestExpirePendingOrdersQueriesProviderFirst`（service/orders_test.go:181）。
- 证据：`evidence/cp2-001.md`。

#### CP2-002 重复请求（同单重复回调）
- 检测：markPaid 行锁内 `locked.Status == "paid"` → 直接 return nil（orders.go:220-222）。
- 处置：幂等返回成功，不重复加点/发会员。
- 重试责任方：N/A（无重试，幂等返回）。
- 幂等前置：仅 待支付→已支付 条件跃迁；provider_order_id unique 防同渠道单号重复落单。
- 用户可见结果：无差异。
- 对应任务：T04、T06、T08。
- 执行方式：自动——`TestCallbackIdempotentAndAmountMismatch`（orders_test.go:92）。
- 证据：`evidence/cp2-002.md`。

#### CP2-003 状态冲突（金额不符/订单非待支付）
- 检测：验签 + 金额无条件比对（orders.go:197-203，解析结果为 0 不短路，差异清单 #7）；已取消/已失败订单不再到账（orders.go:223-226）。
- 处置：拒绝入账并记日志；退款补偿走后台 RefundPending 标记重试（订单侧现状 orders_test 覆盖；生成侧 refund_pending 由管理端 RetryPendingRefunds 重试，request.go:244-256）；**如需新增重试次数/退避属行为边界值，须在评审门②前报用户确认**（计划矩阵原文）。
- 重试责任方：后台人工触发/网关侧重发。
- 幂等前置：退款以 credit_transactions 流水（refund_of_transaction_id）判定幂等。
- 用户可见结果：异常单进入后台补偿/客服流程，余额不被异常回调污染。
- 对应任务：T06、T08。
- 执行方式：自动——`TestCallbackIdempotentAndAmountMismatch`、`TestPackagePriceMustBeWholeCents`（orders_test.go:92、208）。
- 证据：`evidence/cp2-003.md`。

#### CP2-004 回调失败（markPaid 事务内异常 → 整体回滚）
- 检测：markPaid 事务报错（orders.go:215-239 单事务包裹状态跃迁 + 双桶入账 + paid_until）。
- 处置：任一步失败整体回滚；下次回调（或查单兜底）幂等重放。M2 后 GrantFromOrder 编排进同一 tx（计划「回调失败」行）。
- 重试责任方：依赖网关重发 + 查单兜底，次数由网关侧决定。
- 幂等前置：同一事务 + provider_order_id unique。
- 用户可见结果：无感或短暂延迟到账。
- 对应任务：T06、T08。
- 执行方式：自动——`TestCreateOrderSnapshotsPackage`（事务结构）+ T06 落地后新增「Purchase 抛错回滚订单状态」用例（待实现验证）。
- 证据：`evidence/cp2-004.md`。

## 路径三：媒体落盘记账 Check 预检 → 落盘 → 同事务记账（CP3）

幂等基线（代码核实）：media_files 行为事实源，记账随写库事务（media_write.go:67-86：media_files upsert on (user_id, storage_key) + usage_records 增量同事务）；保留期清理后重算（cleanup.go:159-163；quota.go:203-220）。

#### CP3-001 超时（落盘成功但事务失败/进程中断 → 夜间对账）
- 检测：夜间对账任务发现三层记账不平（T08 新增任务；现状原型：RecalculateStorage 用 media_files SUM(bytes) 覆盖计数，quota.go:203-220）。
- 处置：比对 media_files.bytes 聚合 vs storage_usage vs storage_accounts → storage.Recalculate 修正 + 告警（告警通道见未决项 D9）。
- 重试责任方：平台后台任务每日一次。
- 幂等前置：Recalculate 以聚合事实源重算，天然幂等（重复执行结果一致）。
- 用户可见结果：短暂误差自动收敛。
- 对应任务：T05（storage_accounts 落表）、T08（对账任务与告警）。
- 执行方式：现状自动——`TestAdminRecalculateStorageHealsCounter`（media_quota_test.go:91）；对账任务用例**待实现验证**（T08 落地后补）。
- 证据：`evidence/cp3-001.md`。

#### CP3-002 重复请求（同文件重复上传/覆盖）
- 检测：沿用现状媒体写入语义判重（media_write.go:68-78 按 (user_id, storage_key) 查旧行）。
- 处置：覆盖写按 delta 记账（written - previous.Bytes），不重复记账；旧 orig 清理 best-effort（media.go:462-466）。
- 重试责任方：N/A（写入侧幂等）。
- 幂等前置：media_files (user_id, storage_key) 唯一行（upsert），记账以行存在性判定。
- 用户可见结果：无差异。
- 对应任务：T06、T08。
- 执行方式：自动——`TestMediaPutOverwriteAndDeleteRemoveOrig`（media_watermark_test.go:268）、`TestMediaUploadQuotaCountsAndRejects`（media_quota_test.go:41-53 覆盖上传 delta 断言）。
- 证据：`evidence/cp3-002.md`。

#### CP3-003 状态冲突（配额不足/只读态）
- 检测：上传前预检返回 ErrQuotaExceeded / ErrReadOnly（quota.go:224-232：先判只读再判增量；handler media.go:354、381、413 三处调用点）。
- 处置：拒绝上传；507 / 403（READ_ONLY，代码现状 403，计划矩阵写 402 属口径不一致，见 parity-contract.md 不一致点 #2）响应 body 与现状逐字节一致。
- 重试责任方：N/A（用户清理空间或升级档位后重试）。
- 幂等前置：Check 只读不记账（quota.go:87-103 纯读）。
- 用户可见结果：与现状一致的错误体（507 extra {used,limit}；403 extra {planId,used,limit,message}）。
- 对应任务：T06、T08。
- 执行方式：自动——`TestMediaUploadQuotaCountsAndRejects`、`TestMediaUploadReadOnlyReturns402`（media_quota_test.go:14、64）、`TestModerationRejectsUploadWithoutQuota`（moderation_test.go:110）。
- 证据：`evidence/cp3-003.md`。

#### CP3-004 回调失败（矩阵行 N/A 的前提验证）
- 检测/处置/重试/幂等：N/A——进程内链路，无外部回调（计划矩阵原文）。
- 本用例验证前提成立：媒体写入为 handler 进程内 `storage.Put` + DB 事务（media_write.go:46-92），事务失败时删除已写对象补偿（media_write.go:87-89 `_ = s.storage.Delete(ctx, objectPath)`）；对外无回调端点（/api/media 路由无 webhook，main.go:371-388）。
- 对应任务：T06、T08。
- 执行方式：自动——`TestImageGenerationSaveFailureCompensatesOrig`（ai_watermark_test.go:344）覆盖落盘失败补偿语义。
- 证据：`evidence/cp3-004.md`。

## 汇总核对表

| 路径 × 异常类别 | 用例 | 现有自动测试 | 待实现验证 |
| --- | --- | --- | --- |
| CP1 超时 | CP1-001 | TestImageGenerationRefundsOnUpstreamFailure、TestVideoTaskFailureRefundsOnce | — |
| CP1 重复请求 | CP1-002 | TestImageGenerationIdempotencyAndMissingKey | — |
| CP1 状态冲突 | CP1-003 | TestImageGenerationInsufficientCredits、TestReserveInsufficientCredits、TestConcurrentReserveNeverOverdraws | — |
| CP1 回调失败（N/A） | CP1-004 | TestVideoTaskLifecycleAndRefund | — |
| CP2 超时 | CP2-001 | TestExpirePendingOrdersQueriesProviderFirst | — |
| CP2 重复请求 | CP2-002 | TestCallbackIdempotentAndAmountMismatch | — |
| CP2 状态冲突 | CP2-003 | TestCallbackIdempotentAndAmountMismatch、TestPackagePriceMustBeWholeCents | — |
| CP2 回调失败 | CP2-004 | TestCreateOrderSnapshotsPackage | T06 落地后补回滚用例 |
| CP3 超时 | CP3-001 | TestAdminRecalculateStorageHealsCounter | T08 对账任务 + 告警 |
| CP3 重复请求 | CP3-002 | TestMediaPutOverwriteAndDeleteRemoveOrig | — |
| CP3 状态冲突 | CP3-003 | TestMediaUploadQuotaCountsAndRejects、TestMediaUploadReadOnlyReturns402 | — |
| CP3 回调失败（N/A） | CP3-004 | TestImageGenerationSaveFailureCompensatesOrig | — |
