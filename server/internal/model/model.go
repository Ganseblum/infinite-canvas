package model

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID `gorm:"type:char(36);primaryKey"`
	Email           string    `gorm:"uniqueIndex;not null"`
	Username        string    `gorm:"uniqueIndex;not null"`
	PasswordHash    string    `gorm:"not null"`
	DisplayName     string    `gorm:"not null;default:''"`
	AvatarURL       string    `gorm:"not null;default:''"`
	Role            string    `gorm:"not null;default:user"`
	Status          string    `gorm:"not null;default:active"`
	EmailVerifiedAt *time.Time
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID    uuid.UUID `gorm:"type:char(36);index;not null"`
	TokenHash string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	RevokedAt *time.Time
	UserAgent string
	IP        string
	CreatedAt time.Time
}

type EmailToken struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID    uuid.UUID `gorm:"type:char(36);index;not null"`
	TokenHash string    `gorm:"uniqueIndex;not null"`
	Purpose   string    `gorm:"not null"` // verify_email | reset_password
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time
}

type FreeGrantClaim struct {
	ID         uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID     uuid.UUID `gorm:"type:char(36);index;not null;uniqueIndex:idx_user_campaign"`
	CampaignID string    `gorm:"not null;uniqueIndex:idx_user_campaign"`
	Status     string    `gorm:"not null"` // granted | denied
	Reason     string    `gorm:"not null;default:''"`
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
