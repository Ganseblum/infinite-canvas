package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Credit 一个用户一行，双桶余额与付费权益截止时间。
// 不变量：purchased_micros 等于该用户 bucket=purchased 的流水之和，granted 同理。
type Credit struct {
	UserID          uuid.UUID `gorm:"type:char(36);primaryKey"`
	PurchasedMicros int64     `gorm:"not null;default:0"`
	GrantedMicros   int64     `gorm:"not null;default:0"`
	PaidUntil       *time.Time
	UpdatedAt       time.Time
}

type CreditTransaction struct {
	ID                    uuid.UUID  `gorm:"type:char(36);primaryKey"`
	UserID                uuid.UUID  `gorm:"type:char(36);index;not null"`
	Bucket                string     `gorm:"type:varchar(16);not null"` // purchased | granted
	Type                  string     `gorm:"type:varchar(16);not null"` // purchase | consume | refund | grant | expire
	AmountMicros          int64      `gorm:"not null"`
	BalanceAfterMicros    int64      `gorm:"not null"`
	RefType               string     `gorm:"type:varchar(16)"`
	RefID                 string     `gorm:"type:varchar(64);index"`
	RefundOfTransactionID *uuid.UUID `gorm:"type:char(36);uniqueIndex"`
	Note                  string     `gorm:"type:varchar(200)"`
	CreatedAt             time.Time  `gorm:"index"`
}

// UsageRecord 用量计数，按 (user_id, metric, period) 唯一，period 固定 total。
type UsageRecord struct {
	UserID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_usage_user_metric,priority:1"`
	Metric    string    `gorm:"type:varchar(32);primaryKey;uniqueIndex:idx_usage_user_metric,priority:2"`
	Period    string    `gorm:"type:varchar(16);primaryKey;uniqueIndex:idx_usage_user_metric,priority:3"`
	Value     int64     `gorm:"not null;default:0"`
	UpdatedAt time.Time
}

type CreditPackage struct {
	ID              string `gorm:"primaryKey;type:varchar(32)"`
	Name            string `gorm:"type:varchar(80);not null"`
	PriceMicros     int64  `gorm:"not null"`
	BonusMicros     int64  `gorm:"not null;default:0"`
	EntitlementDays int    `gorm:"not null;default:30"`
	Currency        string `gorm:"type:varchar(8);not null;default:CNY"`
	Enabled         bool   `gorm:"not null;default:true"`
	Sort            int    `gorm:"not null;default:0"`
}

type Order struct {
	ID              uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID          uuid.UUID `gorm:"type:char(36);index:idx_order_user_created,priority:1;index:idx_order_status_created,priority:1;not null"`
	Provider        string    `gorm:"type:varchar(16);not null"` // alipay | wechat
	ProviderOrderID *string   `gorm:"type:varchar(128);uniqueIndex"`
	PackageID       string    `gorm:"type:varchar(32);not null"`
	PriceMicros     int64     `gorm:"not null"`
	Currency        string    `gorm:"type:varchar(8);not null"`
	PurchasedMicros int64     `gorm:"not null"`
	GrantedMicros   int64     `gorm:"not null"`
	EntitlementDays int       `gorm:"not null"`
	Status          string    `gorm:"type:varchar(16);not null;default:pending;index:idx_order_status_created,priority:2"`
	PaidAt          *time.Time
	CreatedAt       time.Time `gorm:"index:idx_order_user_created,priority:2,sort:desc"`
	UpdatedAt       time.Time
}

// ModelCatalog 平台模型目录，是前端唯一的模型来源。
// constraints 与 credit_cost 的键集与第四期转发接口共用，改一侧必须同步另一侧。
type ModelCatalog struct {
	ID                uuid.UUID      `gorm:"type:char(36);primaryKey"`
	Name              string         `gorm:"type:varchar(120);not null;uniqueIndex"`
	DisplayName       string         `gorm:"type:varchar(120);not null"`
	Capability        string         `gorm:"type:varchar(16);not null;index"` // image | video | text | audio
	Provider          string         `gorm:"type:varchar(60);not null"`
	Constraints       datatypes.JSON `gorm:"type:json"`
	CreditCost        datatypes.JSON `gorm:"type:json"`
	ChannelIDs        datatypes.JSON `gorm:"type:json"` // 有序的平台渠道 id 数组，顺序即故障转移顺序
	FreeTrialEligible bool           `gorm:"not null;default:false;index"`
	Enabled           bool           `gorm:"not null;default:true"`
	Sort              int            `gorm:"not null;default:0"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ModelPricePromotion struct {
	ID          uuid.UUID      `gorm:"type:char(36);primaryKey"`
	ModelID     uuid.UUID      `gorm:"type:char(36);not null;index"`
	Name        string         `gorm:"type:varchar(120);not null"`
	MatchParams datatypes.JSON `gorm:"type:json;not null"`
	DiscountBPS int            `gorm:"not null"` // 8000 = 8 折
	Priority    int            `gorm:"not null;default:0"`
	Version     int            `gorm:"not null;default:1"`
	Status      string         `gorm:"type:varchar(16);not null;default:draft;index"` // draft | scheduled | active | ended | disabled
	StartsAt    time.Time      `gorm:"not null"`
	EndsAt      time.Time      `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AdminAuditLog 管理操作审计，只能追加。摘要只保存脱敏后的价格、折扣、状态与版本。
type AdminAuditLog struct {
	ID            uuid.UUID      `gorm:"type:char(36);primaryKey"`
	ActorUserID   uuid.UUID      `gorm:"type:char(36);index;not null"`
	Action        string         `gorm:"type:varchar(64);not null;index"`
	TargetType    string         `gorm:"type:varchar(32);not null"`
	TargetID      string         `gorm:"type:varchar(64);not null;index"`
	RequestID     string         `gorm:"type:varchar(64)"`
	BeforeSummary datatypes.JSON `gorm:"type:json"`
	AfterSummary  datatypes.JSON `gorm:"type:json"`
	Reason        string         `gorm:"type:varchar(200)"`
	CreatedAt     time.Time      `gorm:"index"`
}
