---
scope: 全产品功能验收
parent: records/tests/
source: 用例由 docs/content/docs/progress/pending-test.mdx 验收项、records/tests/ 既有用例库与本批功能收尾代码逐域归纳
---

# 全功能验收测试用例库（all features）

> 覆盖当前全部功能域的人工验收用例，按模块分组。执行环境：`https://sim-art.youc.online`（主站）+ `https://sim-admin.youc.online`（后台），测试账号见 pending-test「测试数据导入」节。
> 优先级：**P0** 核心链路冒烟（每次发版必过）、**P1** 重要功能与边界、**P2** 回归与细节。
> 标注「自动：`TestXxx`」的用例已有 Go 自动化覆盖（`cd server && go test ./...` 全绿即可信），人工只需抽验；其余为手工用例。水印域另有专库 `records/tests/infinite-canvas-media-watermark/`，账号与会员域另有 `records/tests/infinite-canvas-platform-account-membership/`（148 条）。
> 红线：涉及新列（`assets.is_aigc`）与 `/api/v1` 前缀的用例，必须先完成 sim 部署并在真 MySQL 上验证过启动迁移。

## A. 账号与会话（ACC）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| ACC-001 | P0 | 注册与邮件验证 | 注册页填邮箱/用户名/密码，勾选同意条款提交；api 日志（`MAIL_DRIVER=log`）取验证链接并打开 | 注册成功提示验证邮件；未验证前登录被拒；打开链接后验证成功 | 自动：`TestRegisterVerifyLoginRefreshLogout` |
| ACC-002 | P0 | 登录/登出 | 正确密码登录 → 用户菜单登出 → 直访业务页 | 登录进站；登出后访问业务页跳 `/login` | 自动：同上 |
| ACC-003 | P0 | 会话保持 | 登录后 F5 硬刷新；等 access token 过期后再操作 | 刷新不闪登录页；过期自动刷新一次成功不掉线 | 手工 |
| ACC-004 | P1 | 并发刷新单飞 | 两个标签页同时让 token 过期后并发请求 | 只有一个刷新请求真正发出，两页会话都继续有效 | 自动：`TestConcurrentRefreshOnlyOneSucceeds` |
| ACC-005 | P1 | 刷新令牌复用检测 | 记下旧 refresh cookie，刷新轮换后用旧 cookie 调 `/api/v1/auth/refresh` | 全部会话撤销、媒体令牌版本递增，需重新登录 | 自动：`TestRefreshReuseRevokesAllTokens` |
| ACC-006 | P1 | 登录失败锁定 | 连续 5 次错密码 → 再试；随后用正确密码登录 | 5 次后锁定 15 分钟（429）；成功登录计数清零 | 自动：`TestLoginLocksAfterFiveFailures` |
| ACC-007 | P1 | 邮件限流 | 同邮箱 1 小时内第 4 次申请验证/重置邮件；换邮箱同 IP 第 11 次 | 同邮箱 3 次/小时、同 IP 10 次/小时，超限 429 | 自动：`TestMailRateLimitByEmail` / `TestMailRateLimitByIP` |
| ACC-008 | P0 | 重置密码 | 忘记密码走邮件链接重置；观察链接时效 | 1 小时内有效；重置后旧密码失效、其他设备被踢、`ic_media` 立即失效（图裂，重新登录恢复） | 手工 |
| ACC-009 | P1 | 重置链接防并发重放 | 两个标签页同时打开同一重置链接并先后提交 | 只有一次成功 | 手工 |
| ACC-010 | P0 | 修改密码 | 个人中心改密后重新登录、查看画布图片 | 改密后旧会话/媒体 cookie 失效，重新登录一切正常 | 手工 |
| ACC-011 | P1 | 强制改密闸门 | admin 重置某用户密码 → 该用户登录 → 调业务接口 → 按引导改密 | 登录后置位；业务接口 403 `PASSWORD_CHANGE_REQUIRED`（登出/刷新/改密除外）；改密后恢复 | 自动：`TestForcedPasswordChangeGate` |
| ACC-012 | P0 | 注销冷静期 | 申请注销（校验密码）→ 尝试登录/刷新/生成/下单/撤销 | 重复申请幂等不重置倒计时；冷静期可登录可刷新；生成与下单 403 `ACCOUNT_PENDING_DELETION`；撤销后账号恢复 | 自动：`TestPendingDeletionCanLoginRefreshAndCancel` |
| ACC-013 | P1 | 注册开关 | 生产模板下不设置 `REGISTRATION_ENABLED`，访问注册页 | 注册默认关闭（合规方向） | 手工 |
| ACC-014 | P2 | 未验证邮箱引导 | 未验证账号上传媒体 | 422 `EMAIL_NOT_VERIFIED` 附「去验证」入口 | 手工 |
| ACC-015 | P1 | 个人数据导出 | 个人中心点「数据导出」 | 下载 `youc-export.json`，含 profile/credits/orders/generations/canvases | 手工 |
| ACC-016 | P1 | 账号间数据隔离 | A 登出后 B 登录，逐页查看个人中心/订单/素材/画布 | 不出现 A 的任何数据 | 手工 |
| ACC-017 | P2 | 注销到期匿名化 | 把 `deletion_scheduled_at` 调到已过期并触发任务后用原邮箱重新注册 | 原账号数据软删、令牌撤销；新注册得到全新账号 | 手工（真环境） |

