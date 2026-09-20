package office

import (
	"encoding/json"
	"time"
)

// 事件类型枚举（spec §2.2）。预留类型本期不发送；收到未知 type 一律忽略（向前兼容）。
const (
	EventRunStarted = "run_started"
	EventDelta      = "delta"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventArtifact   = "artifact"
	EventUsage      = "usage"
	EventNotice     = "notice"
	EventDone       = "done"
	EventError      = "error"
)

// run 状态机取值（spec §4：仅 Go 可写）。
const (
	RunQueued    = "queued"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// 终态错误码（spec §5）。
const (
	CodeRuntimeUnreachable = "runtime_unreachable"
	CodeRuntimeLost        = "runtime_lost"
	CodeOrphanReclaimed    = "orphan_reclaimed"
	CodeCancelled          = "cancelled"
	CodeRunConflict        = "run_conflict"
	CodeCreditsExhausted   = "credits_exhausted"
	CodeEventsExpired      = "events_expired"
)

// Envelope 是契约一与契约二共用的事件信封（spec §2.1，冻结）。
// seq 在契约二方向是 Runtime 的临时编号，Go 落库时重编；契约一方向恒为落库 seq。
type Envelope struct {
	V       int             `json:"v"`
	Seq     int             `json:"seq"`
	Type    string          `json:"type"`
	RunID   string          `json:"runId"`
	TS      int64           `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// newEnvelope 构造落库用信封：v 恒 1，ts 取 Go 落库时刻毫秒，payload 原样透传。
func newEnvelope(runID, eventType string, payload json.RawMessage) Envelope {
	return Envelope{V: 1, Type: eventType, RunID: runID, TS: time.Now().UnixMilli(), Payload: payload}
}

// isTerminalEvent 判断事件是否为终态事件（此后无事件，spec §2.2）。
func isTerminalEvent(eventType string) bool { return eventType == EventDone || eventType == EventError }

// donePayload 是 done 事件的用量字段（M1 计费数据源）。
type donePayload struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// startPayload 是 run_started 事件的模型信息；回写 office_runs.model 供管理面按模型统计。
type startPayload struct {
	Model     string `json:"model"`
	AgentName string `json:"agentName"`
}

// errorPayload 是 error 事件的错误字段。
type errorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

// messageContent 是 office_messages.content 的存储形态（schema_version 与协议版本独立管理）。
type messageContent struct {
	SchemaVersion int    `json:"schema_version"`
	Text          string `json:"text"`
}

const messageSchemaVersion = 1
