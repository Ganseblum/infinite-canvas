// Package office AI 办公助理编排域（第六期 M1）：会话 / run / 事件 / 消息 / 产物
// 的落库与编排，契约一（/api/v1/office，前端↔Go）与契约二（office-agent 内网，Go↔Runtime）。
// 机制基线：我的规划/第六期-技术设计-design.md；字段级基线：第六期-接口规格-spec.md §4。
// run 状态只有 Go 可写（spec §7-2）；事件 seq 由 Go 落库时分配并 UNIQUE(run_id, seq)。
package office

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// OfficeSession 会话。active_run_id 是同会话串行闸（可空唯一索引）：run 终态时清空，
// 非空即表示有活动 run，A5 再提交返回 409 run_conflict。
// agent_id M1 无 office_agents 表（M3 落地），先存原值、不建外键。
type OfficeSession struct {
	ID            string    `gorm:"type:varchar(24);primaryKey;comment:会话主键，s_ 前缀 + 22 位时间可排序随机串"`
	UserID        string    `gorm:"type:char(36);not null;index:idx_office_sessions_user_updated,priority:1;comment:所属平台账号（platform_users.id）；spec 写 bigint，按平台 uuid 主键现状取 char(36)"`
	AgentID       string    `gorm:"type:varchar(24);not null;default:'';comment:智能体 id，M1 无 agents 表仅透传存储，M3 建 office_agents 后接入"`
	Title         string    `gorm:"type:varchar(255);not null;default:'';comment:会话标题，前端可改"`
	WorkspacePath string    `gorm:"type:varchar(255);not null;comment:工作区相对段，M1 即 {sessionId}，卷内绝对路径由部署配置拼装"`
	ActiveRunID   *string   `gorm:"type:varchar(24);uniqueIndex:uq_office_sessions_active_run;comment:当前活动 run id，可空唯一；run 终态时清空，是并发闸的事实源"`
	Status        string    `gorm:"type:varchar(16);not null;default:active;comment:会话状态，M1 恒为 active"`
	CreatedAt     time.Time `gorm:"comment:创建时间"`
	UpdatedAt     time.Time `gorm:"index:idx_office_sessions_user_updated,priority:2,sort:desc;comment:最近更新时间，列表按它倒序"`
}

func (OfficeSession) TableName() string { return "office_sessions" }

// OfficeRun 一次执行。status 仅 Go 可写；client_msg_id 是 A5 幂等键。
type OfficeRun struct {
	ID             string     `gorm:"type:varchar(24);primaryKey;comment:run 主键，r_ 前缀 + 22 位时间可排序随机串"`
	SessionID      string     `gorm:"type:varchar(24);not null;index:idx_office_runs_session_created,priority:1;uniqueIndex:uq_office_runs_session_msg,priority:1;comment:所属会话"`
	Status         string     `gorm:"type:varchar(16);not null;default:queued;index:idx_office_runs_status_updated,priority:1;comment:queued/running/succeeded/failed/cancelled，仅 Go 可写"`
	Model          string     `gorm:"type:varchar(120);not null;default:'';comment:模型标识，M1 随契约二请求透传，首发组合待 OQ-1 拍板"`
	ErrorCode      *string    `gorm:"type:varchar(40);comment:失败/取消错误码，见 spec §5，成功为空"`
	ClientMsgID    string     `gorm:"type:varchar(36);not null;uniqueIndex:uq_office_runs_session_msg,priority:2;comment:A5 幂等键"`
	TokensIn       int        `gorm:"not null;default:0;comment:输入 token 累计，来自 done/usage 事件"`
	TokensOut      int        `gorm:"not null;default:0;comment:输出 token 累计，来自 done/usage 事件"`
	Credits        *float64   `gorm:"type:decimal(12,4);comment:本次应扣点数，终态事务写入；NULL 表示未到终态"`
	CreditsCharged bool       `gorm:"not null;default:false;comment:扣点是否已入账（幂等闸），platform/billing 无 run_id 幂等键，由本列保证恰好一次"`
	StartedAt      *time.Time `gorm:"comment:开始时间，run_started 到达时由 Go 写入"`
	FinishedAt     *time.Time `gorm:"comment:终态时间，finalizeRun 写入"`
	CreatedAt      time.Time  `gorm:"index:idx_office_runs_session_created,priority:2,sort:desc;comment:创建时间"`
	UpdatedAt      time.Time  `gorm:"index:idx_office_runs_status_updated,priority:2;comment:最近状态变更时间，孤儿扫描按它判 TTL"`
}