## B. 会员与权益（MEM）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| MEM-001 | P0 | 发放会员即时生效 | admin「会员管理」发放付费会员 → 用户刷新个人中心 | 「当前档位」立即付费；存储配额 50MB → 1GB | 手工 |
| MEM-002 | P0 | 作废与日落宽限 | admin 作废订阅 → 刷新个人中心 | 立即失去付费身份并进入 60 天日落宽限（宽限至 = 作废时刻 + 60 天），配额回落 200MB | 手工 |
| MEM-003 | P0 | 购买会员（planId 下单） | 用户对付费档 `POST /api/v1/orders {"planId":"paid"}` → 渠道回调 | 订单到账、订阅生效、`storage_accounts.quota_bytes` 回写 1GB、**无点数流水**（会员订单不加点） | 自动：`TestMembershipOrderWebhookGrantsCreditsAndSubscription` |
| MEM-004 | P1 | 续期叠加 | 连续购买两次会员 | `paid_until` 从原截止时间延长，不缩短 | 自动：`TestGrantFromOrderStacksRenewal` |
| MEM-005 | P1 | 只读态 | 把用量制造到超过派生档位上限 | `readOnly=true`、上传 403 `READ_ONLY`（带 planId）、浏览/编辑/删除/导出仍可用 | 手工 |
| MEM-006 | P1 | 超档上传 | 用量接近上限时再传大文件 | 507 `STORAGE_QUOTA_EXCEEDED`，响应带 `used` 与 `limit` | 手工 |
| MEM-007 | P2 | 用量记账一致 | 并发上传后对比 `usage.storageBytes` 与 `SUM(media_files.bytes)` | 相等 | 手工（可 SQL 核对） |
| MEM-008 | P1 | 自然到期回落 | 构造订阅 `period_end` 过期 → 跑夜间对账 | 进入日落宽限、配额回写由对账兜底收敛 | 手工 |
| MEM-009 | P2 | 订单快照稳定 | admin 修改档位价格后查看已创建订单 | 订单快照不变 | 手工 |
| MEM-010 | P1 | 会员管理模块 | admin 会员列表筛选/分页、发放、续期、补偿、作废全操作一遍 | 列表字段正确、操作即生效并同步配额 | 手工 |

## C. 点数账本（CRD）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| CRD-001 | P0 | 流水不变量 | 任意产生流水的操作后，按桶 `SUM(amount_micros)` 对比两桶余额 | 恒等 | 手工（SQL 核对） |
| CRD-002 | P0 | 先扣赠送再扣购入 | 两桶都有余额时消费 | 先扣 granted 再扣 purchased，流水各自成条 | 自动：`TestReserveDeductsGrantedBeforePurchased` |
| CRD-003 | P1 | 非法调整被拒 | admin 调整接口传负数/0/无说明 | 拒绝且余额与流水不变 | 自动：`TestAdjustRejectsNegative` |
| CRD-004 | P1 | 流水只读 | 对流水接口尝试写请求 | 无任何写路由（404/405） | 手工 |
| CRD-005 | P1 | 补偿幂等 | 审核补偿同一记录两次 | 第一次进 granted 桶，第二次拒绝 | 自动：`TestModerationReviewConflictAndCompensation` |

## D. 订单与支付（ORD）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| ORD-001 | P0 | 充值到账 | 充值弹窗选档位下单 → 支付渠道完成支付 | 订单 paid，purchased 桶按实付 1:1 入账（附赠入 granted），流水 ref 为订单 | 手工（真渠道）/ 自动：`TestCallbackIdempotentAndAmountMismatch` |
| ORD-002 | P0 | 重复回调幂等 | 对同一订单重放回调 | 只到账一次，第二次仍返回渠道要求的成功响应 | 自动：同上 |
| ORD-003 | P0 | 验签拒绝 | 伪造签名回调 | 400 `INVALID_SIGNATURE` | 自动：同上 |
| ORD-004 | P0 | 金额篡改拒绝 | 回调金额与订单快照不一致 | 拒绝到账并记日志 | 自动：同上 |
| ORD-005 | P1 | 取消订单 | 取消待支付单；对已支付单取消；用他人订单 id 取消 | 待支付可取消；已支付 409 `ORDER_ALREADY_PAID`；他人 404 | 自动：`TestCancelOrder` |
| ORD-006 | P1 | 超时查单兜底 | 构造超过 30 分钟的待支付订单触发扫描 | 先向渠道主动查询，确认后置 paid/failed，不凭空失败 | 自动：`TestExpirePendingOrdersQueriesProviderFirst` |
| ORD-007 | P1 | 渠道可见性 | `EASYPAY_ENABLED=false` 时看充值弹窗与接口 | 易支付渠道不出现；开启后出现且跳转收银台 | 手工 |
| ORD-008 | P2 | 订单筛选 | admin 订单页按状态/渠道/用户筛选 | 结果正确 | 手工 |
| ORD-009 | P2 | 价格整分校验 | admin 创建非整分（非 10000 微元倍数）价格档位 | 拒绝保存 | 自动：`TestPackagePriceMustBeWholeCents` |

