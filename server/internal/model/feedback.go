package model

import (
	"time"

	"github.com/google/uuid"
)

// FeedbackTicket 用户反馈工单：用户从主站「反馈」入口提交，后台工单台处理。
// 状态 open 待处理、resolved 已解决、closed 已关闭；用户在已解决的工单上再回复会重新打开。
type FeedbackTicket struct {
	ID         uuid.UUID  `gorm:"type:char(36);primaryKey;comment:工单主键"`
	UserID     uuid.UUID  `gorm:"type:char(36);not null;index:idx_feedback_ticket_user_created,priority:1;comment:提交人平台账号"`
	Category   string     `gorm:"type:varchar(16);not null;comment:工单类别，quality 生成效果、suggestion 功能建议、payment 充值支付、account 账号问题、other 其他"`                                   // quality | suggestion | payment | account | other
	Status     string     `gorm:"type:varchar(16);not null;default:open;index:idx_feedback_ticket_status_created,priority:1;comment:工单状态，open 待处理、resolved 已解决、closed 已关闭"` // open | resolved | closed
	Content    string     `gorm:"type:varchar(1000);not null;comment:工单正文，用户提交的反馈描述"`
	ReplyCount int        `gorm:"not null;default:0;comment:回复条数，含用户与客服双方，详情页按回复时间正序展示"`
	ResolvedAt *time.Time `gorm:"comment:最近一次置为已解决的时间"`
	CreatedAt  time.Time  `gorm:"index:idx_feedback_ticket_status_created,priority:2;comment:提交时间"`
	UpdatedAt  time.Time  `gorm:"comment:最近更新时间"`
}

func (FeedbackTicket) TableName() string { return "feedback_tickets" }

// FeedbackTicketReply 工单回复：双方对话线程。is_staff 区分客服侧与用户侧，
// 客服回复来自后台工单台（admin 或被任命 support 角色的账号）。
type FeedbackTicketReply struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey;comment:回复主键"`
	TicketID  uuid.UUID `gorm:"type:char(36);not null;index:idx_feedback_reply_ticket_created,priority:1;comment:所属工单"`
	UserID    uuid.UUID `gorm:"type:char(36);not null;comment:回复人平台账号"`
	IsStaff   bool      `gorm:"not null;default:false;comment:是否客服侧回复，后台工单台发出的回复为 true"`
	Content   string    `gorm:"type:varchar(1000);not null;comment:回复正文"`
	CreatedAt time.Time `gorm:"comment:回复时间"`
}

func (FeedbackTicketReply) TableName() string { return "feedback_ticket_replies" }

// GenerationFeedback 用户对自身生成结果的点赞点踩反馈：点踩时携带预设标签与可选文本说明，
// 供后台了解生成质量。一行一 (user_id, generation_id)，重复提交按 upsert 覆盖。
type GenerationFeedback struct {
	ID           uuid.UUID `gorm:"type:char(36);primaryKey;comment:反馈主键"`
	UserID       uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_generation_feedback_unique,priority:1;comment:反馈人平台账号"`
	GenerationID uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_generation_feedback_unique,priority:2;index;comment:被反馈的生成记录"`
	Rating       int       `gorm:"not null;comment:评分，1 点赞、-1 点踩"`
	Labels       string    `gorm:"type:varchar(200);not null;default:'';comment:点踩预设标签，逗号分隔，如 生成效果差,不符合提示词"`
	Note         string    `gorm:"type:varchar(500);not null;default:'';comment:用户补充的文本说明"`
	CreatedAt    time.Time `gorm:"comment:首次反馈时间"`
	UpdatedAt    time.Time `gorm:"index;comment:最近反馈时间"`
}

func (GenerationFeedback) TableName() string { return "generation_feedbacks" }

// 反馈工单的类别白名单。
func FeedbackCategoryValid(category string) bool {
	switch category {
	case "quality", "suggestion", "payment", "account", "other":
		return true
	}
	return false
}

// 反馈工单的状态白名单。
func FeedbackStatusValid(status string) bool {
	switch status {
	case "open", "resolved", "closed":
		return true
	}
	return false
}
