package model

import (
	"time"

	"github.com/google/uuid"
)

// SiteSetting 是站点级配置的键值存储。管理后台可改的开关与边界值都放这里，
// 不走环境变量，避免每次调整都要重启容器。
type SiteSetting struct {
	Key string `gorm:"type:varchar(64);primaryKey;comment:设置键，取值见 service 的 Setting 常量，如 maintenance_mode"`
	// Value 存 JSON 文本。刻意不用 datatypes.JSON：裸数字（如 50000）不是合法 JSON 文档，
	// 会被 JSONB 扫描器拒绝，导致设置一写入数字后重启就加载失败。
	Value     string     `gorm:"type:text;not null;comment:设置值，以 JSON 文本存储，可为字符串、数字或布尔"`
	UpdatedBy *uuid.UUID `gorm:"type:char(36);comment:最后修改人的用户 id，系统写入时为空"`
	UpdatedAt time.Time  `gorm:"comment:最近修改时间"`
}

func (SiteSetting) TableName() string { return "site_settings" }

// CommunityWork 是发布到社区的作品，复用用户素材（assets）作为内容本体，
// 产物与封面都走 media_files 与第五期的审核链路。
type CommunityWork struct {
	ID          uuid.UUID `gorm:"type:char(36);primaryKey;comment:作品主键"`
	UserID      uuid.UUID `gorm:"type:char(36);not null;index:idx_work_user_created,priority:1;comment:发布者用户"`
	AssetID     uuid.UUID `gorm:"type:char(36);not null;index;comment:内容本体对应的素材，指向 assets"`
	Title       string    `gorm:"type:varchar(200);not null;comment:作品标题"`
	Description string    `gorm:"type:varchar(1000);not null;default:'';comment:作品描述"`
	Tags        string    `gorm:"type:varchar(500);not null;default:'';comment:标签串，逗号分隔，读取时拆成数组"`
	CoverKey    string    `gorm:"type:varchar(80);not null;default:'';comment:封面的存储键，取自素材存储键，空串表示无封面"`
	Status      string    `gorm:"type:varchar(16);not null;default:published;index;comment:作品状态，published 已发布、hidden 已隐藏、removed 已下架"` // published | hidden | removed
	// SourceWorkID 是复刻来源，用于来源作品计数与展示。
	SourceWorkID *uuid.UUID `gorm:"type:char(36);comment:复刻来源作品，非复刻为空"`
	LikeCount    int        `gorm:"not null;default:0;comment:点赞数，冗余计数供列表展示"`
	RemixCount   int        `gorm:"not null;default:0;comment:被复刻次数，冗余计数"`
	ReportCount  int        `gorm:"not null;default:0;comment:被举报次数，冗余计数"`
	ModerationOK bool       `gorm:"not null;default:true;comment:审核是否放行，为假时不在社区展示"`
	CreatedAt    time.Time  `gorm:"index:idx_work_user_created,priority:2,sort:desc;comment:发布时间"`
	UpdatedAt    time.Time  `gorm:"comment:最近更新时间"`
}

// CommunityLike (work_id, user_id) 唯一，重复点赞幂等。
type CommunityLike struct {
	WorkID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_work_like,priority:1;comment:被点赞作品，与用户组成联合主键"`
	UserID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_work_like,priority:2;comment:点赞用户，联合主键保证同一用户对同一作品只赞一次"`
	CreatedAt time.Time `gorm:"comment:点赞时间"`
}

func (CommunityLike) TableName() string { return "community_likes" }

// CommunityReport 记录举报；同一用户对同一作品只能举报一次。
type CommunityReport struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey;comment:举报主键"`
	WorkID    uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_work_report,priority:1;index;comment:被举报作品，与举报人组成唯一约束"`
	UserID    uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_work_report,priority:2;comment:举报人用户"`
	Reason    string    `gorm:"type:varchar(200);not null;comment:举报理由"`
	Status    string    `gorm:"type:varchar(16);not null;default:pending;index;comment:处理状态，pending 待处理、handled 已处理、dismissed 已驳回"` // pending | handled | dismissed
	CreatedAt time.Time `gorm:"comment:举报时间"`
}

func (CommunityReport) TableName() string { return "community_reports" }

// CheckinRecord 每日签到。(user_id, checkin_date) 唯一保证一天只发一次。
type CheckinRecord struct {
	ID           uuid.UUID `gorm:"type:char(36);primaryKey;comment:签到记录主键"`
	UserID       uuid.UUID `gorm:"type:char(36);not null;index;comment:签到用户"`
	CheckinDate  string    `gorm:"type:varchar(10);not null;uniqueIndex:idx_checkin_user_date,priority:1;comment:签到日期，格式 YYYY-MM-DD，与用户组成唯一约束保证一天一次"`
	RewardMicros int64     `gorm:"not null;default:0;comment:本次签到发放的点数，单位微元"`
	StreakDays   int       `gorm:"not null;default:1;comment:截至本次的连续签到天数"`
	CreatedAt    time.Time `gorm:"comment:签到时间"`
}

func (CheckinRecord) TableName() string { return "checkin_records" }

// UserInvite 邀请返利：邀请码归属于邀请人，同一被邀请人只能被绑一次。
type UserInvite struct {
	Code                string    `gorm:"type:varchar(16);primaryKey;comment:邀请码，作为主键，与 users.invite_code 对应"`
	InviterID           uuid.UUID `gorm:"type:char(36);not null;index;comment:邀请人用户"`
	InviteeID           uuid.UUID `gorm:"type:char(36);not null;uniqueIndex;comment:被邀请人用户，唯一约束保证一人只能被绑定一次"`
	InviterRewardMicros int64     `gorm:"not null;default:0;comment:邀请人获得的返利点数，单位微元，绑定当时按配置固化"`
	InviteeRewardMicros int64     `gorm:"not null;default:0;comment:被邀请人获得的奖励点数，单位微元，绑定当时按配置固化"`
	CreatedAt           time.Time `gorm:"comment:绑定时间"`
}

func (UserInvite) TableName() string { return "user_invites" }