## E. 模型目录与报价（CAT）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| CAT-001 | P0 | 新模型上架即见 | admin 新增模型 → 主站模型广场/工作台选择器 | 立即出现且参数表单按 constraints 渲染 | 手工 |
| CAT-002 | P1 | 约束变化回落 | 去掉某模型的 quality 约束后切换到该模型 | 不再渲染质量选择器；非法已选值自动回落 | 手工 |
| CAT-003 | P0 | 价格矩阵校验 | 保存缺组合/重复组合/负价格/约束外取值的价格表 | 一律拒绝 | 手工 |
| CAT-004 | P1 | 折扣匹配 | 建多条重叠折扣，命中最具体者、按优先级取一条不叠加；制造冲突 | 冲突 409 `PROMOTION_CONFLICT` | 手工 |
| CAT-005 | P1 | 活动版本化 | 停用或修改活动 → 到 `nextPricingChangeAt` | 版本递增；到点后价格矩阵自动刷新 | 手工 |
| CAT-006 | P2 | 删除保护 | 对产生过流水的模型点删除；找充值档位的删除入口 | 只能下架不能删除；充值档位没有删除入口 | 手工 |
| CAT-007 | P0 | 报价正确性 | 逐组切换参数看报价金额；传不支持参数 | 价格随组合变化；不支持参数 4xx | 自动：`TestQuoteRejectsUnsupportedParams` 等 |
| CAT-008 | P1 | 目录加载失败恢复 | 断网/停后端后打开选择器再恢复 | 「模型加载失败 + 重试」可恢复；折叠态显示预估点数 | 手工 |

## F. AI 生成链路（GEN）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| GEN-001 | P0 | 图片生成计费全链 | 正常生成一张图，观察余额、生成记录、媒体 | 预扣 → 成功落账；产物进正式存储；生成记录 success | 自动：`TestImageGenerationReservesCreditsAndWritesGeneration` |
| GEN-002 | P0 | 失败退款 | 制造上游失败 | 点数原桶退回、免费试用计数退还；失败只退一次 | 自动：`TestImageGenerationRefundsOnUpstreamFailure` / `TestVideoTaskFailureRefundsOnce` |
| GEN-003 | P0 | 余额不足 | 清空余额后生成 | 明确错误码（402/403 口径以 T02 奇偶清单为准），余额与流水不变 | 自动：`TestImageGenerationInsufficientCredits` |
| GEN-004 | P1 | 幂等键语义 | 同 `idempotencyKey` 重复提交成功后的请求；再试运行中/失败请求；不带键提交 | 成功后 409 `QUOTE_STALE`（duplicate 标记）；运行中/失败 429 `CONCURRENCY_LIMITED`；缺键 400。注：回放历史结果（#15）仍为待办 | 自动：`TestImageGenerationIdempotencyAndMissingKey` |
| GEN-005 | P1 | 并发限流 | 多开生成超过每用户 30 次/分钟或全局 8 并发 | 429 带 `Retry-After` | 手工 |
| GEN-006 | P0 | 视频任务生命周期 | 发起视频生成 → 刷新页面 → 等待完成/失败 | 刷新后自动恢复轮询；任务失败「重试」重新发起而非查旧任务；`409 QUOTE_STALE` 不再出现 | 自动：`TestVideoTaskLifecycleAndRefund` |
| GEN-007 | P1 | 视频多任务隔离 | 同时生成两个视频 | 结果卡各自更新不互相覆盖 | 手工 |
| GEN-008 | P1 | 文本 SSE 流式 | 发起文本对话，观察流式输出 | 输出分块实时渲染不被缓冲（若被缓冲先查 sim 主站域 nginx 仓库外配置的 SSE location 是否已改 `/api/v1/ai/`） | 手工 |
| GEN-009 | P1 | 免费体验额度 | 领取 → 查看剩余次数 → 用超最便宜参数组合生成 → 并发提交 → 失败一次 | 领取成功显示剩余；超参走点数计费；并发不超发；失败退还试用计数 | 手工 |
| GEN-010 | P1 | 上游参数对齐（真渠道） | 逐项验证：文本 systemPrompt 生效、图像尺寸按分辨率+比例换算像素、视频「首尾帧/全能参考」按所选发出、Gemini 高质量 4K、带参考视频/音频的 Gemini 任务带上、语音语速音色生效 | 全部按界面所选发出 | 手工（真渠道） |
| GEN-011 | P2 | 生成历史分页 | 历史滚动到底自动加载；删除一条后继续翻页 | `nextCursor` 为 null 停止；无空洞 | 手工 |
| GEN-012 | P2 | 生成请求带 sessionId | 网络面板看四类生成请求 body | 带 `sessionId`，同标签页刷新不变、新标签页为新值 | 手工 |

