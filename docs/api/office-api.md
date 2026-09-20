# AI 办公助理 接口文档（M1）

> 机器可读规范：[`docs/api/openapi.yaml`](./openapi.yaml)（OpenAPI 3.0.3，标签「办公助理」「Admin-办公助理」，可直接导入 Apifox）。
> 本文是人类可读版，字段以 spec 与 OpenAPI 为准；本文示例来自 2026-09-20 真实联调。

## 通用约定

- 用户面前缀 `/api/v1/office`，管理面前缀 `/api/admin/office`；Bearer JWT 鉴权（`Authorization: Bearer <accessToken>`）。
- 错误信封：`{"error":{"code":"...","message":"..."}}`；时间 RFC3339Nano UTC。
- SSE 事件信封：`{"v":1,"seq":N,"type":"...","runId":"...","ts":ms,"payload":{...}}`，逐事件 `data: <json>\n\n`，`id:` 字段即 seq；协议版本 `v` 与消息存储版本（content.schemaVersion）独立管理。

## 用户面（/api/v1/office）

| 方法 | 路径 | 说明 | 状态码要点 |
|---|---|---|---|
| POST | `/sessions` | 创建会话（agentId 可省略用默认智能体） | 201 |
| GET | `/sessions?cursor=&limit=` | 会话列表（updatedAt 倒序） | 200 |
| GET | `/sessions/{id}` | 会话详情（含 activeRunId） | 200/404 |
| GET | `/sessions/{id}/messages?afterTurnId=` | 消息快照（**权威数据源**） | 200 |
| POST | `/sessions/{id}/messages` | 发消息起 run；body `{content, clientMsgId, attachmentIds?}` | 201/200 幂等/400 credits_exhausted/409 run_conflict |
| GET | `/sessions/{id}/runs/{runId}/stream?lastSeq=` | SSE 事件流（断线重放） | 200/410 events_expired |
| POST | `/runs/{runId}/cancel` | 取消（幂等） | 200 |
| GET | `/sessions/{id}/artifacts` · `/artifacts/{id}` | 产物列表/详情（M2 起有数据） | 200 |

### 幂等与并发

- `clientMsgId` 是幂等键：同键重放返回**首次**结果（201/200 语义一致）。
- 同一会话同时只允许一个活动 run：并发提交返回 `409 run_conflict`。

### SSE 事件类型（M1）

| type | payload | 说明 |
|---|---|---|
| run_started | `{model, agentName}` | run 开始；Go 据此迁移 queued→running |
| delta | `{text}` | 增量文本，按 seq 顺序拼接 |
| tool_call | `{toolCallId, name, inputPreview?, status:"running"}` | 工具开始（inputPreview 截断 200 字符） |
| tool_result | `{toolCallId, status:"ok"\|"error", isError, outputPreview?}` | 工具结束 |
| artifact | `{artifactId, kind, name, size}` | 产物产生（M2 起出现） |
| notice | `{level:"info"\|"warn", text}` | 运行提示（如 resume 降级说明） |
| usage | `{inputTokens, outputTokens}` | 累计用量（M3，前端不展示） |
| done | `{inputTokens, outputTokens, credits}` | **终态**：成功 |
| error | `{code, message?, retryable}` | **终态**：失败/取消（code: cancelled / stalled / runtime_lost / runtime_unreachable / upstream_timeout / orphan_reclaimed） |
| quota_exhausted | `{usedCredits}` | 点数耗尽（随后 error 收尾） |

未知 `type` 必须静默忽略（向前兼容）。终态事件之后不再有事件。重放：`?lastSeq=N`（等价 Last-Event-ID 头）；`lastSeq` 落在已清理区间返回 `410 {"error":{"code":"events_expired"}}`，客户端应改用消息快照重建。

### curl 示例（真实联调摘录）

```bash
# 1. 登录拿 token
curl -s -X POST https://work.youc.online/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"account":"user@example.com","password":"***"}'

# 2. 建会话
curl -s -X POST https://work.youc.online/api/v1/office/sessions \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' -d '{}'
# → {"id":"s01m2xbwppjz7w7zf7vxpbv","title":"","activeRunId":"",...}

# 3. 发消息（幂等键）
curl -s -X POST https://work.youc.online/api/v1/office/sessions/$SID/messages \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"content":"用一句话介绍你自己","clientMsgId":"cmsg-0001"}'
# → {"runId":"r01m2xc36jg86f3hpa4mc72","sessionId":"...","status":"queued"}

# 4. 订阅事件流
curl -s -N "https://work.youc.online/api/v1/office/sessions/$SID/runs/$RID/stream?lastSeq=0" \
  -H "Authorization: Bearer $TOKEN"
# → data: {"v":1,"seq":1,"type":"run_started",...}
# → data: {"v":1,"seq":2,"type":"delta","payload":{"text":"我是"}}
# → data: {"v":1,"seq":32,"type":"done","payload":{"inputTokens":109,"outputTokens":52,...}}

# 5. 取消
curl -s -X POST https://work.youc.online/api/v1/office/runs/$RID/cancel -H "Authorization: Bearer $TOKEN"
```

## 管理面（/api/admin/office，权限 `office.read`）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/sessions?userId=&status=&cursor=&limit=` | 全站会话列表（含 userEmail、messagesCount） |
| GET | `/sessions/{id}` | 会话详情 + 全部消息（客服查看用户对话，只读） |
| GET | `/stats?days=7\|30` | 统计：runsByStatus（成功率口径 succeeded/(succeeded+failed+cancelled)）、runsByModel Top10、toolCalls Top10（按 tool_call 事件聚合）、topUsers Top10、activeUsers/messagesTotal/sessionsTotal |

## 错误码（office 专属）

| code | HTTP | 触发 |
|---|---|---|
| credits_exhausted | 400 | 点数预检不过 / run 中耗尽 |
| run_conflict | 409 | 同会话并发提交 |
| rate_limited | 429 | 每日消息上限（待边界值报批后启用） |
| events_expired | 410 | 重放命中已清理区间 |
| invalid_session_id | 400 | Runtime 侧 sessionId 格式非法 |
| cancelled / stalled / runtime_lost / runtime_unreachable / upstream_timeout / orphan_reclaimed | —（SSE error 事件） | run 终态错误，见上表 |
