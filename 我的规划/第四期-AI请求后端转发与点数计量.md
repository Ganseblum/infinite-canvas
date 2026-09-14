---
title: 第四期执行计划：AI 请求后端转发与点数计量
description: 账号体系与后端服务规划第四期的中文执行计划，涵盖 AI 请求后端转发、点数一致性、直连路径移除、偏离纠正与验收
---

# 第四期执行计划：AI 请求后端转发与点数计量

本文档是[账号后端总规划](账号后端总规划.md)第四期的中文执行计划。前置条件是[第三期](第三期-点数计费与管理后台.md)已交付并通过验收，`model_catalog` 与双桶点数流水已经可用。

## 范围界定

### 本期要做

图像、视频、语音、文本四类 AI 请求的后端转发接口——这也是全站**唯一**的 AI 调用路径——按 `model_catalog.credit_cost` 的先扣后生成与失败退还，文本对话的 SSE 流式透传，平台渠道 Key 的加密托管与故障转移，按 `model_catalog.constraints` 的参数校验，生成结果的服务端转存，单用户并发限制，以及前端生成入口从直连实现整体切换到 `/api/ai/*`。

### 本期不做

**不保留用户自定义渠道的任何形态。** 用户自带 Key 的浏览器直连路径随本期一起移除：`image.ts`、`video.ts`、`audio.ts` 里的直连协议实现、渠道编解码与配置界面全部删除，理由见下面的「为什么不允许用户自带 Key」。

**本期不新增支付相关改动，充值链路已经在第三期交付。** `GET /api/credit-packages`、`POST /api/orders`、`POST /api/orders/{id}/cancel` 与 `POST /api/payments/webhook/{provider}` 连同前端的充值页都已经可用，本期既不改这几个接口，也不新增充值入口，只是让第三期建好的账本真正开始被消费。

**用户自定义脚本一并移除。** `model-plugin.ts` 允许用户为任意模型写自定义调用方式，它依赖用户自配的 Key 与 baseUrl，随自定义渠道一起删除，见下面的「自定义脚本整体移除」一节。

不做多实例部署下的分布式并发计数与任务调度。本期的并发槽位和视频任务轮询都在单进程内完成，沿用第一期「进程内计数，横向扩展时再换 Redis」的结论。

## 阶段目标与完成定义

本期目标是把“生成产生真实上游成本”这条链路收到服务端，并使报价、免费额度、扣点、退还、结果落盘和生成记录成为一个可追踪闭环。同时把前端收敛到单一调用路径，删除直连与自定义渠道的全部残留。

| 目标 | 完成定义 | 主要证据 |
| --- | --- | --- |
| 平台密钥隔离 | 平台 Key 只在 provider 发请求瞬间解密，浏览器、日志和错误响应不可见 | 网络抓包、日志扫描、管理接口检查 |
| 统一报价计量 | `/api/ai/quote` 与实际请求调用同一基础价格和折扣解析器，免费额度、原价、折后价和实扣一致 | 参数矩阵、活动时间边界测试与账本对账 |
| 扣点可恢复 | 跨桶扣减原子完成，失败按原桶退还且同一消费只退一次 | 故障注入与逐桶 SQL 对账 |
| 多能力可用 | 图片、视频、语音、对话分别按同步、异步或 SSE 特性正确处理 | 四类端到端用例 |
| 单一路径 | 全部生成请求只指向本站 `/api/ai/*`，浏览器不存在任何上游直连与 API Key | 浏览器网络面板与全局代码搜索 |
| 结果服务端落地 | 平台产物不依赖临时第三方 URL，生成记录由服务端写入 | URL 过期与页面重开测试 |

## 实施任务与顺序

| 顺序 | 任务 | 主要文件 | 具体改动 | 任务完成检查 |
| --- | --- | --- | --- | --- |
| 1 | 固定目录与报价 | model/promotion service、`/api/models`、`/api/ai/quote`、前端动态表单 | 能力、约束、基础价格、折扣优先级、活动时钟和免费资格共用服务端判定 | 参数矩阵、活动重叠与时间边界测试通过 |
| 2 | 完成平台渠道托管 | `platform_channels`、admin channel handler、`internal/provider/` | 加密 Key、优先级、健康状态、协议适配 | 管理接口不返回明文，错误日志不泄密 |
| 3 | 跑通图片纵向切片 | image handler、upstream service、credit service、media/generation service、前端 image 入口切换 | 校验、报价、额度或扣点、上游、落盘、记录、失败处理 | 成功扣点一次，失败原桶退还一次 |
| 4 | 扩展异步视频 | video task model/service/handler、轮询恢复 | 创建时扣点和写 pending，后台完成落盘，失败幂等退还 | 关闭页面后任务仍能完成或正确失败 |
| 5 | 扩展语音与流式对话 | audio/chat handler、SSE 转发、nginx | 固定价、首字节失败退款、流开始后不退款、取消传播和心跳 | nginx 后逐字输出，停止后槽位释放 |
| 6 | 完成前端单路径切换 | `web/src/services/api/ai.ts`、既有 image/video/audio、模型选择器与配置页 | 全部生成入口调本站；删除直连实现、渠道配置与自定义脚本 | 网络面板无第三方直连，全局搜索无渠道残留 |
| 7 | 故障注入与对账 | 后端测试、验收脚本、监控配置 | 覆盖超时、5xx、断连、重复提交、存储失败、并发上限 | 两桶余额与流水逐桶相等，无重复结果 |

## 阶段检查与纠偏

| 偏离信号 | 说明 | 立即纠偏 |
| --- | --- | --- |
| 报价和实扣各有一套价格或折扣计算 | 用户会看到 A、被扣 B | 收口为同一解析器，先补基础价、折扣优先级和时间边界测试再恢复联调 |
| 用户自带 Key、自定义渠道或脚本重新出现 | 直连路径不可计量也不可审核，破坏单一计费口径 | 删除相关代码与入口；确有需求时回到总规划重新决策，不做中间形态 |
| 上游调用早于额度/余额预留 | 成功生成可能无法扣费 | 调整顺序为校验、预留、上游；失败走统一 Refund |
| 退款直接改余额或退到错误桶 | 账本无法解释 | 只允许 credit service 按消费流水逐桶冲正 |
| 前端写生成记录 | 刷新或重试会重复记账 | 生成记录只由服务端创建，前端只查询和展示 |
| 平台响应透传第三方 URL 或原始体 | 协议和临时链接泄漏到前端 | provider 归一化并由服务端下载落盘，只返回 `storageKey` |
| SSE 直连正常、nginx 后整段返回 | 反代缓冲未关闭 | 暂停对话验收，修 nginx 并只在完整链路复验 |
| 为多实例提前上队列和分布式锁 | 超出当前单实例范围 | 回退进程内任务和槽位，记录扩容触发指标 |

出现账本偏离时必须立即关闭平台模型入口和免费额度开关，保留数据查询与导出；先按 `ref_id` 对账、修复和复验，再恢复生成流量。不能用手工改余额掩盖差异。

## 阶段门禁

- 自动化：provider 契约、参数校验、报价、双桶 Reserve/Refund、幂等、视频任务和 SSE 生命周期测试通过。
- 对账：对任意用户逐桶验证 `credits.*_micros = SUM(credit_transactions.amount_micros)`；每个消费最多一个对应退款。
- 安全：平台 Key 不进浏览器、日志、生成记录或错误；结果 URL 下载阻断回环、私网和重定向绕过。
- 完整链路：流式验收必须经过 nginx；视频必须验证页面关闭与服务重启后的恢复策略。
- 产品门禁：Phase 4 与 Phase 5 未同时通过前，不对外开放充值和公开注册。

## 为什么必须做这一期

第三期引入了点数收费，但点数目前扣不准，因为生成行为压根不经过本项目的服务端。这件事在早期方案里是被当成「软约束」接受的：浏览器直连用户自己配置的第三方渠道，服务端只能依赖前端在生成完成后主动上报一条生成记录来计数，改几行前端代码就能绕过。当计数只用于「每天最多生成 50 张」这类礼貌性限制时，软约束是可接受的；一旦点数变成要花钱买的东西，软约束就等于卖一个无法计量的商品——用户付了钱，我们不知道他用了多少；用户没付钱，我们也拦不住他用。

**要让点数变成硬约束，唯一的办法是让密钥只存在于服务端。** 只要浏览器持有可用的 API Key，任何前端侧的计费都只是提示。这不是补丁能解决的问题，只能改架构。本期把这条结论用在全部生成链路上：平台渠道的 Key 从头到尾不出服务端，浏览器里不再存在任何可用的 API Key，点数因此成为硬约束。

**本期的另一项收尾是删除用户自带 Key 的直连路径，包括它背后的凭据托管设想。** 早期方案曾让 `GET /api/credentials` 返回明文 Key 供前端直连，那意味着任何拿到 access token 的人都能读走该用户的全部厂商密钥，只能靠「令牌不落 localStorage」一条防线硬扛。现在那条路径不复存在，这个暴露面被整体消灭：服务端只保管平台自己的渠道密钥，「access token 只存内存、refresh token 走 httpOnly cookie」的令牌策略继续执行，但不再需要承担「防止用户密钥被读走」的全部压力。

还有一项顺带的收益，这次是完整的。**全部生成链路上的错误处理和厂商差异都退到了服务端**：请求失败统一按第一期的 `error.code` 约定返回，前端不用再靠猜 HTTP 状态码、翻找 `msg` / `message` / `error.message` / `detail` 拼文案，Gemini 与 Seedance 那类协议适配也由服务端的 provider 承担。`image.ts` 里的 Gemini 分支、`video.ts` 里的 Seedance 分支、以及三个文件里各自那份 `readApiErrorMessage` 与 `readAxiosError` 在本期**整体删除**——没有直连路径需要它们兜底。

## 核心商业规则

### 只有平台一条路径

这条规则是整个收费模型的地基，实现前必须先对齐：**不存在用户自定义渠道，不存在浏览器直连上游，所有生成都走 `model_catalog` 与服务端托管的平台渠道。**