## G. 媒体与存储（MED）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| MED-001 | P0 | 上传与访问 | 上传图片/视频/音频，用返回地址直开；同 storageKey 重传 | 可访问；不产生重复记录 | 自动：`TestMediaLocalUploadHeadGetDelete` |
| MED-002 | P1 | 上传边界 | 传超单文件上限文件；伪装成图片的非图片文件；带错误校验和 | 分别被拒（上限、`Content-Type 与文件内容不符` 400、校验和 4xx） | 自动：`TestModerationRejectsUploadWithoutQuota` 邻近用例 |
| MED-003 | P0 | 跨用户隔离 | 用户 B 直访用户 A 的媒体/画布/素材/生成记录 | 全部 404 | 自动：多包覆盖 |
| MED-004 | P0 | 下载两步流 | 付费档对免费期素材点下载，看网络面板 | 先 `POST /api/v1/media/{key}/download` 返回签名地址与 `expiresAt ≤ 300s`，取件为无水印原件，文件名 `ic-{对象id}.{扩展名}` | 手工 |
| MED-005 | P0 | 防盗链四连 | 签名 URL 过期后取件；篡改 token；发给另一登录账号取件；对他人素材调 `/download` | 全部 404 | 手工 |
| MED-006 | P1 | 下载限流 | 同一账号高频点下载 >60 次/小时 | 429，展示不受影响 | 手工 |
| MED-007 | P0 | 下载提示与防重（本批） | 三处下载按钮连点、断网触发失败 | 请求中 loading、不重复申请；失败提示为「错误码 → message → 兜底」三级文案 | 手工 |
| MED-008 | P1 | 分块上传配额复核 | 分块上传超额文件到完成时 | 完成后复核配额拒绝 | 手工 |
| MED-009 | P1 | 导出导入闭环 | 画布导出/素材导出 zip → 删除后导入 | 按 storageKey 逐个下载媒体；导入后图片不破图 | 手工 |
| MED-010 | P2 | 0 字节展示（本批） | 新账号查看 admin 用户列表存储配额列、个人中心用量 | 显示「0 B / 1.0 GB」而不是「/ 1.0 GB」 | 手工 |

## H. 水印与干净原件（WM，专库 `records/tests/infinite-canvas-media-watermark/`）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| WM-001 | P0 | flag=false 回归 | `WATERMARK_ENABLED=false` 走一遍生成/展示/下载/导出 | 与功能上线前完全一致 | 手工 |
| WM-002 | P0 | 免费链路烧录 | 免费账号生成图片/视频，看画布/工作台/素材页与下载 | 展示与下载均为平铺水印版；webp 源转 PNG 正常 | 手工 |
| WM-003 | P0 | 格式 fail-closed | 免费账号让上游返回 gif/webm | 生成失败并退还点数/试用次数，绝不下发干净件 | 自动：水印包注入测试 |
| WM-004 | P0 | 付费与升级回溯 | 付费生成无水印；免费期素材升级付费后刷新变干净；降级回水印版 | 全部符合 | 手工 |
| WM-005 | P0 | 隔离件释放——上传件不烧录（本批 E-4） | 上传被拒 → admin 人工释放（免费档物主、水印开启） | 释放为原始字节，不烧水印、不写 orig | 自动：`TestReleaseUploadArtifactNeverWatermarked` |
| WM-006 | P0 | 隔离件释放——免费档生成件烧录（本批 E-4） | 生成被拒 → admin 释放免费档物主 | 释放为水印版并留存干净原件（升级后可下发干净版） | 自动：`TestReleaseGenerationArtifactWatermarkAttribution` |
| WM-007 | P1 | 隔离件释放——付费档直落（本批 E-4） | 生成被拒 → admin 释放付费档物主 | 直接落原始字节、不写 orig | 自动：同上（付费段） |
| WM-008 | P1 | 隔离件释放——水印失败 fail-closed（本批 E-4） | 水印服务不可用时释放生成件 | 释放失败、隔离原件保留、无媒体行；恢复后重试释放成功 | 自动：`TestReleaseGenerationArtifactFailsClosedOnWatermarkError` |
| WM-009 | P1 | 社区语义 | 免费作者发布作品，另一账号浏览 | 浏览者看到水印版；付费作者作品为干净版 | 手工 |
| WM-010 | P1 | orig 清理与限流 | 删除素材/注销后核对 `{uid}/orig/`；高频下载 | orig 同步删除；>60 次/小时被限流 | 手工 |

