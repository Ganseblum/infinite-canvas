package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// PlatformChannel 是平台自己的上游渠道，API Key 用 CREDENTIAL_MASTER_KEY 加密落库。
// 全服务只有这一把主密钥，解密后的明文只在发起上游请求的瞬间停留。
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

// AIRequest 记录一次生成请求的计费快照与状态。同步能力在进程重启后由启动收敛置为 failed。
type AIRequest struct {
	ID               uuid.UUID  `gorm:"type:char(36);primaryKey"`
	UserID           uuid.UUID  `gorm:"type:char(36);not null;index:idx_ai_request_user_created,priority:1;uniqueIndex:idx_ai_request_idempotency,priority:1"`
	IdempotencyKey   *string    `gorm:"type:varchar(40);uniqueIndex:idx_ai_request_idempotency,priority:2"`
	Capability       string     `gorm:"type:varchar(16);not null"` // image | video | audio | text
	Model            string     `gorm:"type:varchar(120);not null"`
	ChannelID        *uuid.UUID `gorm:"type:char(36)"`
	BaseCostMicros   int64      `gorm:"not null;default:0"`
	FinalCostMicros  int64      `gorm:"not null;default:0"`
	PriceVersion     int        `gorm:"not null;default:0"`
	PromotionID      *uuid.UUID `gorm:"type:char(36)"`
	PromotionVersion *int
	PromotionEnabled bool           `gorm:"not null;default:true"`
	PricingSnapshot  datatypes.JSON `gorm:"type:json"`
	// ConsumeTransactionIDs 记录本次预扣写下的消费流水 id，失败退还按这些 id 原桶退回。
	ConsumeTransactionIDs datatypes.JSON `gorm:"type:json"`
	Status                string         `gorm:"type:varchar(16);not null;index"` // running | succeeded | failed
	RefundPending         bool           `gorm:"not null;default:false"`
	UpstreamStatus        int
	DurationMs            int
	CreatedAt             time.Time `gorm:"index:idx_ai_request_user_created,priority:2,sort:desc"`
	UpdatedAt             time.Time
}

// AITask 是视频等异步能力的上游任务。服务端主动轮询，进程重启后按非终态恢复。
type AITask struct {
	ID             uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID         uuid.UUID `gorm:"type:char(36);not null;index:idx_ai_task_user,priority:1"`
	RequestID      uuid.UUID `gorm:"type:char(36);not null"`
	Provider       string    `gorm:"type:varchar(16);not null"`
	UpstreamTaskID string    `gorm:"type:varchar(200);not null"`
	Status         string    `gorm:"type:varchar(16);not null;index:idx_ai_task_status_updated,priority:1"` // pending | succeeded | failed
	GenerationID   uuid.UUID `gorm:"type:char(36)"`
	StorageKey     string    `gorm:"type:varchar(80)"`
	Error          string    `gorm:"type:text"`
	PollFailures   int       `gorm:"not null;default:0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time `gorm:"index:idx_ai_task_status_updated,priority:2"`
}

// PollAfterMs 返回该任务建议的轮询间隔。不同厂商的合理间隔差别很大，
// 这个知识跟着 provider 走，前端不再硬编码。
func (t AITask) PollAfterMs() int {
	switch t.Provider {
	case "gemini":
		return 10000
	default:
		return 5000
	}
}
