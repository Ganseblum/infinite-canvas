package model

import (
	"time"

	"github.com/google/uuid"
)

// SiteSetting 是站点级配置的键值存储。管理后台可改的开关与边界值都放这里，
// 不走环境变量，避免每次调整都要重启容器。
type SiteSetting struct {
	Key string `gorm:"type:varchar(64);primaryKey"`
	// Value 存 JSON 文本。刻意不用 datatypes.JSON：裸数字（如 50000）不是合法 JSON 文档，
	// 会被 JSONB 扫描器拒绝，导致设置一写入数字后重启就加载失败。
	Value     string     `gorm:"type:text;not null"`
	UpdatedBy *uuid.UUID `gorm:"type:char(36)"`
	UpdatedAt time.Time
}

func (SiteSetting) TableName() string { return "site_settings" }

// CommunityWork 是发布到社区的作品，复用用户素材（assets）作为内容本体，
// 产物与封面都走 media_files 与第五期的审核链路。
type CommunityWork struct {
	ID          uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID      uuid.UUID `gorm:"type:char(36);not null;index:idx_work_user_created,priority:1"`
	AssetID     uuid.UUID `gorm:"type:char(36);not null;index"`
	Title       string    `gorm:"type:varchar(200);not null"`
	Description string    `gorm:"type:varchar(1000);not null;default:''"`
	Tags        string    `gorm:"type:varchar(500);not null;default:''"`
	CoverKey    string    `gorm:"type:varchar(80);not null;default:''"`
	Status      string    `gorm:"type:varchar(16);not null;default:published;index"` // published | hidden | removed
	// SourceWorkID 是复刻来源，用于来源作品计数与展示。
	SourceWorkID *uuid.UUID `gorm:"type:char(36)"`
	LikeCount    int        `gorm:"not null;default:0"`
	RemixCount   int        `gorm:"not null;default:0"`
	ReportCount  int        `gorm:"not null;default:0"`
	ModerationOK bool       `gorm:"not null;default:true"`
	CreatedAt    time.Time  `gorm:"index:idx_work_user_created,priority:2,sort:desc"`
	UpdatedAt    time.Time
}

// CommunityLike (work_id, user_id) 唯一，重复点赞幂等。
type CommunityLike struct {
	WorkID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_work_like,priority:1"`
	UserID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_work_like,priority:2"`
	CreatedAt time.Time
}

func (CommunityLike) TableName() string { return "community_likes" }

// CommunityReport 记录举报；同一用户对同一作品只能举报一次。
type CommunityReport struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	WorkID    uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_work_report,priority:1;index"`
	UserID    uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_work_report,priority:2"`
	Reason    string    `gorm:"type:varchar(200);not null"`
	Status    string    `gorm:"type:varchar(16);not null;default:pending;index"` // pending | handled | dismissed
	CreatedAt time.Time
}

func (CommunityReport) TableName() string { return "community_reports" }

// CheckinRecord 每日签到。(user_id, checkin_date) 唯一保证一天只发一次。
type CheckinRecord struct {
	ID           uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID       uuid.UUID `gorm:"type:char(36);not null;index"`
	CheckinDate  string    `gorm:"type:varchar(10);not null;uniqueIndex:idx_checkin_user_date,priority:1"`
	RewardMicros int64     `gorm:"not null;default:0"`
	StreakDays   int       `gorm:"not null;default:1"`
	CreatedAt    time.Time
}

func (CheckinRecord) TableName() string { return "checkin_records" }

// UserInvite 邀请返利：邀请码归属于邀请人，同一被邀请人只能被绑一次。
type UserInvite struct {
	Code                string    `gorm:"type:varchar(16);primaryKey"`
	InviterID           uuid.UUID `gorm:"type:char(36);not null;index"`
	InviteeID           uuid.UUID `gorm:"type:char(36);not null;uniqueIndex"`
	InviterRewardMicros int64     `gorm:"not null;default:0"`
	InviteeRewardMicros int64     `gorm:"not null;default:0"`
	CreatedAt           time.Time
}

func (UserInvite) TableName() string { return "user_invites" }