## I. 内容审核与隔离（REV）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| REV-001 | P0 | 提示词预审 | 开审核（fake provider）提交含违禁词提示词 | 422 `CONTENT_REJECTED`；不预扣、无消费流水、无生成记录；记录只存哈希与标签不存原文 | 自动：`TestModerationRejectsPromptBeforeReserve` |
| REV-002 | P0 | 产物后审拒绝 | 上游产物被拒 | 422；不进正式存储、点数不退（上游成本已发生）；生成记录 rejected；隔离原件保留 24h | 自动：`TestModerationArtifactRejectedKeepsCredits` |
| REV-003 | P0 | 人工复核 | admin 复核通过/拒绝；两管理员用旧 revision 提交 | 通过→释放进正式存储；旧 revision 409 `MODERATION_ALREADY_REVIEWED` | 自动：`TestModerationReviewConflictAndCompensation` |
| REV-004 | P1 | fail-mode 语义 | 审核服务故障下分别用 reject/allow 模式 | reject 503 `MODERATION_UNAVAILABLE` 不预扣；allow 记 error 放行；正式环境 `MODERATION_ENABLED=true`+fake 拒绝启动 | 自动：`TestModerationFailModeRejectAndAllow` |
| REV-005 | P1 | 视频审核终态 | 开审核后视频任务跑到终态 | succeeded/failed 不再永久卡 moderating；被拒视频原件可预览可释放；帧数密度/上限可配且生效 | 自动：视频审核用例 |
| REV-006 | P1 | 隔离区统计 | 查看隔离区用量统计 | 字节数 = 真实加密对象大小（非键长×条数） | 自动：`SetQuarantineBytes` 相关 |
| REV-007 | P2 | 语音占位审核 | 生成语音 | provider=none 的审核记录、正常可用 | 手工 |
| REV-008 | P1 | 上传/生成来源可区分（本批） | admin 审核列表分别产生上传被拒与生成被拒记录 | 上传记录 stage=upload、生成记录 stage=artifact，可按阶段筛选 | 自动：`TestModerationRejectsUploadWithoutQuota` |

## J. 画布（CAN）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| CAN-001 | P0 | 画布 CRUD 与列表 | 新建/重命名/删除；列表翻页、关键字、排序 | 全部服务端完成，网络面板无一次性全量拉取；卡片「N 个节点 · N 条连线」与实际一致 | 手工 |
| CAN-002 | P0 | 自动保存 | 编辑停手约 2 秒 / 持续拖动超 30 秒 / 断网保存 | 顶栏「已保存」；停手 2s 或最长 30s 各保存一次；断网有提示可重试 | 手工 |
| CAN-003 | P1 | 双端冲突 | 两台设备同开一画布先后保存 | 后保存方弹冲突框；「用我的版本覆盖」「重新加载」行为正确；重命名不触发冲突 | 手工 |
| CAN-004 | P0 | WebP 预览渲染（本批） | 打开含大图的画布，未放大时平移；再放大单个节点超过 768px 当量 | 平移顺滑（渲染预览）；放大后自动回退原图不发虚；无预览时直接原图 | 手工 |
| CAN-005 | P1 | 预览生成与清理（本批） | 上传图片 → 看IndexedDB `image_previews`；打开历史画布；删除素材 | 上传时即生成预览；历史图片首次打开后台补生成；删除素材后对应预览被清理 | 手工 |
| CAN-006 | P1 | 多图生成 | 数量 >1 生成图片：槽位/主图切换/失败槽位重试删除/展开收起/创建副本 | 按 pending-test 多图节逐条符合 | 手工 |
| CAN-007 | P1 | 打组与组引用 | 多选打组/解散组（含快捷键）；组连到生成节点；拖组不触发高亮 | 组引用展开组内资源、不重复发送；拖拽合批跟手、落点与预览一致（upstream） | 手工 |
| CAN-008 | P1 | 连线视口裁剪（upstream） | 缩放/平移到只露部分画布 | 穿过视口的连线全部渲染、可点选可右键；虚线预览跟光标 | 手工 |
| CAN-009 | P1 | 悬停按钮（upstream） | 移入/移出图片与文本节点 | 下载与「设为主文本」按钮悬停才显示，不遮挡内容 | 手工 |
| CAN-010 | P1 | 视频截帧 | 右键有内容视频节点截首帧/尾帧/当前帧 | 按原始比例生成图片节点并连线；「当前帧」用此刻画面且不改变播放位置 | 手工 |
| CAN-011 | P2 | 遮罩编辑 | 涂抹 + 修改要求 → 导出到画布 / 立刻生成 | 遮罩标注节点颜色透明度一致；请求按两张参考图发送；结果只改标注区无蓝色残留 | 手工 |
| CAN-012 | P2 | 选择/移动模式与快捷键 | 模式切换、Ctrl/空格临时反转、中键平移、Shift 追加选择 | 按 pending-test 画布选择节符合 | 手工 |
| CAN-013 | P1 | 导出导入 | 导出画布 → 删除 → 导入 | 媒体逐个下载重传，导入后不破图 | 手工 |
| CAN-014 | P1 | 文本生成与主文本 | 多文本生成数量 >1；「设为主文本」来回切换 | 单结果节点展开备选；切换保留原主文本为备选；双击编辑后切换不丢 | 手工 |
| CAN-015 | P1 | Agent 写回与引用 | Agent 写回提示词；提示词仅含 `@[node:...]` 引用 | 输入框即时同步；纯引用直接连线不重复建提示词节点 | 手工 |
| CAN-016 | P2 | 缩放稳定性 | 导入图片立即从角落缩放 | 面板暂隐、平滑无闪烁、无 `Maximum update depth exceeded` | 手工 |