func (OfficeRun) TableName() string { return "office_runs" }

// OfficeEvent 契约一/二共用信封的落库形态。payload JSON 原样透传，
// Go 侧只解析 type/runId/seq 与终态事件的少量字段（M1 裁决）。
type OfficeEvent struct {
	ID        int64          `gorm:"primaryKey;autoIncrement;comment:自增主键"`
	RunID     string         `gorm:"type:varchar(24);not null;uniqueIndex:uq_office_events_run_seq,priority:1;comment:所属 run"`
	Seq       int            `gorm:"not null;uniqueIndex:uq_office_events_run_seq,priority:2;comment:run 内单调递增，Go 落库时分配"`
	Type      string         `gorm:"type:varchar(32);not null;comment:事件类型，见 spec §2.2，未知类型客户端必须忽略"`
	Payload   datatypes.JSON `gorm:"type:json;comment:事件负载 JSON 原样透传"`
	CreatedAt time.Time      `gorm:"comment:落库时间，即信封 ts 毫秒来源"`
}

func (OfficeEvent) TableName() string { return "office_events" }

// OfficeMessage 消息快照（历史事实唯一权威，spec §7-1）。归属键：
// thread_id=session_id、turn_id=run_id、id 即 itemId（仓库 agent 消息硬规则）。
type OfficeMessage struct {
	ID        string         `gorm:"type:varchar(24);primaryKey;comment:消息主键即 itemId，m_ 前缀"`
	SessionID string         `gorm:"type:varchar(24);not null;index:idx_office_messages_session,priority:1;comment:所属会话"`
	RunID     string         `gorm:"type:varchar(24);not null;comment:触发本条消息的 run"`
	Role      string         `gorm:"type:varchar(16);not null;comment:user 或 assistant"`
	Content   datatypes.JSON `gorm:"type:json;comment:{schema_version:1,text,blocks?}，存储版本与协议版本独立管理"`
	ThreadID  string         `gorm:"type:varchar(24);not null;comment:= session_id"`
	TurnID    string         `gorm:"type:varchar(24);not null;index:idx_office_messages_session,priority:2;comment:= run_id，A4 afterTurnId 按它做可排序增量"`
	CreatedAt time.Time      `gorm:"comment:创建时间，快照按它升序回放"`
}

func (OfficeMessage) TableName() string { return "office_messages" }

// OfficeArtifact 产物登记（M1 只有表与 A8/A9 读端点，产生方是 M2 的 artifact 事件）。
type OfficeArtifact struct {
	ID         string    `gorm:"type:varchar(24);primaryKey;comment:产物主键，a_ 前缀"`
	SessionID  string    `gorm:"type:varchar(24);not null;index:idx_office_artifacts_session;comment:所属会话"`
	RunID      string    `gorm:"type:varchar(24);not null;index;comment:产生本产物的 run"`
	Kind       string    `gorm:"type:varchar(16);not null;comment:markdown/code/html/file"`
	Name       string    `gorm:"type:varchar(255);not null;comment:展示名"`
	StorageKey string    `gorm:"type:varchar(255);not null;comment:storage 域对象键"`
	Size       int64     `gorm:"not null;default:0;comment:字节数"`
	Mime       string    `gorm:"type:varchar(120);not null;default:'';comment:MIME 类型"`
	CreatedAt  time.Time `gorm:"comment:创建时间"`
}

func (OfficeArtifact) TableName() string { return "office_artifacts" }

// Migrate 对 office 域五张表执行 AutoMigrate（M1 只建这五张；
// office_agents/files/skills 留 M3）。双方言：列名避开 MySQL 保留字、
// 字符串列显式长度，SQLite 下类型提示被忽略。
func Migrate(gormDB *gorm.DB) error {
	return gormDB.AutoMigrate(
		&OfficeSession{},
		&OfficeRun{},
		&OfficeEvent{},
		&OfficeMessage{},
		&OfficeArtifact{},
	)
}
