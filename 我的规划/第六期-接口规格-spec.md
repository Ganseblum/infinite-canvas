# 第六期接口规格：AI 办公助理（Agent 产品线）— Spec

> 文档类型：Spec（接口与数据规格，字段语义冻结）。上游：`第六期-提案-proposal.md`（范围）、`第六期-技术设计-design.md`（机制）。执行任务板见 `records/plans/PLAN-OFFICE-AGENT.md`。
> 状态：待评审；「边界值」章节数值均为待报批默认值，报批后冻结。本文未覆盖的字段语义以实现时的 Go 结构体注释为准并回填本文（禁止实现与规格双向漂移不记录）。

## 0. 冻结声明与版本

- 契约一（前端↔Go）：`/api/v1/office/**`，SSE 事件信封 `v:1`。
- 契约二（Go↔Runtime）：`/v1/**`（office-agent 内网端口），同信封格式。
- 协议版本与消息存储版本（`office_messages.content.schema_version`）**独立管理**；本文升级须追加变更记录，向后不兼容变更升 `v` 并双写过渡。
- 判定规则（§7）与边界场景（§8）为冻结项：实现与测试以本文为准。

## 1. 契约一：HTTP API（前端 ↔ Go）

### 1.1 通用约定

- Base：`/api/v1/office`；鉴权：登录态 + 权限点（读 `office.read`，写 `office.write`）。
- 错误形状（沿用平台既有约定）：`{"code": "<错误码>", "message": "<用户可读>", "detail": {…?}}`。
- 分页：`?cursor=&limit=`（默认 20，最大 100），响应 `{"items":[…],"next_cursor":"…?"}`。
- 幂等：写操作可携带 `clientMsgId`（uuid v4）；同键重放返回首次结果。

### 1.2 端点清单

| # | 端点 | 语义 | 期次 |
|---|---|---|---|
| A1 | `POST /sessions` | 创建会话（body: `agentId?`，缺省用默认智能体） | M1 |
| A2 | `GET /sessions` | 会话列表（按 updated_at 倒序） | M1 |
| A3 | `GET /sessions/:id` | 会话详情（含 activeRunId） | M1 |
| A4 | `GET /sessions/:id/messages` | 消息快照（权威；`?afterTurnId=` 增量） | M1 |
| A5 | `POST /sessions/:id/messages` | 发消息起 run（见 §1.3） | M1 |
| A6 | `GET /sessions/:id/runs/:runId/stream` | SSE 事件流（见 §2；Last-Event-ID 重放） | M1 |
| A7 | `POST /runs/:id/cancel` | 取消（幂等；queued/running 之外返回 200 幂等成功） | M1 |
| A8 | `GET /sessions/:id/artifacts` | artifact 列表 | M2 |
| A9 | `GET /artifacts/:id` | artifact 详情（kind=html 返回源码视图标记） | M2 |
| A10 | `GET /artifacts/:id/download` | 下载（`Content-Disposition: attachment` + 签名 URL 跳转） | M2 |
| A11 | `POST /files/upload-url` / `POST /files/complete` | 附件 presigned 上传（storage 域包装） | M2 |
| A12 | `GET /agents` / `GET /skills` | 可用智能体/技能列表（能力位随 agent 返回） | M3 |

### 1.3 A5 发消息（核心写路径）

请求：

```json
{ "content": "整理成本周周报", "clientMsgId": "0e0c…-uuid", "attachmentIds": ["f_…"] }
```

响应（201）：

```json
{ "runId": "r_01J…", "sessionId": "s_01J…", "status": "queued", "clientMsgId": "0e0c…-uuid" }
```

| 场景 | 状态码 | code | 语义 |
|---|---|---|---|
| 正常 | 201 | — | run 已排队；SSE 从 A6 订阅 |
| 幂等重放（同 clientMsgId） | 200 | — | 返回既有 run（不新建） |
| 同会话已有活动 run（不同 clientMsgId） | 409 | `run_conflict` | 前端提示「上一条还在执行」 |
| 点数不足 | 400 | `credits_exhausted` | M1 预检 |
| 触发限流 | 429 | `rate_limited` | 每日消息上限 |
| 无权限 / 未登录 | 403 / 401 | 沿用平台错误码 | — |

## 2. SSE 事件规格（契约一与契约二共用信封）

### 2.1 信封（冻结）

```json
{ "v": 1, "seq": 42, "type": "delta", "runId": "r_01J…", "ts": 1726771200000, "payload": { } }
```

| 字段 | 类型 | 语义 |
|---|---|---|
| v | int | 协议版本，当前 1 |
| seq | int | run 内单调递增；Go 落库时分配；`UNIQUE(run_id, seq)` |
| type | enum | 见 §2.2 |
| runId | string | 所属 run |
| ts | int64 | Go 落库时刻毫秒 |
| payload | object | 按 type 定义（§2.2） |