## K. 素材（AST）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| AST-001 | P0 | 列表筛选 | 关键字（含正文关键词）/类型/标签多选（同时包含） | 全部服务端命中；第一页响应带标签候选 | 手工 |
| AST-002 | P0 | 下载 | 图片/视频下载；标题含中文、空格、「【】」 | 文件可保存且沿用原标题命名 | 手工 |
| AST-003 | P0 | 存为素材带 AIGC 标识（本批） | 工作台结果点「存为素材」→ 查素材详情/再发布 | 素材 `isAIGC=true`（手动上传为 false） | 自动：`TestCommunityWorkCarriesAIGCTag` |
| AST-004 | P1 | 删除联动（本批） | 删除素材后查本地 IndexedDB `image_previews` 与服务端 | 本地预览被清理；服务端软删，引用它的作品退出公开流 | 手工 |
| AST-005 | P2 | 导出导入 | 导出 zip → 清空 → 导入 | 素材与媒体恢复 | 手工 |
| AST-006 | P1 | 素材选择弹窗与画布侧栏 | 画布侧栏/素材弹窗/Agent `assets_list` 搜索 | 同走服务端筛选 | 手工 |
| AST-007 | P2 | 删除后历史引用 | 删除素材后看生成历史/画布引用处缩略图 | 历史缩略图仍显示（历史清理语义） | 手工 |

## L. 社区（COM）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| COM-001 | P0 | 发布 | 素材页「发布到社区」填表提交 | 列表立即可见；字段与素材一致 | 自动：`TestCommunityPublishLikeReportAndAdminRemove` |
| COM-002 | P0 | 频控与去重 | 一天发第 21 篇；同素材同标题当日重复发布 | 429；409 | 自动：同上 |
| COM-003 | P0 | 跨用户浏览 | 用户 B 浏览用户 A 已发布作品图片/视频 | 正常显示（未发布/已下架 404） | 手工 |
| COM-004 | P0 | 点赞幂等 | 点赞两次、取消 | 计数正确、详情弹窗即时回写 | 自动：同上 |
| COM-005 | P1 | 举报与下架 | 举报 → 重复举报 → admin 采纳下架 | 重复 409；下架后公开流消失、源素材软删也退出公开流 | 自动：同上 |
| COM-006 | P0 | AIGC 标签（本批） | 用 `isAIGC=true` 素材发布 → 看卡片与详情；用上传素材发布对比 | 「AI 生成」蓝色标签只出现在前者 | 自动：`TestCommunityWorkCarriesAIGCTag` |
| COM-007 | P0 | 复刻完整链路（本批） | 图片作品详情点「以此作品为素材创作」→ 工作台生成 → 存为素材 → 发布 | 工作台自动加载原作为参考图并提示；发布弹窗显示「复刻自《原作》」且随发布落 `sourceWorkId`；原作 remix 计数 +1 | 手工 |
| COM-008 | P1 | 复刻来源可取消（本批） | 发布弹窗点复刻标签的取消按钮 | 标签移除，发布后无 sourceWorkId | 手工 |
| COM-009 | P2 | 视频作品复刻（本批） | 视频作品点复刻 | 保留复制原作链接 + 跳视频工作台的旧闭环 | 手工 |
| COM-010 | P1 | 我的作品与作者下架 | 「我的作品」列表与下架操作 | 列表正确、下架后公开流消失 | 手工 |
| COM-011 | P2 | 发布路由限流 | 1 小时内连续发布 >12 篇 | 429 | 手工 |
| COM-012 | P2 | 用户主页 | 点作者头像进入 `/community/users/:id` | 作品与统计正确 | 手工 |

