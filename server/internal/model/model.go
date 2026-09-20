// Package model 是共享数据模型层：全部 GORM 表结构集中在此，gorm tag 的 comment
// 即建表列注释；跨域共享的常量（统计时区、存储键格式契约）也定义在这里。
package model

import (
	"time"

	"github.com/google/uuid"
)

// PlatformUser 是平台账号主表（platform_users）：全产品共享的唯一身份，
// 各业务表通过 user_id 列引用它，语义即平台用户 id。
type PlatformUser struct {
	ID           uuid.UUID `gorm:"type:char(36);primaryKey;comment:平台账号主键，UUID v4 文本"`
	Email        string    `gorm:"type:varchar(255);uniqueIndex;not null;comment:登录邮箱，全站唯一"`
	Username     string    `gorm:"type:varchar(64);uniqueIndex;not null;comment:用户名，全站唯一，用于展示与他人搜索"`
	PasswordHash string    `gorm:"type:varchar(100);not null;comment:登录密码的 bcrypt 哈希，不存明文"`
	DisplayName  string    `gorm:"type:varchar(64);not null;default:'';comment:展示昵称，可与用户名不同"`
	AvatarURL    string    `gorm:"type:varchar(512);not null;default:'';comment:头像图片地址，空串表示未设置"`
	// Role 是旧的角色投影列，仅作回滚安全带保留，不再作为授权依据；
	// 由 authz.AssignRole 统一同步为 role_key 的保守投影（admin / user）。
	Role    string  `gorm:"type:varchar(16);not null;default:user;comment:旧角色投影列，不再作为授权依据，仅作回滚安全带"`
	RoleKey *string `gorm:"type:varchar(64);index;comment:后台角色标识，指向 roles.key，空值表示没有后台角色；授权只看这一列"`
	Status  string  `gorm:"type:varchar(24);not null;default:active;comment:账号状态，active 正常、disabled 已封禁、pending_deletion 待注销"` // active | disabled | pending_deletion
	// MustChangePassword 由管理员重置密码后置位，要求用户下次登录修改密码。
	MustChangePassword bool `gorm:"not null;default:false;comment:是否必须修改密码"`
	// MediaTokenVersion 是 ic_media 媒体令牌的版本号：改密、重置、封禁等安全事件
	// 递增一次，旧版本媒体令牌随即失效（差异清单 #9，此前改密后旧媒体令牌仍可用 30 天）。
	MediaTokenVersion int `gorm:"not null;default:0;comment:媒体令牌版本号，安全事件递增使旧 ic_media 失效"`
	// InviteCode 可空：唯一索引允许多行 NULL；注册时生成，用于邀请返利。
	InviteCode          *string    `gorm:"type:varchar(16);uniqueIndex;comment:本人的邀请码，注册时生成，空值表示未生成"`
	DeletionScheduledAt *time.Time `gorm:"comment:注销生效时间，到期后由后台任务真正删除账号"`
	EmailVerifiedAt     *time.Time `gorm:"comment:邮箱验证通过时间，未验证为空"`
	LastLoginAt         *time.Time `gorm:"comment:最近一次登录成功时间"`
	CreatedAt           time.Time  `gorm:"comment:记录创建时间"`
	UpdatedAt           time.Time  `gorm:"comment:记录最近更新时间"`
}

// Session 是平台会话表（sessions）：一次登录签发的刷新令牌记录，
// 取代旧 refresh_tokens 表；字段语义不变。
type Session struct {
	ID        uuid.UUID  `gorm:"type:char(36);primaryKey;comment:会话主键"`
	UserID    uuid.UUID  `gorm:"type:char(36);index;not null;comment:会话所属平台账号"`
	TokenHash string     `gorm:"type:varchar(64);uniqueIndex;not null;comment:刷新令牌原文的 SHA-256 哈希，不存原始令牌"`
	ExpiresAt time.Time  `gorm:"not null;comment:会话过期时间，过期后拒绝刷新"`
	RevokedAt *time.Time `gorm:"comment:会话被吊销的时间，未吊销为空"`
	UserAgent string     `gorm:"type:varchar(512);comment:签发时浏览器的 User-Agent，用于会话列表展示"`
	IP        string     `gorm:"type:varchar(64);comment:签发时的客户端 IP，用于会话列表展示"`
	CreatedAt time.Time  `gorm:"comment:会话签发时间"`
}

// EmailToken 是邮件令牌表（email_tokens）：验证邮箱与重置密码共用，
// 按 purpose 区分；只存令牌哈希，用过即失效。
type EmailToken struct {
	ID        uuid.UUID  `gorm:"type:char(36);primaryKey;comment:邮件令牌主键"`
	UserID    uuid.UUID  `gorm:"type:char(36);index;not null;comment:令牌所属用户"`
	TokenHash string     `gorm:"type:varchar(64);uniqueIndex;not null;comment:邮件令牌原文的 SHA-256 哈希，不存原始令牌"`
	Purpose   string     `gorm:"type:varchar(24);not null;comment:令牌用途，verify_email 验证邮箱、reset_password 重置密码"` // verify_email | reset_password
	ExpiresAt time.Time  `gorm:"not null;comment:令牌过期时间"`
	UsedAt    *time.Time `gorm:"comment:令牌被使用的时间，未使用为空，用过即失效"`
	CreatedAt time.Time  `gorm:"comment:令牌签发时间"`
}

// FreeGrantClaim 是免费赠送领取记录表（free_grant_claims）：
// (user_id, campaign_id) 唯一，同一活动一人只能有一条领取结论。
type FreeGrantClaim struct {
	ID         uuid.UUID `gorm:"type:char(36);primaryKey;comment:领取记录主键"`
	UserID     uuid.UUID `gorm:"type:char(36);index;not null;uniqueIndex:idx_user_campaign;comment:领取人用户，与活动 id 组成唯一约束"`
	CampaignID string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_user_campaign;comment:活动标识，取自配置 FREE_GRANT_CAMPAIGN_ID，同一活动只允许领取一次"`
	Status     string    `gorm:"type:varchar(16);not null;comment:领取结论，granted 已发放、denied 被拒绝"` // granted | denied
	Reason     string    `gorm:"type:varchar(255);not null;default:'';comment:发放或拒绝的原因说明"`
	// 唯一约束：(user_id, campaign_id) 只能有一条领取结论
	CreatedAt time.Time `gorm:"comment:领取时间"`
}

func (FreeGrantClaim) TableName() string { return "free_grant_claims" }