### 2.2 事件类型与 payload

| type | payload 字段 | 语义 | 期次 |
|---|---|---|---|
| run_started | model, agentName | run 开始（Go 据此迁移 queued→running） | M1 |
| delta | text | 增量文本，按 seq 顺序拼接 | M1 |
| tool_call | toolCallId, name, inputPreview?, status="running" | 工具开始 | M2 |
| tool_result | toolCallId, status("ok"\|"error"), isError, outputPreview? | 工具结束 | M2 |
| artifact | artifactId, kind("markdown"\|"code"\|"html"\|"file"), name, size | 产物产生 | M2 |
| usage | inputTokens, outputTokens（累计） | run 中用量（M3 计费数据源；前端不展示） | M3 |
| notice | level("info"\|"warn"), text | 运行时提示（降级/环境说明） | M1 |
| done | inputTokens, outputTokens, credits | 终态：成功；此后无事件 | M1 |
| error | code, message?, retryable | 终态：失败/取消（code 见 §5）；此后无事件 | M1 |
| quota_exhausted | usedCredits | 点数耗尽（随后 error 收尾） | M3 |
| plan_updated / clarification / task_tool_call / task_tool_result / sandbox_queue_status / environment_rebuilt | — | **预留，本期不发送**；客户端收到未知 type 必须忽略（向前兼容规则） | 预留 |

### 2.3 A6 SSE 订阅与重放（冻结）

- 重连载体：客户端显式 `?lastSeq=<seq>`（前端为 fetch-stream 客户端——平台 Bearer 鉴权不可用原生 EventSource，见前端方案 D1；服务端需同时接受查询参数与 SSE `Last-Event-ID` 头，语义等价，优先实现查询参数）。
- `lastSeq` 大于该 run 清理水位：重放 `seq > lastSeq` 全部事件；run 已终态则重放至末尾补发 `done/error`。
- `lastSeq` 落在已清理区间：HTTP **410** + `{"code":"events_expired"}` + 一条终态事件；客户端转 A4 快照重建。
- run 不存在/无权限：404/403（不复用 410）。

## 3. 契约二：Runtime API（Go ↔ office-agent，内网）

认证：所有请求须带头 `X-Office-Internal-Token: <共享密钥>`；缺失或不匹配返回 401，Go 侧按 E3/E4 处理。

| # | 端点 | 语义 |
|---|---|---|
| B1 | `POST /v1/runs` | 起 run。body：`{ runId, sessionId, message:{role:"user",content,attachments:[{key,mime,name}]}, agent:{ systemPrompt, model, tools[], skills[] } }`；响应：SSE（信封同 §2，seq 由 **Runtime 临时编号**，Go 落库时重编） |
| B2 | `POST /v1/runs/:id/cancel` | 取消；未知 runId 返回 200（幂等） |
| B3 | `GET /v1/runs/:id` | `{ exists, phase:"idle"\|"starting"\|"running", sessionId }`（E13 对账用） |
| B4 | `GET /healthz` | `{ ok, sdkVersion }` |

约束：B1 的 `sessionId` 仅用于推导工作区 `/data/workspaces/{sessionId}/`（正则 `^[a-z0-9][a-z0-9-]{17,39}$`，不匹配直接 400）；Runtime 不接受任何客户端路径；`resume` 由 Runtime 依据 sessionId 定位 transcript 文件（落盘于工作区 `../transcripts/{sessionId}.jsonl`）。

## 4. 数据字典（字段级，MySQL 8.4）

| 表.字段 | 类型 | 约束/语义 |
|---|---|---|
| office_sessions.id | varchar(24) | PK，`s_` 前缀 ULID |
| office_sessions.user_id | bigint | 索引 (user_id, updated_at) |
| office_sessions.agent_id | varchar(24) | FK→office_agents.id |
| office_sessions.active_run_id | varchar(24) NULL | **可空唯一索引**（并发闸）；run 终态时清空 |
| office_sessions.workspace_path | varchar(255) | `{sessionId}` 相对段（卷内绝对路径由部署配置拼装） |
| office_runs.id | varchar(24) | PK，`r_` 前缀 ULID |
| office_runs.session_id | varchar(24) | 索引 (session_id, created_at) |
| office_runs.status | enum | queued/running/succeeded/failed/cancelled；**仅 Go 可写** |
| office_runs.error_code | varchar(40) NULL | 见 §5 |
| office_runs.client_msg_id | varchar(36) | UNIQUE(session_id, client_msg_id) |
| office_runs.tokens_in/out | int | Runtime usage 汇总 |
| office_runs.credits | decimal(12,4) | 本次扣点 |
| office_events.id | bigint AUTO_INCREMENT | PK |
| office_events.run_id/seq | varchar(24)/int | UNIQUE(run_id, seq) |
| office_events.type/payload | varchar(32)/JSON | §2.2；payload JSON 原样存 |
| office_messages.id | varchar(24) | PK（即 itemId） |
| office_messages.role | enum | user/assistant |
| office_messages.content | JSON | `{ "schema_version": 1, "text": "…", "blocks":[…]? }` |
| office_messages.thread_id / turn_id | varchar(24) | = session_id / run_id（仓库归属键规则） |
| office_artifacts.kind | enum | markdown/code/html/file |
| office_artifacts.storage_key | varchar(255) | storage 域对象键 |