| 维度 | 唯一路径（平台渠道） |
| --- | --- |
| 请求路径 | 浏览器 → 本站后端 → 上游 |
| 模型来源 | 第三期的 `model_catalog` 表，也是前端唯一能选到的模型列表 |
| 用谁的 Key | 平台自己的渠道 Key，加密存在服务端，浏览器拿不到 |
| 上游成本承担方 | 平台 |
| 是否扣点 | 按 `model_catalog.credit_cost` 扣，或消耗免费额度 |
| 参数校验 | 按 `model_catalog.constraints` 白名单严格校验 |
| 自定义脚本 | 不存在 |
| 存储配额与保留期 | 受约束 |
| 并发与频次限流 | 服务端在 `/api/ai/*` 上强制 |

**为什么不再允许用户自带 Key。** 早期方案曾保留「用户自配渠道浏览器直连、不扣点」的第二路径，结论是弊大于利：直连请求服务端看不见，点数与用量的口径在两条链路之间永远说不清；服务端看不到的生成无法审核，与第五期的合规目标直接冲突；同时维护两套前端调用、两套错误处理、两套参数约束的复杂度远超收益，还伴随「托管明文 Key」这个安全暴露面。愿意自带 Key 的折腾型用户不再是本产品的服务对象——这是产品边界的收缩，不是实现上的妥协。

**服务端不替用户转发自填 `baseUrl` 的请求**在旧方案里是拒绝用户渠道走后端的理由，现在这个理由反过来成了删除用户渠道的理由之一：只要不存在用户自填的 `baseUrl`，服务端主动出站的目标就只剩平台自己配置的上游和上游返回的结果 URL，SSRF 防护面收敛到最小。

**模型选择器只有平台目录。** 不存在「平台模型与用户模型并列展示」「余额不足自动降级到用户渠道」这些设计——用户按下生成之前不需要判断这一次走哪条路，因为只有一条路。

### 后端仍做防御性校验

**`/api/ai/*` 只认 `model_catalog` 里的裸模型名。** 四种情况一律返回 400 `MODEL_NOT_SUPPORTED`：模型不在目录里、`enabled` 为假、能力与接口不匹配（例如拿一个视频模型去调 `/api/ai/images/generations`）、以及任何带自定义渠道前缀痕迹的标识。

不能因为「前端只会发合法请求」就省掉这道校验。接口是公开的，任何人都可以拿着自己的 access token 直接往 `/api/ai/images/generations` 里塞任意字符串。后端如果不校验而是把整串当模型名去查目录或拼上游请求，最好的结果是查不到，最坏的结果是撞上一个同名的平台模型，用平台的 Key 白生成一次。

**响应里没有 `source` 字段。** 全部请求都走同一条路径、都按同一种规则计费，一个恒为 `platform` 的字段只会让前端多一个永远走不进去的分支。

## 统一约定

### 新增错误码

沿用第一期的响应格式，失败一律包一层 `error`，前端优先按 `code` 查本地化文案。本期新增或首次启用的错误码：

| HTTP | code | 触发场景 |
| --- | --- | --- |
| 400 | `MODEL_NOT_SUPPORTED` | 模型不在 `model_catalog` 中、已禁用，或能力与接口不匹配 |
| 400 | `PARAM_NOT_SUPPORTED` | 参数不在该模型 `constraints` 允许的范围内，`error` 里带 `param` 与 `allowed` |
| 402 | `INSUFFICIENT_CREDITS` | 点数不足，带 `requiredMicros`、`availableMicros` 与 `shortfallMicros` |
| 409 | `QUOTE_STALE` | 报价过期、参数不匹配、价格版本变化或免费资格已被占用，必须重新报价 |
| 429 | `CONCURRENCY_LIMITED` | 同一用户进行中的生成请求数超过上限，带 `Retry-After` |
| 502 | `UPSTREAM_ERROR` | 上游返回非 2xx 或响应无法解析，`error` 里带 `upstreamStatus` |
| 504 | `UPSTREAM_TIMEOUT` | 上游超时，`error` 里带 `phase` 说明是连接、首字节还是整体超时 |

复用第一期与第三期已有的：401 `UNAUTHORIZED` 与 `TOKEN_EXPIRED`、403 `EMAIL_NOT_VERIFIED`、429 `RATE_LIMITED`、507 `STORAGE_QUOTA_EXCEEDED`、500 `INTERNAL_ERROR`。

`PARAM_NOT_SUPPORTED` 与 `VALIDATION_FAILED` 的分工要分清：类型错误、缺必填、数值越界这类「怎么看都不合法」的用 `VALIDATION_FAILED`；参数本身合法、只是这个模型不支持的用 `PARAM_NOT_SUPPORTED`。前者是前端的 bug，后者是用户选错了，两者的提示文案完全不同。

`EMAIL_NOT_VERIFIED` 与 `STORAGE_QUOTA_EXCEEDED` 都必须在预扣之前判定。生成产物一定要落到媒体存储，如果生成后才发现存不下，用户既拿不到结果又白扣点数。

### 请求与响应的通用形状

六个接口全部需要登录，全部挂在 `/api/ai` 下，全部由第一期的 `RequireAuth` 中间件保护，并且只服务平台目录里的模型。

请求体统一带 `model`、`quoteToken` 与 `idempotencyKey`。报价凭证固定模型、参数哈希、计费模式、原价、折后价、基础价格版本、促销版本和全局促销开关状态；幂等 key 避免重试和双击重复预扣。**四个生成接口（图片、视频、语音、对话）的 `idempotencyKey` 一律必填**，由 `ai.ts` 统一生成 UUID，调用方不感知——花钱的接口不能容忍「响应丢失后重试变成两次真实扣费」，必填的成本只是客户端一个 UUID。

响应统一包含计费快照与产物。计费快照固定返回 `baseCostMicros`、`finalCostMicros`、`finalCostYuan`、命中的折扣或 null、`remainingMicros`；产物一律以 `storageKey` 形式给出。不透传上游原始响应体。

## 接口定义

### POST `/api/ai/quote`

请求带模型、能力和本次完整参数，不接受客户端计算的价格：

```json
{
  "model": "video-model-a",
  "capability": "video",
  "params": { "resolution": "720p", "duration": 5, "ratio": "16:9" }
}
```

成功返回：

```json
{
  "billingMode": "credits",
  "baseCostMicros": 1000000,
  "discount": { "promotionId": "promo-1", "name": "720p 限时 8 折", "discountBps": 8000, "endsAt": "..." },
  "finalCostMicros": 800000,
  "finalCostPoints": 800000,
  "finalCostYuan": "0.80",
  "availableMicros": 1500000,
  "affordable": true,
  "shortfallMicros": 0,
  "priceVersion": 3,
  "promotionVersion": 2,
  "quoteToken": "...",
  "expiresAt": "..."
}
```

未命中活动时 `discount` 为 null，`finalCostMicros` 等于原价。免费额度适用时 `billingMode` 为 `free_trial`、最终金额为 0，并返回对应剩余次数；免费额度优先于折扣，折扣只在实际走点数分支时生效。报价是纯查询，不写流水、不占余额、不消耗次数。

`quoteToken` 由服务端 HMAC 签名，绑定原价、折后价、基础价格版本、活动 id、活动版本、全局促销开关版本和参数哈希。普通报价最多有效 5 分钟；命中活动时 `expiresAt` 不得晚于活动 `endsAt`。生成时重新解析当前活动；活动开始、停用、修改、结束、全局开关变化或被更具体规则替代后，旧 token 返回 `QUOTE_STALE`。新活动刚开始导致价格降低时也必须重新报价，不能按旧原价多扣。

生成接口收到 token 后按固定顺序执行：鉴权与参数校验、验证报价和请求哈希、检查邮箱及存储、原子占用免费次数或按报价预扣双桶点数、写 `ai_requests`，最后请求上游。余额不足返回 `INSUFFICIENT_CREDITS`，报价失效返回 `QUOTE_STALE`，两者都不得调用上游。免费资格在报价后被另一请求占用时返回 `QUOTE_STALE`，不能自动改扣点数。

### POST `/api/ai/images/generations`

同步接口，生成完成后返回。文生图与图生图共用这一个入口，靠 `references` 是否为空区分，与现有 `requestGeneration` 和 `requestEdit` 的分工一致。

请求：

