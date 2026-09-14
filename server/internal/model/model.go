package model

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID `gorm:"type:char(36);primaryKey"`
	Email           string    `gorm:"type:varchar(255);uniqueIndex;not null"`
	Username        string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	PasswordHash    string    `gorm:"type:varchar(100);not null"`
	DisplayName     string    `gorm:"type:varchar(64);not null;default:''"`
	AvatarURL       string    `gorm:"type:varchar(512);not null;default:''"`
	Role            string    `gorm:"type:varchar(16);not null;default:user"`
	Status          string    `gorm:"type:varchar(24);not null;default:active"`
	EmailVerifiedAt *time.Time
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID    uuid.UUID `gorm:"type:char(36);index;not null"`
	TokenHash string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	RevokedAt *time.Time
	UserAgent string    `gorm:"type:varchar(512)"`
	IP        string    `gorm:"type:varchar(64)"`
	CreatedAt time.Time
}

type EmailToken struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID    uuid.UUID `gorm:"type:char(36);index;not null"`
	TokenHash string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	Purpose   string    `gorm:"type:varchar(24);not null"` // verify_email | reset_password
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time
}

type FreeGrantClaim struct {
	ID         uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID     uuid.UUID `gorm:"type:char(36);index;not null;uniqueIndex:idx_user_campaign"`
	CampaignID string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_user_campaign"`
	Status     string    `gorm:"type:varchar(16);not null"` // granted | denied
	Reason     string    `gorm:"type:varchar(255);not null;default:''"`
	// 唯一约束：(user_id, campaign_id) 只能有一条领取结论
	CreatedAt time.Time
}

func (FreeGrantClaim) TableName() string { return "free_grant_claims" }

type Plan struct {
	ID            string `gorm:"primaryKey"` // free | paid | sunset
	Name          string `gorm:"not null"`
	StorageBytes  int64  `gorm:"not null"`
	MaxFileBytes  int64  `gorm:"not null"`
	RetentionDays int    `gorm:"not null"`
}