## 5. 错误码总表（冻结）

| code | HTTP | 方向 | 触发 |
|---|---|---|---|
| run_conflict | 409 | 契约一 | 同会话并发提交（E2） |
| credits_exhausted | 400 | 两契约 | 预检不过（E5）/ run 中耗尽（随后 quota_exhausted 事件） |
| rate_limited | 429 | 契约一 | 消息数/并发超限 |
| runtime_unreachable | —（run 终态 error 事件） | 事件 | E3 |
| runtime_lost | — | 事件 | E4 |
| stalled | — | 事件 | E6 |
| upstream_timeout | — | 事件 | E8 |
| orphan_reclaimed | — | 事件 | E13 |
| cancelled | — | 事件 | KP-3 正常取消（retryable=false） |
| events_expired | 410 | 契约一 | 重放空洞（E11） |
| invalid_session_id | 400 | 契约二 | sessionId 不匹配格式 |

## 6. 边界值（待报批后冻结；报批前实现一律引用此处变量，不硬编码）

沿用总览「边界值默认值表」全部 12 项（起跑 10s / 静默 120s / 上游 120s / cancel ≤2s / cancel 转发 5s / 孤儿 TTL 10min / 事件保留 7 天 / 并发 run M1=1 / 每日消息 50 / usage 间隔 30s / nginx 600s / 附件沿用 storage 现值）。数值调整只改配置与该表，不改逻辑分支。

## 7. 判定规则（冻结）

1. **快照权威**：`office_messages` 是历史事实唯一权威；SSE 事件只用于未物化 turn 的实时补充；两者冲突以快照为准。
2. **终态唯一写者**：run 状态只由 Go 写；Runtime/前端对终态的任何认知仅是缓存。
3. **seq 胜出**：同 run 事件乱序到达时按 seq 排序消费；payload 不携带可覆盖 seq 的顺序信息。
4. **历史只展示不推断**：历史 run 的 usage/状态仅用于展示与计费对账，不参与当前资格判定（如并发闸只看 active_run_id 实时值）。
5. **物化不可逆**：assistant 消息物化后，重放事件不得改写其文本（过程卡片可恢复）。

## 8. 边界场景清单（测试设计输入）

| 类 | 场景 | 预期 |
|---|---|---|
| just-succeeded | cancel 请求与 done 事件竞态到达 | 终态取先落库者；后到 cancel 幂等 200 |
| just-expired | Last-Event-ID 恰等于清理水位 | 410 + 终态事件（≥ 水位即视为已清理） |
| empty | 新会话立即 GET messages / stream | 空列表 / 等待首事件的空 SSE（不报错） |
| invalid enum | A5 传不存在 attachmentId / agentId | 400 `invalid_param` |
| dirty history | run 无 done/error 即进程双亡 | TTL 扫描置 failed(orphan_reclaimed)，用户可重试 |
| 并发 | 同 clientMsgId 双发竞态 | 唯一键兜底，返回同 run（两请求都可能 201/200，语义一致） |
| 乱序 | Runtime 事件到达顺序异常 | Go 按 seq 重排落库；非法 seq 跳跃→记 notice 并按到达序落库（seq 由 Go 重编，实际不跳跃） |
| 净化 | artifact 含 `<script>`/`onerror` | 预览不执行（M2 验收断言） |

## 9. 未决问题登记（OQ）

| # | 问题 | 阻塞对象 | 状态 |
|---|---|---|---|
| OQ-1 | 首发模型组合（决策项 2） | M1 模型路由配置 | 待用户 |
| OQ-2 | `/office` 落位（决策项 3） | 前端任务 T-T04 | 待用户（默认现有 web） |
| OQ-3 | AskUserQuestion 是否提前 M2（决策项 6） | M2 范围 | 待用户 |
| OQ-4 | 生成反馈并入 M3（决策项 7） | M3 范围 | 待用户 |
| OQ-5 | 边界值表报批（决策项 8） | 全部边界值冻结 | 待用户 |
| OQ-6 | SDK resume/事件映射/bubblewrap 假设（M0 spike 出口） | M1 开工 | M0 验证 |
| OQ-7 | GPT 评测任务集清单细化（20 题构成） | M3 门禁执行 | M3 前定 |
| OQ-8 | 自建搜索工具的上游选型 | M2 T-T11 | M2 前定 |