## M. 活动与运营（ACT）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| ACT-001 | P1 | 每日签到 | 签到 → 再签 | 首次领取奖励、重复签到拒绝 | 手工 |
| ACT-002 | P1 | 邀请绑定 | 新用户注册 1 小时内绑定邀请码；未验证邮箱账号绑定 | 成功绑定返利；越时/未验证给具体原因 | 手工 |
| ACT-003 | P2 | 站点公告 | admin 设置公告 → 主站查看 | 经 `/api/v1/settings/public` 下发展示 | 手工 |

## N. 管理后台（ADM）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| ADM-001 | P0 | 独立站登录与会话 | 打开 sim-admin 登录 → F5 硬刷新 → 登出 | 独立登录页；硬刷新保持登录（真浏览器复核，自动化环境曾现 cookie 怪癖） | 手工 |
| ADM-002 | P0 | 非管理员落说明页 | 用普通账号登录 admin 域 | 「当前账号没有后台访问权限」说明页且不被登出（回主站仍是登录态） | 手工 |
| ADM-003 | P0 | 角色与权限 | 建只授 `users.read` 的角色 → 该角色登录 → 直输 `/admin/models` | 菜单只剩用户项；直输落无权限页且接口 403 不重试；系统角色不可改删、不能改自己的角色、有成员的角色不能删 | 自动：受限角色过滤 + 手工 |
| ADM-004 | P0 | 用户管理 | 筛选（邮箱/状态/档位）、封禁、解封、重置密码 | 封禁后 ≤15 分钟完全掉线且 `ic_media` 失效；重置密码踢全设备并置强制改密 | 手工 |
| ADM-005 | P1 | 建号 | admin 建号 → 查看临时密码展示与审计 | 明文只出现一次；账号置 `must_change_password` 且邮箱视为已验证 | 手工 |
| ADM-006 | P0 | 配额列 0 值（本批） | 查看已用量为 0 的用户列表与详情抽屉 | 「0 B / 1.0 GB」与「0 B / 200 MB」 | 手工 |
| ADM-007 | P1 | SSO 接入管理 | 新增客户端 → 一次性密钥展示 → 编辑 → 重置密钥 → 删除 | 密钥仅创建与重置时展示一次 | 手工 |
| ADM-008 | P1 | 审核工作台 | 列表筛选（阶段/结论/标签/用户）、详情预览隔离件、复核、补偿 | 全部可用；补偿同记录只一次 | 自动：`TestModerationReviewConflictAndCompensation` + 手工 |
| ADM-009 | P1 | 模型能力分区 | 能力筛选计数、编辑图片/视频模型的分组约束、自定义约束保留、新建视频模型价格矩阵 | 按 pending-test「多产品后台」节符合 | 手工 |
| ADM-010 | P1 | 多产品菜单 | 侧边栏两层菜单、博客/办公套件占位页、受限角色模块过滤 | 占位页说明接入条件、不渲染菜单 | 手工 |
| ADM-011 | P1 | 用量分析 | 打开用量分析页切 7/30/90 天 | KPI/趋势（缺数补零）/分布/排行正确；`days` 非法值 400 | 手工 |
| ADM-012 | P2 | 环境与入口 | 主域访问 `/admin`；正式环境角标 | 主域真 404（nginx 已应用）；正式环境红色角标 | 手工 |
| ADM-013 | P1 | 审计 | 抽查用户/模型/设置/SSO 操作后的审计日志 | 记录操作者、动作、before/after | 手工 |

## O. OIDC 与承接页（OID）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| OID-001 | P0 | authorize 安全口径 | 缺 code_challenge / plain method / 非法 redirect_uri / scope 无 openid | 一律 400 JSON 拒绝，绝不重定向 | 自动：`TestOIDCAuthorizeRejects` |
| OID-002 | P0 | 授权码一次性 | 同一 code 换 token 两次 | 第二次失败 | 自动：`TestOIDCFullChain` |
| OID-003 | P0 | token 签发 | form 与 JSON 两种 body 换 token | RS256 access_token 15 分钟 + id_token；client_secret 恒时比对 | 自动：`TestOIDCTokenAcceptsJSON` |
| OID-004 | P1 | userinfo 边界 | 用平台会话令牌调 userinfo | 401（两套凭据互不相认） | 自动：`TestOIDCUserinfoRejectsInvalidTokens` |
| OID-005 | P1 | jwks | `GET /api/v1/oidc/jwks.json` | 返回公钥集 | 自动：`TestOIDCFullChain` |
| OID-006 | P0 | 端点限流（本批） | 10 分钟内对 authorize/token 各打第 21 次 | 429 `RATE_LIMITED` + `Retry-After` | 手工 |
| OID-007 | P0 | 承接页直跳（本批） | 浏览器访问 `/oauth/authorize?client_id=...&redirect_uri=...&response_type=code&scope=openid&state=st&code_challenge=...&code_challenge_method=S256`（已登录） | 显示「正在为你授权并跳转…」后自动跳第三方，redirect 带 code 与 state；默认 302 口径不变 | 自动：`TestOIDCAuthorizeJSONModeForRelayPage` |
| OID-008 | P1 | 承接页登录衔接（本批） | 未登录走同一 URL → 登录 | 先到登录页，登录后回到承接页继续完成跳转 | 手工 |
| OID-009 | P2 | 承接页错误态（本批） | 用未注册的 redirect_uri 走承接页 | 显示授权失败原因与重试按钮 | 手工 |