```json
{
  "model": "gpt-image-2",
  "prompt": "一只坐在窗台上的猫",
  "n": 1,
  "size": "1024x1024",
  "quality": "high",
  "background": "transparent",
  "references": ["image:Ab3xY"],
  "mask": "image:Zk8mQ",
  "quoteToken": "...",
  "idempotencyKey": "V1StGXR8"
}
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `model` | 是 | 平台目录里的模型名；目录外、已禁用或能力不匹配一律拒绝 |
| `prompt` | 是 | **系统提示词由前端拼好**，沿用现有 `withSystemPrompt` 的做法，服务端不再拼一次 |
| `n` | 否 | 缺省 1，上限取 `constraints` 与 15 的较小值，与现有前端的 clamp 对齐 |
| `size` | 否 | `1024x1024` 形式的像素尺寸，或 `16:9` 形式的比例 |
| `quality` | 否 | 取值来自 `constraints.quality` |
| `background` | 否 | 只有 `transparent` 有意义，与现有 `normalizeBackground` 的语义一致 |
| `references` | 否 | 已上传媒体的 `storageKey` 数组，非空即走图生图 |
| `mask` | 否 | 蒙版的 `storageKey`，模型不支持时返回 `PARAM_NOT_SUPPORTED` |

**参考图传 `storageKey` 而不是 base64。** 现有代码用 `imageToDataUrl` 把参考图转成 dataURL 再塞进请求体，base64 会让请求体膨胀三分之一以上，服务端还要重新解码、校验大小、防住内存放大攻击。第二期之后参考图本来就存在媒体存储里，直接传 key、服务端自己去读要省得多，还能顺带校验归属——传别人的 `storageKey` 一律当作不存在处理，与第二期「跨用户返回 404」的约定一致。

代价是画布里刚粘贴、还没保存的临时图必须先 `PUT /api/media/{storageKey}` 再发起生成。这与第二期「媒体先落盘、业务引用后落库」的顺序约定一致，前端已经有这条路径。

成功 200：

```json
{
  "model": "gpt-image-2",
  "credits": { "baseCostMicros": 1000000, "finalCostMicros": 800000, "finalCostYuan": "0.80", "discount": { "promotionId": "promo-1", "name": "1K 限时 8 折", "discountBps": 8000 }, "remainingMicros": 13200000 },
  "images": [
    { "storageKey": "image:Ab3xY", "width": 1024, "height": 1024, "bytes": 812345, "mimeType": "image/png" }
  ],
  "generationId": "g1...",
  "durationMs": 8421
}
```

错误：`VALIDATION_FAILED`、`MODEL_NOT_SUPPORTED`、`PARAM_NOT_SUPPORTED`、`INSUFFICIENT_CREDITS`、`EMAIL_NOT_VERIFIED`、`STORAGE_QUOTA_EXCEEDED`、`CONCURRENCY_LIMITED`、`UPSTREAM_ERROR`、`UPSTREAM_TIMEOUT`。

### POST `/api/ai/videos/generations`

**异步接口，只创建任务，不等生成完成。** 现有 `requestVideoGeneration` 的做法是创建任务后本地轮询最多 120 次、每次间隔 2.5 秒或 5 秒，最长要等十分钟。把这段时间挂在一个 HTTP 连接上不现实：nginx 会读超时、移动网络会断、用户切个标签页就可能被系统回收。所以后端只负责创建任务并落一行 `ai_tasks`，由前端轮询查询接口。

请求：

```json
{
  "model": "grok-imagine-video",
  "prompt": "镜头缓慢推近",
  "duration": 6,
  "ratio": "16:9",
  "resolution": "720p",
  "generateAudio": true,
  "watermark": false,
  "references": ["image:Ab3xY"],
  "videoReferences": ["video:Qm4tR"],
  "audioReferences": ["audio:Nz9pL"],
  "quoteToken": "...",
  "idempotencyKey": "V1StGXR8"
}
```

`ratio` 与 `resolution` 用统一的语义字段，不再让前端在 `size`（`1280x720`）和 `ratio`（`16:9`）两种表达之间自己换算。现有前端为此写了 `normalizeVideoSize`、`normalizeVideoResolution`、`normalizeSeedanceRatio`、`normalizeSeedanceResolution` 四个函数，其中 Seedance 那两个还带「找最接近的比例」的模糊匹配，这类厂商适配搬到服务端的 provider 里去做。

`videoReferences` 与 `audioReferences` 只有部分模型支持，不支持时返回 `PARAM_NOT_SUPPORTED` 而不是像现在这样抛一个 `videoReferencesUnsupported` 文案。

成功 202：

```json
{
  "taskId": "3f2a...",
  "status": "pending",
  "credits": { "baseCostMicros": 5000000, "finalCostMicros": 4000000, "finalCostYuan": "4.00", "discount": { "promotionId": "promo-2", "name": "720p 限时 8 折", "discountBps": 8000 }, "remainingMicros": 9200000 },
  "generationId": "g2...",
  "pollAfterMs": 5000
}
```

点数在创建任务时就扣，任务最终失败时退还。`pollAfterMs` 由服务端给出，前端不再硬编码 2500 与 5000 这两个魔数——不同厂商的合理轮询间隔差别很大，这个知识应该跟着 provider 走。

错误：与生图接口相同。

### GET `/api/ai/videos/tasks/{id}`

查询视频任务状态。任务归属当前用户，查别人的任务返回 404 而不是 403。

```json
{
  "taskId": "3f2a...",
  "status": "succeeded",
  "video": { "storageKey": "video:Qm4tR", "bytes": 4821334, "mimeType": "video/mp4", "durationMs": 6000 },
  "credits": { "baseCostMicros": 5000000, "finalCostMicros": 4000000, "finalCostYuan": "4.00", "discount": { "promotionId": "promo-2", "name": "720p 限时 8 折", "discountBps": 8000 }, "refundedMicros": 0 },
  "generationId": "g2...",
  "pollAfterMs": 5000
}
```

`status` 取 `pending`、`succeeded`、`failed`。失败时不返回 `video`，改为返回 `error` 对象，形状与普通错误响应里的 `error` 一致，并在 `credits.refunded` 里给出退还的点数。

**上游轮询由服务端的后台 goroutine 主动做，不是等前端查询时才代查。** 这一点必须选对：如果服务端只在被查询时才去问上游，用户关掉页面这段时间任务就停在原地，而点数已经扣了；主动轮询则保证任务无论如何都会跑完、结果无论如何都会落库，用户下次打开页面就能看到。代价是要处理进程重启：`ai_tasks` 里存了 `provider` 与 `upstream_task_id`，服务启动时扫一遍非终态任务恢复轮询即可。同时后台轮询要有全局并发上限，避免大量任务同时在飞。

终态任务保留 24 小时后可以清理，前端在这期间随时能查到结果。

### POST `/api/ai/audio/speech`

同步接口。

请求 `{ "model": "...", "input": "要朗读的文本", "voice": "alloy", "format": "mp3", "speed": 1, "instructions": "...", "quoteToken": "...", "idempotencyKey": "..." }`，字段与现有请求对应，并带生成前取得的报价凭证。

成功 200：

```json
{
  "model": "gpt-4o-mini-tts",
  "credits": { "baseCostMicros": 20000, "finalCostMicros": 20000, "finalCostYuan": "0.02", "discount": null, "remainingMicros": 13000000 },
  "audio": { "storageKey": "audio:Nz9pL", "bytes": 184320, "mimeType": "audio/mpeg" },
  "durationMs": 1820
}
```

现有前端拿到的是 Blob（`responseType: "blob"`），再由 `storeGeneratedAudio` 调 `uploadMediaFile` 存起来。改造后这一步在服务端完成，前端直接拿 `storageKey`，`assertAudioBlob` 那种「响应看起来是 JSON 说明其实是报错」的兜底判断也就不需要了。

### POST `/api/ai/chat/completions`

文本对话，默认 SSE 流式。

请求：

```json
{
  "model": "gpt-5.5",
  "messages": [{ "role": "user", "content": "..." }],
  "reasoningEffort": "auto",
  "tools": [ ... ],
  "toolChoice": "auto",
  "stream": true,
  "quoteToken": "...",
  "idempotencyKey": "..."
}
```

`messages` 沿用现有 `AiTextMessage` 的形状，`content` 可以是字符串，也可以是 `{type:"text"}` 与 `{type:"image_url"}` 混排的数组。工具调用相关的 `tools` 与 `toolChoice` 必须保留，画布的 Agent 依赖它们。

**响应是统一的自有事件流，不是上游原始 SSE 的透传。** 这是本接口最重要的设计决定。如果原样透传，前端就得继续维护两套解析器：`consumeResponseStreamBlock` 认的是 OpenAI Responses API 的 `response.output_text.delta` 与 `response.completed`，`consumeGeminiStreamBlock` 认的是 Gemini 的 `candidates[].content.parts[]`，两者的错误位置、工具调用形状、结束标志全都不一样。本期的目标就是把厂商差异挡在服务端，透传等于把它原封不动地漏过去。

事件形状固定为四种：

```txt
event: delta
data: {"text":"增量文本"}

event: tool_call
data: {"id":"call_1","name":"search","arguments":"{\"q\":\"...\"}"}

event: error
data: {"code":"UPSTREAM_ERROR","message":"上游返回 500"}

