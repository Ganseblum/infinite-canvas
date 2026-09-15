package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ModerationRecord 是一次审核的事实记录。
// 只保存内容哈希、标签与服务商摘要，不保存原始提示词与媒体；原件只放短期隔离区。
type ModerationRecord struct {
	ID                  uuid.UUID      `gorm:"type:char(36);primaryKey"`
	UserID              uuid.UUID      `gorm:"type:char(36);not null;index:idx_moderation_user_created,priority:1"`
	GenerationID        *uuid.UUID     `gorm:"type:char(36)"`
	MediaFileID         *uuid.UUID     `gorm:"type:char(36)"`
	Stage               string         `gorm:"type:varchar(16);not null;index"` // prompt | reference | artifact | upload
	ContentType         string         `gorm:"type:varchar(16);not null"`       // text | image | video | audio
	ContentHash         string         `gorm:"type:varchar(64);not null;index"`
	Provider            string         `gorm:"type:varchar(32);not null"`
	ProviderRequestID   string         `gorm:"type:varchar(128);index"`
	PolicyVersion       string         `gorm:"type:varchar(32);not null;index"`
	ProviderResult      datatypes.JSON `gorm:"type:json"`
	Decision            string         `gorm:"type:varchar(16);not null;index:idx_moderation_decision_created,priority:1"` // pending | passed | rejected | error
	RiskLabels          datatypes.JSON `gorm:"type:json"`
	QuarantineKey       string         `gorm:"type:varchar(120)"`
	QuarantineExpiresAt *time.Time     `gorm:"index"`
	ReviewStatus        string         `gorm:"type:varchar(16);not null;default:not_required;index:idx_moderation_review_created,priority:1"` // not_required | pending | approved | rejected
	ReviewRevision      int            `gorm:"not null;default:0"`
	ReviewedBy          *uuid.UUID     `gorm:"type:char(36)"`
	ReviewNote          string         `gorm:"type:varchar(200)"`
	ReviewedAt          *time.Time
	CompensatedMicros   int64     `gorm:"not null;default:0"`
	CreatedAt           time.Time `gorm:"index:idx_moderation_user_created,priority:2,sort:desc;index:idx_moderation_decision_created,priority:2,sort:desc;index:idx_moderation_review_created,priority:2,sort:desc"`
	UpdatedAt           time.Time
}

func (ModerationRecord) TableName() string { return "moderation_records" }