## P. 工作台（STU）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| STU-001 | P0 | 图片工作台生成 | 多张并发生成、结果卡下载/存为素材/加参考 | 各卡独立更新；下载与存素材正常（isAIGC=true） | 手工 |
| STU-002 | P0 | 视频工作台恢复 | 发起视频 → 刷新页面 | 自动恢复轮询至终态 | 手工 |
| STU-003 | P1 | 历史与删除 | 滚动加载历史、删除失败提示 | 游标分页无空洞；删除失败有 toast | 手工 |
| STU-004 | P1 | 复刻参数读取（本批） | 从社区复刻进入 `/image?remix=<id>` | 参考图自动加载、URL 参数被清除（刷新不重复加载） | 手工 |
| STU-005 | P2 | 剪贴板与拖拽参考 | 截图后粘贴、拖文件进工作台 | 参考图加入且数量上限生效 | 手工 |

## Q. Canvas Agent（AGT，详见 pending-test Agent 节既有清单）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| AGT-001 | P1 | 对话与历史一致性 | 流式回复、刷新恢复、多标签隔离 | 按 pending-test「Agent 消息元数据」条目 | 手工 |
| AGT-002 | P1 | 凭据脱敏 | 让 CLI stderr 携带 token/key 场景复现 | 调试日志与诊断事件无明文凭据 | 手工 |
| AGT-003 | P2 | 引用与 Skill | `/` 选 Skill、`@` 引用画布素材、`@[node:]` 直连 | 标签插入、预览、刷新后保持 | 手工 |
| AGT-004 | P1 | 画布素材工具 | Agent 搜索/上传/引用素材 | 走服务端筛选与媒体接口 | 手工 |

## R. 部署与运维（DEP）

| 编号 | 优先级 | 用例 | 步骤 | 预期 | 自动化 |
| --- | --- | --- | --- | --- | --- |
| DEP-001 | P0 | `/api/v1` 全链路回归 | 部署后过一遍登录/画布/素材/生成/媒体/订单/个人中心 | 前端无感知；外部直调旧路径的脚本需自行更新 | 手工 |
| DEP-002 | P0 | 仓库外 nginx SSE | 检查 sim 主站域反代（`sub2api-http.conf`）的 SSE location | 改为 `/api/v1/ai/`（含 proxy_buffering/gzip 豁免），否则对话流式被缓冲 | 手工 |
| DEP-003 | P0 | 新列真库迁移（本批） | 部署含 `assets.is_aigc` 的版本到 sim，观察 api 启动与 `information_schema.columns` | AutoMigrate 正常、只有新列添加、无类型漂移 | 手工 |
| DEP-004 | P1 | D9 告警邮件（本批） | 制造存储记账漂移并跑对账任务 | admin 角色邮箱收到告警邮件；无漂移不发信；`MAIL_DRIVER=log` 时在 api 日志可见 | 手工 |
| DEP-005 | P1 | 维护模式 | 站点设置开启维护模式 | 普通用户写 503 `MAINTENANCE_MODE`、读正常、管理员不受限、支付回调豁免 | 手工 |
| DEP-006 | P1 | 环境标识 | 本地/`SITE_ENV=test`/production 三态看顶栏 | 分别显示「本地开发」「测试环境」/无标识 | 手工 |
| DEP-007 | P1 | 备份恢复 | `scripts/backup.sh` + `restore.sh` 演练一次 | 库+媒体+env 三件备份并可复原 | 手工 |
| DEP-008 | P2 | 迁移留痕 | 启动后查 `schema_migrations` | 版本行写入 | 手工 |
| DEP-009 | P1 | 水印部署前提 | 开 flag 且字体就位/缺字体各启动一次 | 就位正常启动；缺字体 fail-fast 拒绝启动 | 手工 |
| DEP-010 | P2 | 测试数据 seed | `SEED_TEST_DATA=true` 启动两次 | 首次写入测试账号/画布/素材，第二次幂等不重复 | 手工 |