event: done
data: {"credits":{"baseCostMicros":20000,"finalCostMicros":20000,"finalCostYuan":"0.02","discount":null,"remainingMicros":13100000},"finishReason":"stop"}
```

`delta` 只带增量而不带累计文本，与现有 `onDelta(state.text)` 传累计值的做法相反。累计由前端自己做一次字符串拼接就行，让每个事件都重发一遍全文，长回答的传输量会随长度平方增长。

**错误有两种表达形式，前端必须都处理。** SSE 建立之前的错误（未登录、点数不足、参数非法、并发超限）走普通 HTTP 状态码加第一期的 `error` 包裹，此时响应根本不是 `text/event-stream`；SSE 已经建立之后再出错，只能发一个 `event: error` 再关闭连接，HTTP 状态码已经是 200 了没法改。

`stream: false` 时返回一次性 JSON `{ "credits": ..., "content": "...", "toolCalls": [...], "finishReason": "stop" }`，供不需要增量渲染的调用方使用，例如 Agent 的中间工具调用轮次。

## 参数校验

校验必须发生在预扣和调用上游之前。顺序固定为：鉴权 → 模型和参数校验 → 报价凭证校验 → 邮箱与存储预检 → 并发槽位 → 原子占用免费次数或预扣点数 → 调用上游。任何一步失败都不进入下一步。

校验依据是 `model_catalog.constraints`，第三期定义、本期只消费。它的形状大致如下：

```json
{
  "size": ["1024x1024", "1536x1024", "1024x1536"],
  "quality": ["low", "medium", "high"],
  "n": { "max": 4 },
  "duration": [4, 6, 8],
  "resolution": ["480p", "720p", "1080p"],
  "ratio": ["16:9", "9:16", "1:1"],
  "features": ["mask", "referenceImage"]
}
```

数组型的字段做白名单精确匹配，对象型的做范围判断。命中失败返回 400 `PARAM_NOT_SUPPORTED`，`error` 里带 `param` 和 `allowed`，前端可以直接把 `allowed` 渲染成可选项让用户重选，不用再猜哪里不对。

**服务端不做「自动纠正」。** 现有前端积累了一批模糊映射：`normalizeVideoResolution` 把 `high` 映射成 `720p`，`normalizeSeedanceRatio` 在找不到精确匹配时挑一个最接近的比例，`closestGeminiAspectRatio` 在 14 个 Gemini 支持的比例里选最近的，`resolveSize` 还会按质量档和比例反算出一个像素尺寸再对齐到 16 的倍数。这些映射的存在是因为「前端的固定选项」和「厂商实际接受的取值」对不上，只能靠猜。唯一路径上这个前提反过来了：选项由 `constraints` 驱动，前端只可能选出合法值，服务端遇到非法值就该直接拒绝。继续猜只会掩盖前端的 bug，而且用户会拿到一个自己没选过的尺寸。这批映射函数连同它们服务的直连实现在本期一起删除。

需要区分的是**厂商格式改写**：同一个「16:9、720p」在 OpenAI 兼容接口里是 `size=1280x720`，在 Ark 里是 `ratio=16:9` 加 `resolution=720p`。这类改写是 provider 适配器的正当职责，与「纠正用户输入」是两回事，要放在 `internal/provider/` 下而不是校验层。

服务端同样**不需要维护多套宽松校验**。参数约束的唯一事实来源是 `model_catalog.constraints`，前端选项、`/api/ai/quote` 与真实转发三处共用它，不存在「另一个来源的参数需要另一套规则」的情况。

## 流式转发

### Gin 里的写法

响应头先设好，其中 `X-Accel-Buffering: no` 是给 nginx 看的，能在应用层就地关闭该响应的缓冲，比只依赖 nginx 配置多一层保险：

```go
c.Header("Content-Type", "text/event-stream")
c.Header("Cache-Control", "no-cache")
c.Header("Connection", "keep-alive")
c.Header("X-Accel-Buffering", "no")
flusher, ok := c.Writer.(http.Flusher)
if !ok {
    // 不支持 Flush 说明中间包了一层缓冲 Writer，直接按 INTERNAL_ERROR 返回
}
```

然后每写完一个事件立刻 `flusher.Flush()`。**漏掉 Flush 是这里最常见的 bug**：Go 的响应 Writer 自带缓冲，不主动刷会攒够几 KB 才发出去，表现就是「前面一直没反应，最后哗一下全出来」，和没做流式完全一样，而且在短回答上根本看不出来——只有长回答才会暴露。

读上游同样不能等全部返回。**不要用 `io.ReadAll`**，要按行增量读。如果用 `bufio.Scanner`，务必调用 `scanner.Buffer` 把上限调大：默认单行上限是 64KB，而带图片的多模态响应或长工具调用参数很容易超过，超过之后 Scanner 会静默停止，表现为「回答莫名其妙截断」。更稳妥的做法是直接用 `bufio.Reader.ReadString('\n')`，没有行长上限。

上游长时间不吐字时要发心跳。每 15 秒写一个 SSE 注释行 `: ping`，避免中间的反向代理或负载均衡按空闲超时掐断连接。注释行不会被前端的事件解析器当成数据。

### 客户端断开时取消上游

用 `c.Request.Context()` 作为上游请求的 context，通过 `http.NewRequestWithContext` 传进去。客户端断开时 Gin 会取消这个 context，上游的 HTTP 连接随之关闭，上游那边通常也会停止生成，不再继续消耗额度。不要用已经废弃的 `CloseNotify`。

同一个 context 还要串到后续所有环节：结果下载、媒体写入、数据库操作。只取消上游请求而让后面的写入继续跑，会留下「用户已经走了但服务端还在忙」的僵尸任务。

前端的「停止生成」按钮走的是 `AbortController`，现有代码里所有请求都已经接了 `options.signal`，中止后浏览器关闭连接，服务端这条链路就跟着断掉，不需要额外设计一个取消接口。

### 反向代理必须关闭缓冲

nginx 默认开启 `proxy_buffering`，会先把上游响应攒进缓冲区再转发，SSE 在它后面会完全失去流式效果。这是本期最容易「本地测着好好的，部署上去就不流式了」的地方，因为本地开发通常直连后端端口，根本不经过 nginx。

配置见下面的「部署调整」。验收时必须在 nginx 后面测，不能直连 8080。

## 扣点与生成的一致性

### 先扣后生成，失败退还

顺序是**先扣后生成**，不是先生成后扣。先生成的话，用户在生成完成到扣点之间断开连接就白拿了一次结果；而先扣的失败面是「扣了没生成出来」，可以靠退还补偿，两害相权取其轻。

预扣在一个数据库事务里完成：重新解析当前基础价和有效折扣、与 token 中的原价及折后价逐项核对、锁定双桶余额、按先赠送后购买扣减折后金额并写一到两条消费流水。流水和 `ai_requests` 快照记录 `base_cost_micros`、`final_cost_micros`、`promotion_id` 与活动版本，便于解释“为什么这次扣了这个价格”。事务提交之后才发起上游请求。

退还往第三期的 `credit_transactions` 里写一条 `type=refund` 流水，金额与被退的消费流水相等，并且**在流水里记 `ref_id` 指向被退的那条 `type=consume` 流水的 id**，一条消费最多对应一条退还。

幂等由第三期的 `refund_of_transaction_id` 唯一索引兜底。每条退款指向一条具体消费流水，普通流水保持 NULL；重试、并发或进程重启导致同一消费被再次退还时，第二次插入撞唯一冲突并按“已退还”处理。跨桶消费有两条消费流水，退款也逐条回原桶。

退还本身也可能失败（数据库抖动、进程被杀）。这种情况下把 `ai_requests.refund_pending` 置为真并记 error 级日志，管理后台的用户详情页提供一个「重试待退还」的入口。不要为此引入定时任务或消息队列，出现频率极低，人工触发足够。

### 流式场景下「失败」的定义

这是本期最需要提前定死的规则，因为它直接影响用户会不会觉得被乱扣钱。

**判定标准是「上游有没有开始产出」。**

| 场景 | 判定 | 是否退还 |
| --- | --- | --- |
| 连接上游失败、鉴权失败、上游 4xx/5xx | 失败 | 全额退还 |
| 首个 delta 之前超时 | 失败 | 全额退还 |
| 已产出若干 delta 后上游中途断开 | 成功 | 不退还 |
| 已产出若干 delta 后用户点「停止」 | 成功 | 不退还 |
| 已产出若干 delta 后客户端网络断开 | 成功 | 不退还 |
| 非流式接口：上游返回了但解析不出产物 | 失败 | 全额退还 |
| 非流式接口：结果下载或落盘失败 | 失败 | 全额退还 |

「已经开始输出就算成功」的理由有两条。**上游按 token 计费，已经产生的输出就是已经发生的成本**，退给用户等于平台自己吃掉；更关键的是，「上游中途断开」和「用户网络抖动」在服务端看来常常是同一种现象，无法可靠区分，而任何无法可靠区分的判定标准最后都会变成漏洞。

图像、语音这类一次性返回的接口没有这个歧义：拿不到产物就是失败，全额退。视频任务的终态为 `failed`、`cancelled`、`expired` 时全额退；用户在任务还是 `pending` 时主动取消可以退，已经进入生成阶段的不退，理由同上。

这条规则要同步写进产品文案，别让用户在扣费记录里自己去猜。

### 并发与重复提交

前端可能因为按钮双击、网络重试、React 严格模式下的重复执行而把同一个生成请求发两次。服务端靠 `idempotencyKey` 去重：在 `ai_requests` 表上对 `(user_id, idempotency_key)` 建唯一索引，插入冲突时说明是重复请求，直接返回上一次的结果；上一次还在进行中就返回一个「进行中」的响应而不是再扣一次点。

前端要保证同一次用户操作用同一个 key，重试时复用而不是重新生成。key 用 `nanoid` 即可，项目已有依赖。

缺失 `idempotencyKey` 的请求返回 400 `VALIDATION_FAILED`。key 由 `ai.ts` 在请求出口统一生成，Agent 的工具调用循环走同一个出口，不需要在调用链里逐层传参；绕过 `ai.ts` 手工拼请求的调用方必须自己带 key，这正是要拦住的对象。

## 超时与重试

### 按能力分别设置超时

生图和生视频的耗时差着一个数量级，用同一个超时值必然一头太松一头太紧。分开设置：

| 能力 | 连接超时 | 首字节超时 | 整体超时 |
| --- | --- | --- | --- |
| 文生图 | 10 秒 | 60 秒 | 180 秒 |
| 图生图 | 10 秒 | 60 秒 | 240 秒 |
| 语音合成 | 10 秒 | 30 秒 | 120 秒 |
| 文本流式 | 10 秒 | 30 秒（首个 delta） | 600 秒，且空闲 60 秒无数据即断 |
| 视频任务创建 | 10 秒 | 30 秒 | 60 秒 |
| 视频任务轮询 | 10 秒 | 20 秒 | 单次 30 秒，任务总时长上限 20 分钟 |

视频之所以能用这么短的 HTTP 超时，是因为它已经改成异步任务了：创建接口只需要拿到上游的任务 id，真正的等待发生在后台轮询里，不占用任何 HTTP 连接。

文本流式的「空闲超时」和「整体超时」是两回事，都要有。只有整体超时的话，一个卡住的上游会白占 10 分钟的并发槽位；只有空闲超时的话，一个持续缓慢吐字的上游能永远挂着。

这几个值都要能通过环境变量覆盖，不同部署环境的上游质量差别很大。

### 上游 5xx 的重试规则

**只在没有产生任何输出时重试，最多一次。** 一旦已经往客户端写过 delta 或者已经拿到部分产物，重试就意味着用户会看到重复内容，不如直接失败。

| 上游状态 | 处理 |
| --- | --- |
| 连接失败、DNS 失败 | 重试一次，仍失败则切换备用渠道 |
| 502、503、504 | 重试一次，仍失败则切换备用渠道 |
| 429 | 不重试，返回 502 `UPSTREAM_ERROR` 并在 `error.upstreamStatus` 里带 429 |
| 400、422 | 不重试，参数问题重试还是错 |
| 401、403 | 不重试，Key 有问题，同时打 error 日志提醒运维 |
| 500 | 重试一次 |

上游 429 之所以不映射成本站的 429 `RATE_LIMITED`，是因为两者的含义完全不同：本站限流是「你请求太快了」，上游限流是「平台的额度被打满了」，混在一起会让用户以为是自己的问题。

**重试和故障转移都不重复扣点。** 扣点发生在调用上游之前，一次业务请求只扣一次，内部重试几次是实现细节。这一点在代码上要靠结构保证——扣点逻辑放在 `service/credit.go`，重试循环放在 `service/upstream.go`，两者不嵌套，就不可能写出「在重试循环里扣点」的代码。

视频任务的轮询失败要单独对待：单次查询失败不算任务失败，累计连续失败 5 次才判定失败并退还。上游抖动一下就退款并丢掉结果，比多等一会儿糟糕得多。

### 进程重启后的 `ai_requests` 收敛

`ai_requests.status = running` 不是稳态，进程重启会让同步能力（图片、语音、对话）的请求永远停在那里：请求上下文随进程一起消失，没有任何代码路径会再把它置成终态，而它身上可能还挂着已预扣、未退还的点数。视频能力没有这个问题——`ai_tasks` 的启动恢复流程会把任务收敛到终态，收敛时顺手把对应的 `ai_requests` 一并处理。同步能力必须显式补上这条路径：

服务启动时（`/readyz` 通过之前）扫描 `status = 'running'` 且 `updated_at` 早于「该能力整体超时的两倍」的请求，全部置为 `failed`；每条被置失败的请求检查是否存在已预扣且尚无退款流水的消费，有就走幂等 Refund——`refund_of_transaction_id` 的唯一索引保证并发或重复扫描不会双退。扫描不做的代价是三类事故叠加：账上永远挂着一笔无法解释的预扣、`/api/admin/stats` 的进行中请求数虚高、该用户的并发槽位在重启后少一个。

「两倍超时」而不是「一倍」是给时钟偏移和慢请求留余量：重启瞬间可能恰好有一个合法的慢请求还在跑，按一倍超时误杀它，用户会看到「明明成功了却提示失败还退了款」的怪现象；两倍超时下，真正卡死的请求最多多挂一个超时周期，可以接受。

## 平台渠道与 Key 管理

### 表结构与加密

平台自己的渠道单独一张表，用本期引入的 AES-256-GCM 与 `CREDENTIAL_MASTER_KEY` 加密。全服务只有这一把加密主密钥，以后其他需要加密落库的敏感字段也复用它——多一把密钥就多一处配置、多一处泄漏面、多一处「换机器时忘了带过去」的故障。

```go
type PlatformChannel struct {
    ID         uuid.UUID `gorm:"type:char(36);primaryKey"`
    Name       string    `gorm:"type:varchar(80);not null"`
    BaseURL    string    `gorm:"type:varchar(300);not null"`
    APIFormat  string    `gorm:"type:varchar(16);not null"` // openai | gemini | ark
    Nonce      []byte    `gorm:"not null"`
    Payload    []byte    `gorm:"not null"` // AES-256-GCM 加密后的 API Key
    KeyVersion int       `gorm:"not null;default:1"`
    Priority   int       `gorm:"not null;default:0"`
    Enabled    bool      `gorm:"not null;default:true"`
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

`APIFormat` 的取值与前端 `ApiCallFormat` 完全一致（`openai`、`gemini`、`ark`），因为服务端的 provider 就是把前端现有的三套适配逻辑搬过去，没有理由换一套命名。

`model_catalog` 本期补一个有序的 `channel_ids` JSON 列——第三期建目录时刻意不含渠道指向（当时还没有 `platform_channels`），现在补上，数组顺序就是故障转移顺序。不单独建一张绑定表：本期的关系简单到一个数组就能表达，多一张表只会让每次查目录都多一次 join。

平台 Key 的读取只发生在发起上游请求的那一刻，解密后的明文不进任何缓存、不进日志、不出现在任何响应里。第一期的 `middleware/logger.go` 已经会脱敏密码与令牌字段，本期把 `Authorization`、`x-goog-api-key`、`apiKey` 一并加进脱敏列表。

### 多上游与故障转移

按 `channel_ids` 的顺序取第一个 `enabled` 的渠道发起请求。连接失败或 5xx 且尚未产生任何输出时，切到下一个渠道，最多尝试两个。全部失败返回 502 `UPSTREAM_ERROR`。

不做健康检查与自动摘除。健康检查要维护后台探测、状态机和恢复策略，而当前的上游数量是个位数，「按顺序试、失败就切」已经能覆盖绝大多数场景。等真的出现「某个渠道长时间不可用导致每次请求都要先失败一次」的问题时，再加一个简单的失败计数熔断即可。

管理入口挂在第三期的管理后台下，`/api/admin/channels` 提供增删改查。**Key 字段只写不读**：响应里返回 `hasKey: true` 与后四位，永远不返回明文。管理员想换 Key 就重新填一次，没有「先看看现在配的是什么」这个需求。

## 生成结果的落地

上游返回产物只有三种形态：base64（`b64_json`）、URL、以及直接的二进制流（语音）。**服务端一律转存到第二期的媒体存储，只把 `storageKey` 给前端。**

不能把第三方 URL 直接交给前端，理由有三条。**这类 URL 通常是几十分钟到几小时就过期的签名地址**，用户把生成结果放进画布，第二天打开就是一片破图，而画布是要长期保存的。**第三方 URL 会让浏览器直连外部域**，绕过了第二期设计的同源与媒体 cookie 权限模型——单一路径方案下浏览器不与任何外部域通信，这是要守住的性质。**结果不落库就没有存储用量记账**，第三期的存储配额直接被绕过。

转存流程是：拿到 base64 就解码，拿到 URL 就下载，然后走第二期的 `Storage.Put` 写盘、写 `media_files`、更新存储用量计数，最后返回 `storageKey`。这几步与 `PUT /api/media/{storageKey}` 走的是同一套代码，不要另写一份。

下载外部 URL 必须做出站防护，这是本期新引入的攻击面：只允许 http 与 https，跟随重定向时每一跳都要重新校验，解析出的 IP 落在回环、私有网段、链路本地地址一律拒绝，并且要在建立连接时校验实际 IP（用 `net.Dialer.Control`）以防 DNS rebinding。下载还要限制最大字节数（按套餐 `max_file_bytes`）和超时（图片 60 秒、视频 300 秒）。虽然这些 URL 来自平台自己配置的上游，但 URL 的内容是上游生成的，仍然属于外部输入，一个被攻陷或行为异常的上游返回一个内网地址是完全可能的。

**服务端主动发起的外部请求只有两类：打平台自己配置的上游，以及下载平台上游返回的结果 URL。** 不存在用户自填的 `baseUrl`，也就不存在由用户完全控制的目标地址，出站防护面收敛到最小，校验逻辑集中在 `service/upstream.go` 一处。

落盘失败等同生成失败，全额退还点数。用户拿不到产物却被扣了钱，无论技术上是哪一步出的问题，对他来说都是一样的。

**生成记录由服务端写，不由前端补。** 第三期把 `generations` 定成了生成历史的权威来源，`/api/me` 的 `usage.imageGenerateToday` 与管理后台 `/api/admin/stats` 的今日生成量都直接从它 `count(*)` 数出来，所以平台路径的每一次生成都必须在这张表里留下、且只留下一条记录。**这条记录由服务端在结果落盘的同一个事务里写**：`kind`、`model`、`prompt`、`config`、`result`、`durationMs` 这些字段服务端在那一刻全都有，写 `media_files` 与更新存储用量计数本来就在同一个数据库事务里，顺手多写一条生成记录不引入任何新的一致性风险。

**前端完全不写生成记录。** 第二期的 `/api/generations` 资源从一开始就没有写接口，生成记录由服务端在结果落盘的同一个事务里创建，责任唯一：今日生成量从这张表数出来，没有「两边都写会翻倍」的问题。为了让前端拿到服务端刚写的这条记录（预览、删除、列表定位都要用 `id`），图像与视频接口的响应体里回一个 `generationId`。

**语音与文本两个接口不写 `generations`，也不返回 `generationId`。** 第二期的 `kind` 只有 `image` 与 `video` 两个取值，语音合成与文本对话本来就不进生成历史，本期也不为它们新增 `kind`。这两条链路的计量依据是 `credit_transactions` 的消费流水，与生成历史是两回事。

**视频任务的记录由服务端管完整个生命周期。** `POST /api/ai/videos/generations` 在扣点、写 `ai_tasks` 的同一个事务里落一条 `status=pending` 的生成记录，`generationId` 随 202 一起返回；后台轮询拿到终态后，由服务端在结果落盘的同一个事务里把这条记录收敛成 `success` 或 `failed`。`GET /api/ai/videos/tasks/{id}` 是纯查询，不写任何记录，**前端也不再调任何生成记录写接口**——那个接口本来就不存在。理由与「上游轮询由服务端主动做」是同一条：用户关掉页面之后没有任何前端在跑，把终态收敛交给前端等于让记录永远停在 `pending`。

## 限流与并发控制

点数本身不是限流手段。余额充足的用户完全可能同时发起几十个生成请求，把上游的并发额度打满，导致所有人都拿到 502。所以除了点数，还要有并发和频次两道闸。既然全部生成请求都走 `/api/ai/*`，这两道闸就能覆盖全部生成行为，不存在绕过本站的路径。

| 维度 | 阈值 | 超出时 |
| --- | --- | --- |
| 单用户同时进行中的图像请求 | 3 | 429 `CONCURRENCY_LIMITED`，`Retry-After: 5` |
| 单用户同时进行中的文本流式请求 | 2 | 同上 |
| 单用户同时进行中的语音请求 | 2 | 同上 |
| 单用户 `pending` 状态的视频任务 | 3 | 同上 |
| 单用户生成请求频次 | 30 次/分钟 | 429 `RATE_LIMITED` |
| 平台全局同时进行中的上游请求 | 按部署环境配置 | 429 `RATE_LIMITED`，提示稍后重试 |

视频任务的并发单独算，因为它不占 HTTP 连接，用同一个计数器会让「三个视频在后台跑」挡住「发一条文本消息」。

槽位在进入 handler 时占用、`defer` 释放。**流式请求要特别小心**：连接可能在任何时刻断开，释放逻辑必须放在 `defer` 里而不是正常返回路径上，否则用户反复开关几次流式对话就会把自己的槽位耗光，表现为「明明没在生成却提示并发超限」。验收清单里专门有一条测这个。

计数放在进程内内存，与第一期的限流实现保持一致。全局上限触发时直接返回 429 而不是排队——排队会让请求挂住，用户看到的是「转圈半天最后失败」，还不如立刻告诉他稍后再试。

## 自定义脚本整体移除

现有的 `model-plugin.ts` 允许用户为任意模型写一段 JavaScript 自定义调用方式，`runModelPlugin` 用 `new Function` 把它编译成一个异步函数，注入 `prompt`、`images`、`model`、`baseUrl`、`apiKey`、`http`、`request`、`poll` 等变量后执行。这套能力的前提是「用户自己的 Key、用户自己的 baseUrl」——两个前提都随自定义渠道一起消失，脚本没有可以注入的凭据，也没有可以直连的地址。

因此 `model-plugin.ts` 在本期**整个文件删除**，连同配置页的脚本编辑器 UI、`resolveModelScript` 与渠道模型编解码（`encodeChannelModel` / `decodeChannelModel` / `CHANNEL_MODEL_SEPARATOR`）一起清掉，不留开关或隐藏入口。模型调用方式由平台保证正确：参数取值由 `constraints` 约束、协议适配由服务端的 provider 负责、出错了由平台排查，用户改不了也不需要改。如果某个模型需要特殊调用逻辑，正确做法是在服务端 `internal/provider/` 里写适配，而不是把责任推回给用户。

## 后端新增文件

| 路径 | 职责 |
| --- | --- |
| `internal/crypto/crypto.go` | AES-256-GCM 加解密与 `CREDENTIAL_MASTER_KEY` 装载校验；凭据托管取消后，这层随 `platform_channels` 在本期首次引入 |
| `internal/handler/ai.go` | 报价与五个生成/查询接口的入口：模型、参数、报价、配额、预扣、provider 与结果落地 |
| `internal/handler/ai_stream.go` | SSE 的响应头、事件写入、心跳、断开处理 |
| `internal/handler/admin_channel.go` | 平台渠道的管理接口，Key 只写不读 |
| `internal/provider/provider.go` | `Provider` 接口与注册表，按 `APIFormat` 选实现 |
| `internal/provider/openai.go` | OpenAI 兼容格式的图像、语音、视频与 Responses 流式 |
| `internal/provider/gemini.go` | Gemini 适配，承接现有 `toGeminiBody`、`toGeminiContents`、`parseGeminiToolResponse` 的转换逻辑 |
| `internal/provider/ark.go` | Ark 与 Seedance 适配，含 `/contents/generations/tasks` 的创建与查询 |
| `internal/service/credit.go` | 双桶预扣、原桶退还与幂等判定，生成入口共用 |
| `internal/service/aitask.go` | 视频异步任务的创建、后台轮询、进程重启后的恢复 |
| `internal/service/upstream.go` | 渠道选择、故障转移、超时配置、结果下载与出站地址校验 |
| `internal/model/model.go` | 增加 `PlatformChannel`、`AIRequest`、`AITask` |

把流式单独拆一个文件，是因为它的生命周期管理与其余四个接口完全不同：响应头要提前写、错误不能用 `c.JSON`、每个事件要 Flush、断开要靠 context。混在 `ai.go` 里会让两种模式的代码互相污染。

`Provider` 接口保持最小，只描述能力，不关心 HTTP：

```go
type Provider interface {
    Images(ctx context.Context, req ImageRequest) (ImageResult, error)
    Speech(ctx context.Context, req SpeechRequest) (SpeechResult, error)
    CreateVideo(ctx context.Context, req VideoRequest) (VideoTask, error)
    PollVideo(ctx context.Context, task VideoTask) (VideoState, error)
    ChatStream(ctx context.Context, req ChatRequest, sink StreamSink) error
}

type StreamSink interface {
    Delta(text string) error
    ToolCall(call ToolCall) error
    Done(finishReason string)
}
```

不支持的能力返回 `ErrCapabilityUnsupported`，handler 统一映射成 400 `MODEL_NOT_SUPPORTED`。provider 只管把上游各自的 SSE 解析成三个回调，handler 只管把回调写成 HTTP 事件，加一个新厂商不需要碰任何 HTTP 代码。

两张新表：

```go
type AIRequest struct {
    ID               uuid.UUID  `gorm:"type:char(36);primaryKey"`
    UserID           uuid.UUID  `gorm:"type:char(36);not null"`
    IdempotencyKey   *string    `gorm:"type:varchar(40)"`
    Capability       string     `gorm:"type:varchar(16);not null"` // image | video | audio | text
    Model            string     `gorm:"type:varchar(120);not null"`
    ChannelID        *uuid.UUID `gorm:"type:char(36)"`
    BaseCostMicros   int64      `gorm:"not null;default:0"`
    FinalCostMicros  int64      `gorm:"not null;default:0"`
    PriceVersion     int        `gorm:"not null"`
    PromotionID      *uuid.UUID `gorm:"type:char(36)"`
    PromotionVersion *int
    PromotionEnabled bool       `gorm:"not null"`
    PricingSnapshot  datatypes.JSON `gorm:"type:json"`
    Status           string     `gorm:"type:varchar(16);not null"` // running | succeeded | failed | refunded
    RefundPending    bool       `gorm:"not null;default:false"`
    UpstreamStatus   int
    DurationMs       int
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

type AITask struct {
    ID             uuid.UUID `gorm:"type:char(36);primaryKey"`
    UserID         uuid.UUID `gorm:"type:char(36);not null"`
    RequestID      uuid.UUID `gorm:"type:char(36);not null"`
    Provider       string    `gorm:"type:varchar(16);not null"`
    UpstreamTaskID string    `gorm:"type:varchar(200);not null"`
    Status         string    `gorm:"type:varchar(16);not null"` // pending | succeeded | failed
    StorageKey     string    `gorm:"type:varchar(80)"`
    Error          string    `gorm:"type:text"`
    PollFailures   int       `gorm:"not null;default:0"`
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

索引：`ai_requests.idempotency_key` 改为可空指针；MySQL 唯一索引 `(user_id, idempotency_key)` 允许多行 NULL，但同一用户的非空 key 只能出现一次。另建 `(user_id, created_at)` 服务管理后台查询；`ai_tasks` 上 `(status, updated_at)` 服务启动恢复，以及 `(user_id, id)` 服务归属校验。不要使用 MySQL 不支持的部分索引。

## 前端改造

本期前端的核心动作是**把每个生成入口整体切换到 `ai.ts`，并删除全部直连实现**。切换完成后，浏览器里不存在任何 API Key、baseUrl、上游域名与自定义脚本；除 SSE 外的所有请求都走第一期的统一 client。凡是本节没有明确写「保留」的直连相关代码，一律默认删除。

### 新增 `web/src/services/api/ai.ts`

导出 `quoteGeneration`、`generateImages`、`createVideoTask`、`getVideoTask`、`generateSpeech`、`streamChat` 六个函数，是全部生成行为的唯一调用方。除 SSE 外都走第一期的统一 client。

`streamChat` 需要读 SSE，如果 `client.ts` 只封装了 JSON 响应，就在这里单独用 `fetch`，但**必须复用 `client.ts` 的 token 获取与刷新重试逻辑**，把那部分抽成 `client.ts` 的导出函数再调用。绝对不要在 `ai.ts` 里重写一遍「单飞刷新 + 请求排队」，第一期文档已经点名那是最容易写出 bug 的地方，写两遍就是两份 bug。

### 生图链路 `web/src/services/api/image.ts`

`requestGeneration`、`requestEdit`、`requestImageQuestion` 三个导出函数保留、签名不变，**函数体全部改写为调用 `ai.ts`**。返回值统一成带 `storageKey` 的结构——服务端已经落盘，不再有「拿到 dataURL 再补一次转存」这一步，调用方无需感知差异。

因此这个文件里的直连协议适配**整体删除**：`aiApiUrl`、`aiHeaders`、`geminiBaseUrl`、`geminiApiUrl`、`geminiHeaders`、`geminiModelName`、`toGeminiBody`、`toGeminiContents`、`toGeminiParts`、`toGeminiImagePart`、`toGeminiToolOptions`、`requestGeminiImages`、`requestGeminiImagesOnce`、`parseGeminiImagePayload`、`parseGeminiToolResponse`、`validateGeminiPayload`、`consumeGeminiStreamText`、`consumeGeminiStreamBlock`、`requestGeminiStreamingResponse`、`resolveGeminiImageConfig`、`closestGeminiAspectRatio`、`resolveGeminiImageSize`、`supportsGeminiImageSize`、`requestStreamingResponse`、`consumeResponseStreamText`、`consumeResponseStreamBlock`、`parseToolResponse`、`toResponseInput`、`toResponseContent`、`toResponseTool`、`validateResponsePayload`、`parseImagePayload`、`resolveImageDataUrl`、`readFetchError`、`readAxiosError`、`readStatusError`、`readApiErrorMessage`，以及 `GEMINI_SUPPORTED_RATIOS`、`GEMINI_IMAGE_SIZE_BY_QUALITY`、`defaultGeminiConfig` 这几个常量。它们唯一的调用方是已删除的直连路径。

尺寸推导那一组——`resolveSize`、`resolveRequestSize`、`validateImageSize`、`parseImageRatio`、`parseImageDimensions`、`normalizeQuality`、`normalizeBackground` 以及 `QUALITY_BASE`、`IMAGE_SIZE_STEP`、`IMAGE_MIN_PIXELS`、`IMAGE_MAX_PIXELS`、`IMAGE_MAX_EDGE`、`IMAGE_MAX_RATIO` 这些常量——**同样删除**。参数约束的唯一事实来源是模型目录的 `constraints`，服务端已经按它校验并实现等价解析，前端不再维护第二套尺寸算法。

`fetchImageModels` 与 `fetchChannelModels` 删除，渠道模型列表这个概念不复存在。

`withSystemPrompt` 与 `withSystemMessage` 保留，系统提示词继续在前端拼进 prompt，服务端不感知。

### 视频链路 `web/src/services/api/video.ts`

`requestVideoGeneration` 保留签名，内部改为「`createVideoTask` 拿 `taskId`，按响应里的 `pollAfterMs` 轮询 `getVideoTask`」，轮询循环、终态判定、超时上限全部由服务端的任务状态驱动。

**直连实现整体删除**：`createOpenAIVideoTask` / `pollOpenAIVideoTask`、`createSeedanceTask` / `pollSeedanceTask`、那套最多 120 次、间隔 2.5 秒或 5 秒的本地轮询，以及 `seedanceApiUrl`、`buildSeedanceContent`、`resolveSeedanceImageUrl`、`resolveSeedanceVideoUrl`、`resolveSeedanceAudioUrl`、`assertSeedanceVideoReferences`、`assertSeedanceAudioReferences`、`unwrapEnvelope`、`unwrapVideoResponse`、`unwrapSeedanceTask`、`videoResultUrl`、`videoResultFromUrl`、`assertVideoBlob`、`assertVideoConfig`、`normalizeVideoSeconds`、`normalizeVideoSize`、`normalizeVideoResolution`、`aiApiUrl`、`aiHeaders` 和本文件的 `readApiErrorMessage` / `readAxiosError` / `statusMessage`。`web/src/lib/seedance-video.ts` 整个文件删除，UI 选项改由 `constraints` 渲染，服务端的 Ark provider 独立实现等价换算。

`VideoGenerationTask.provider` 的取值收敛为 `"backend"` 一种（或直接删掉该字段）。`pluginVideoResults` 这个模块级 Map 删除。`VideoGenerationResult` 扩成带可选 `storageKey` 的结构，`storeGeneratedVideo` 删除——服务端已经转存，前端不再经手视频二进制。

第二期遗留的 `resumePendingLogs` 问题在此顺带解决：任务的权威状态在服务端，前端刷新页面后按 `generationId`/`taskId` 调一次查询接口就能续上。

### 语音链路 `web/src/services/api/audio.ts`

`requestAudioGeneration` 保留签名，内部改为调用 `generateSpeech` 直接拿 `storageKey`。`aiApiUrl`、`aiHeaders`、`assertAudioConfig`、`assertAudioBlob`、`readApiErrorMessage`、`readAxiosError`、`statusMessage` 与 `storeGeneratedAudio` 删除。

`web/src/lib/audio-generation.ts` 里的 `normalizeAudioFormatValue`、`normalizeAudioSpeedValue`、`normalizeAudioVoiceValue` 保留——选项本身改由 `constraints` 渲染后，这些归一化函数仍可用于把历史取值映射到合法集合；`audioMimeType` 视 `constraints` 是否覆盖格式字段决定去留。

### 自定义脚本删除 `web/src/services/api/model-plugin.ts`

整个文件删除，理由见「自定义脚本整体移除」一节。全局搜索 `runModelPlugin`、`createPluginHttp`、`createPluginRequest`、`createPoll`、`getPluginVariables`、`getPluginTemplates`、`normalizePluginImages` 确认连同调用点一起清干净。

### 配置 store 与模型来源 `web/src/stores/use-config-store.ts`

AI 渠道相关成员**整体删除**：`AiConfig` 及其 `baseUrl`、`apiKey`、`channels`、`channelMode` 字段，`buildApiUrl`、`resolveModelChannel`、`resolveModelRequestConfig`、`resolveModelScript`、`encodeChannelModel`、`decodeChannelModel`、`findChannelModel`、`normalizeChannels`、`createModelChannel`、`normalizeChannelModels`、`guessCapability`，以及 `fetchImageModels`、`fetchChannelModels` 的调用面。`isAiConfigReady` 一并删除——模型来自平台目录，不存在「渠道没配好」这种状态，生成入口的就绪条件只剩「已登录且目录已加载」。

`config.models` 与 `selectableModelsByCapability` 的唯一来源是 `/api/models` 拉到的平台目录，目录项自带 `capability` 与 `constraints`。`modelOptionLabel` 保留，用于展示模型名与点数信息。

**登录前的生成入口不需要单独处理**：全站业务路由在第一期就包了 `RequireAuth`，能进入生成页的用户必然已登录。

### 模型选择器与参数面板

`web/src/components/model-picker.tsx` 的 `options` 直接来自 `selectableModelsByCapability`（平台目录），不再合并任何用户渠道来源。模型项展示预估点数；`emptyModelLabel` 的空态文案重写——目录为空时提示「管理员尚未上架模型」，而不是「先去添加渠道」。

尺寸、比例、时长这些硬编码选项**全部退役**：`web/src/components/image-settings-panel.tsx` 的 `qualityOptions` 与 `aspectOptions`（对外导出为 `imageQualityOptions`、`imageAspectOptions`）、`web/src/components/video-settings-panel.tsx` 的 `resolutionOptions`、`sizeOptions`、`secondOptions`（导出为 `videoResolutionOptions`、`videoSizeOptions`、`videoSecondOptions`）、`web/src/components/audio-settings-panel.tsx` 的 `speedOptions`、`web/src/components/canvas/canvas-size-picker.tsx` 的 `sizeOptions`，全部改为从当前选中模型的 `constraints` 动态渲染。

切换模型时如果当前取值不在新模型的 `constraints` 里，自动落回该模型的第一个合法值。留着一个必然被 400 拒绝的取值，用户点了生成才报错，体验很差。

画布侧的 `canvas-image-settings-popover.tsx`、`canvas-video-settings-popover.tsx`、`canvas-audio-settings-popover.tsx`、`canvas-image-toolbar-settings-modal.tsx`、`canvas-config-node-panel.tsx`、`canvas-node-prompt-panel.tsx` 复用的是同一批导出常量，改导出源即可覆盖，但要逐个确认没有各自维护的副本。

### 调用方与错误处理

六处调用方需要跟着改：`web/src/pages/image/index.tsx`、`web/src/pages/video/index.tsx`、`web/src/pages/canvas/project.tsx`、`web/src/pages/canvas/hooks/use-plugin-host.tsx`、`web/src/components/canvas/canvas-node-generation.ts`、`web/src/lib/agent/agent-site-tools.ts`。改动集中在返回值形状上：所有路径统一拿到 `storageKey`，不再有 Blob 与 dataURL 的分支。

**生成记录的写入从页面里去掉。** `web/src/pages/image/index.tsx` 与 `web/src/pages/video/index.tsx` 原来的 `saveLog` 往 localforage 塞记录，第二期已改为只读服务端的生成记录（本就没有写接口），本期把页面上残留的任何「自己补一条记录」的意图全部删除：记录由服务端写，页面拿响应里的 `generationId` 直接 `invalidateQueries(["generations", kind])` 即可。画布侧的 `canvas-node-generation.ts`、`project.tsx`、`use-plugin-host.tsx` 与 Agent 的 `agent-site-tools.ts` 本来就不写生成记录，这一条与它们无关。

**错误处理只剩一套。** 全部错误按第一期的约定以 `error.code` 返回，文案统一走 `apiErrors.*` 注册表（见第一期「统一错误提示机制」）；三个文件里各自的 `readApiErrorMessage` / `readAxiosError` 猜测逻辑与 `apiErrors.*` 里那批靠猜 HTTP 状态码生成的条目（`authenticationFailed`、`badGateway`、`serviceBusy`、`httpFailed`、`rateLimited`、`notFound`、`htmlError`）**一起删除**。各错误码的呈现行为在生成入口逐个落实：

- 402 `INSUFFICIENT_CREDITS`：拦截生成并展示「还差 N 点（¥X.XX）」+ 充值按钮，用户已输入的 prompt 与参数原样保留。
- 409 `QUOTE_STALE`：静默重新调一次 `/api/ai/quote`，新价与用户确认过的价不同时弹确认框展示新价，用户点确认后才按新价生成；不自动按旧价提交。
- 429 `CONCURRENCY_LIMITED` / `RATE_LIMITED`：按钮进入按 `Retry-After` 秒数的禁用倒计时，文案说明「生成任务过多，请稍候」，不要让用户连点。
- 502 `UPSTREAM_ERROR` / 504 `UPSTREAM_TIMEOUT`：提示「生成失败，本次点数已退回」，失败不吞掉用户输入，可直接重试；重试走新的 `idempotencyKey`（`ai.ts` 每次请求生成），不与失败请求共用。
- 403 `READ_ONLY` / `ACCOUNT_PENDING_DELETION` / `EMAIL_NOT_VERIFIED`：持续性状态，按第一期的分层用常驻提示引导，而不是每次生成弹一条。

生成成功后让 `/api/me` 的 react-query 查询失效，点数余额与免费额度跟着刷新，与第三期处理配额的做法一致。

### 国际化

`zh-CN.ts` 与 `en-US.ts` 同步新增 `ai.credits.*`（余额、消耗、价格表展示）文案；本期启用的错误码文案（`MODEL_NOT_SUPPORTED`、`PARAM_NOT_SUPPORTED`、`QUOTE_STALE`、`CONCURRENCY_LIMITED`、`UPSTREAM_ERROR`、`UPSTREAM_TIMEOUT` 等）按第一期定下的机制登记进扁平的 `apiErrors.*`，不新开 `ai.errors.*` 分组。删除渠道、Key、脚本相关的全部文案（渠道管理、连接测试、脚本编辑器）与 `apiErrors.*` 里靠猜 HTTP 状态码的旧条目。两个文件的 key 结构必须保持对称。


## 部署调整

### nginx

给 `/api/ai/` 单独一个 location，放在第一期的 `location /api/` 之前：

```txt
location /api/ai/ {
    proxy_pass http://api:8080;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;
    chunked_transfer_encoding on;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 20m;
}
```

三个细节容易踩。`proxy_pass` **不能带尾斜杠，也不能带任何 URI 部分**，要与第一期 `location /api/` 的 `proxy_pass http://api:8080;` 保持同一种写法，也就是都不剥前缀。写成 `http://api:8080/ai/` 时，nginx 会把 location 匹配到的 `/api/ai/` 剥掉再换成 `/ai/`，`/api/ai/images/generations` 到后端就成了 `/ai/images/generations`；而后端所有路由统一注册在 `r.Group("/api")` 下，实际路径是 `/api/ai/images/generations`，照那么写就是全线 404。不带 URI 部分则路径原样透传，与后端前缀严格对齐。`proxy_http_version 1.1` 加上清空 `Connection` 头是分块传输的前提，默认的 HTTP/1.0 不支持分块，SSE 会被整段缓冲。`client_max_body_size` 可以比第一期媒体上传的 200m 小很多，因为参考图现在传的是 `storageKey` 而不是 base64。

### 超时链路对齐

三层超时必须从外到内递减，否则最短的那一层会先断，而更内层还在继续跑：nginx 的 `proxy_read_timeout` ≥ 后端的整体超时 ≥ 上游请求超时。如果 nginx 最短，用户看到 504、后端却还在生成并且点数已经扣了，退不退款都不对。

Go 侧还有一个必踩的坑：`http.Server.WriteTimeout` 是从响应开始写起算的绝对时限，对流式响应等于「最多流这么久就掐断」。要么设为 0，要么放到大于最长的流式整体超时，真正的时限交给每个 handler 的 context 控制。`ReadHeaderTimeout` 保留用于防慢速攻击。

### 环境变量

| 变量 | 用途 |
| --- | --- |
| `AI_IMAGE_TIMEOUT` | 图像请求整体超时，缺省 180 秒 |
| `AI_AUDIO_TIMEOUT` | 语音请求整体超时，缺省 120 秒 |
| `AI_STREAM_TIMEOUT` | 文本流式整体超时，缺省 600 秒 |
| `AI_STREAM_IDLE_TIMEOUT` | 文本流式空闲超时，缺省 60 秒 |
| `AI_VIDEO_TASK_TIMEOUT` | 视频任务总时长上限，缺省 20 分钟 |
| `AI_MAX_CONCURRENCY_PER_USER` | 单用户并发上限 |
| `AI_GLOBAL_CONCURRENCY` | 全局并发上限 |
| `AI_ALLOW_PRIVATE_UPSTREAM` | 是否允许上游地址落在内网，缺省 false，仅本地调试开启 |
| `PRICING_PROMOTION_ENABLED` | 是否启用限时折扣；关闭后所有新报价只使用基础价格 |

`CREDENTIAL_MASTER_KEY` 是本期新引入的，也是全服务唯一的加密主密钥。媒体卷第一期已经建好，本期不需要新的数据卷。

## 验收清单

单一路径与计费规则：

- 用平台目录里的模型生成一张图，点数余额按 `credit_cost` 减少，流水里有一条消费记录。
- 任意页面的任意生成请求在浏览器网络面板里只打到本站 `/api/ai/*`，全程没有任何指向第三方域名的请求。
- 手工往 `/api/ai/images/generations` 里传一个目录外的模型名或带 `::` 之类的渠道前缀痕迹，返回 400 `MODEL_NOT_SUPPORTED`，没有扣点、没有调上游。
- localStorage、sessionStorage 与内存里都找不到任何 API Key。
- 全局搜索 `model-plugin`、`CHANNEL_MODEL_SEPARATOR`、`fetchChannelModels`、`encodeChannelModel` 均无残留，配置页没有任何渠道、Key 或脚本入口。

价格展示与报价：

- 图片价格表完整展示该模型的 1K/2K/4K、质量与数量价格，当前组合同时显示点数和人民币。
- 视频价格表以 480p/720p/1080p 为行、5 秒/10 秒等合法时长为列，选择任意组合后 `/api/ai/quote` 返回对应唯一价格。
- 整模型、指定分辨率、指定时长和完整组合折扣分别生效；多个规则同时匹配时只选最具体且优先级最高的一条，不叠加。
- 报价同时返回原价、活动名称、折扣、折后点数、折后人民币和活动结束时间，前端原价划线并突出折后价。
- 报价凭证的 `expiresAt` 不晚于活动结束时间；跨过结束时间后旧 token 返回 `QUOTE_STALE`，没有预扣、没有请求上游。
- 活动被停用、修改或由更具体活动覆盖后，旧 token 返回 `QUOTE_STALE`，前端展示最新价格并要求重新确认。
- 生成成功后 `ai_requests` 可查到基础价、折后价、活动 id 和版本，点数流水通过请求 id 解释本次折扣扣款。
- 活动停用或价格矩阵回滚后，已经成功预扣的请求继续使用自己的计费快照，不能被回滚动作改成另一价格。
- 无折扣报价有效期内新活动开始时，旧 token 返回 `QUOTE_STALE`；重新报价后展示更低价格，不按旧原价扣费。
- 生成成功后 `ai_requests` 可查到基础价、折后价、活动 id 和版本，点数流水通过请求 id 解释本次折扣扣款。
- 修改后台价格矩阵版本后，旧 `quoteToken` 返回 409 `QUOTE_STALE`，没有预扣、没有请求上游，前端刷新并要求用户按新价格重新确认。
- 篡改生成参数但复用旧 token 返回 `QUOTE_STALE`；篡改 token 签名同样被拒绝。
- 免费报价后由另一个并发请求先占用最后一次额度，原请求返回 `QUOTE_STALE`，不会静默改扣点数。
- 报价结果显示 `affordable=false` 和准确差额时，生成按钮禁用并展示还差多少点及人民币。

扣点一致性：

- 点数不足时返回 402 `INSUFFICIENT_CREDITS`，带 `requiredMicros`、`availableMicros` 与 `shortfallMicros`，且没有发起任何上游请求。
- 把平台渠道 Key 改错后发起生成，返回 502，点数被全额退还并写入一条 `type=refund` 流水。
- 同一个 `idempotencyKey` 连发两次，只扣一次点，第二次返回同一结果。
- 不带 `idempotencyKey` 的生成请求返回 400 `VALIDATION_FAILED`，没有扣点、没有调上游。
- 流式对话输出到一半时关掉浏览器标签页，点数不退还，服务端日志显示上游请求被取消。
- 流式对话在第一个 delta 之前上游返回 500，点数全额退还。
- 视频任务失败后点数退还，且同一个任务不会被退两次。
- 手动把退还流程打断后重试，不会出现双倍退款。
- 生成进行中直接杀掉 `api` 进程再启动，启动完成后对应的 `ai_requests` 收敛为 `failed`，被预扣的点数按原桶退还且只有一条退款流水；视频任务则在重启后继续轮询到终态，`ai_requests` 跟着收敛。

流式转发：

- 文本对话逐字出现，不是等全部生成完一次性出现。
- **这条必须在 nginx 后面测，不能直连 8080**，漏配 `proxy_buffering off` 时它才会失败。
- 上游沉默 60 秒以上时连接不被中间设备掐断，心跳生效。
- 客户端点「停止」后，服务端到上游的连接在 1 秒内关闭。
- 超长回答不会中途截断（验证行缓冲上限已经调整）。

参数与目录：

- 给平台模型传一个不在 `constraints.size` 里的尺寸，返回 400 `PARAM_NOT_SUPPORTED`，响应里列出允许值，且没有扣点、没有调上游。
- 在平台模型之间切换后，尺寸、比例、时长选项跟着变，原来选中的非法值被自动重置为合法值。
- 不同模型之间切换后，尺寸、比例、时长选项全部来自各自的 `constraints`，前端不再存在硬编码选项列表。
- 拿视频模型去调 `/api/ai/images/generations` 返回 `MODEL_NOT_SUPPORTED`。

结果落地：

- 生成的图片以 `storageKey` 返回，画布里 `<img src="/api/media/...">` 能直接显示，响应里没有任何第三方 URL。
- 只返回 URL 的视频模型同样落到 `storageKey`，等第三方 URL 过期后画布里的视频仍能播放。
- 存储超额时返回 507 `STORAGE_QUOTA_EXCEEDED` 且点数被退还。
- 未验证邮箱调用生成接口，在预扣之前就返回 403 `EMAIL_NOT_VERIFIED`。
- 用平台模型生成一张图后，`GET /api/generations?kind=image` 恰好多出一条记录，浏览器网络面板里没有 `POST /api/generations` 请求，`/api/me` 的 `imageGenerateToday` 只加一。
- 平台视频任务在创建时就出现一条 `status=pending` 的生成记录；关掉页面等任务跑完再打开，这条记录已经是 `success` 并带着产物的 `storageKey`，全程没有前端发出的 `PATCH /api/generations/{id}`。

安全：

- 让平台上游返回一个指向内网地址的结果 URL，服务端拒绝下载。
- 让结果 URL 先返回一个公网地址、再 302 跳到内网地址，服务端在这一跳上同样拒绝。
- 在管理后台把某个模型的请求故意配错，生成失败并全额退款；随后修复配置恢复正常，全程浏览器侧拿不到任何 Key。
- 后端日志里搜不到任何 API Key，`Authorization` 与 `x-goog-api-key` 均已脱敏。
- 管理后台的渠道接口不返回 Key 明文，只返回 `hasKey` 与后四位。

并发与限流：

- 同一用户并发发起超过上限的生成请求，多余的返回 429 `CONCURRENCY_LIMITED` 并带 `Retry-After`。
- **连续开关 20 次流式对话后仍能正常发起新请求**，验证槽位在断开时被正确释放，没有泄漏。
- 视频任务的并发计数与图像、文本互不影响。

前端：

- 全新注册的账号不需要任何配置就能直接生成，入口不被「未配置 API Key」挡住。
- 模型选择器只出现平台目录里的模型，每项显示预估点数。
- `bun run typecheck` 通过，`web/src/lib/seedance-video.ts` 与 `model-plugin.ts` 已删除，`image.ts` / `video.ts` / `audio.ts` 中没有残留的直连实现。
- 两个语言包 key 结构对称，新增的 `ai.*` 文案齐全，渠道与脚本相关文案及 `apiErrors.*` 猜测类条目已删除。
