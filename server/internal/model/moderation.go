package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ModerationRecord 是一次审核的事实记录。
// 只保存内容哈希、标签与服务商摘要，不保存原始提示词与媒体；原件只放短期隔离区。
type ModerationRecord struct {
	ID                  uuid.UUID      `gorm:"type:char(36);primaryKey;comment:审核记录主键"`
	UserID              uuid.UUID      `gorm:"type:char(36);not null;index:idx_moderation_user_created,priority:1;comment:送审用户"`
	GenerationID        *uuid.UUID     `gorm:"type:char(36);comment:关联的生成记录，非生成链路为空"`
	MediaFileID         *uuid.UUID     `gorm:"type:char(36);comment:关联的媒体文件，非文件类审核为空"`
	Stage               string         `gorm:"type:varchar(16);not null;index;comment:审核环节，prompt 提示词、reference 参考图、artifact 生成产物、upload 上传文件"` // prompt | reference | artifact | upload
	ContentType         string         `gorm:"type:varchar(16);not null;comment:送审内容类型，text 文本、image 图片、video 视频、audio 音频"`                     // text | image | video | audio
	ContentHash         string         `gorm:"type:varchar(64);not null;index;comment:送审内容的哈希，不存原文，用于去重与追溯"`
	Provider            string         `gorm:"type:varchar(32);not null;comment:审核服务商标识"`
	ProviderRequestID   string         `gorm:"type:varchar(128);index;comment:审核服务商侧请求 id，用于对账与申诉"`
	PolicyVersion       string         `gorm:"type:varchar(32);not null;index;comment:命中的策略版本，策略调整后旧记录据此解释"`
	ProviderResult      datatypes.JSON `gorm:"type:json;comment:审核服务商返回的原始结果 JSON"`
	Decision            string         `gorm:"type:varchar(16);not null;index:idx_moderation_decision_created,priority:1;comment:机器审核结论，pending 审核中、passed 通过、rejected 拒绝、error 调用异常"` // pending | passed | rejected | error
	RiskLabels          datatypes.JSON `gorm:"type:json;comment:命中的风险标签数组 JSON"`
	QuarantineKey       string         `gorm:"type:varchar(120);comment:隔离区对象的存储键，短期保留原件供人工复核，空串表示已清空"`
	QuarantineBytes     int64          `gorm:"not null;default:0;comment:隔离原件的加密字节数，用于隔离区容量统计与告警"`
	QuarantineExpiresAt *time.Time     `gorm:"index;comment:隔离原件的过期时间，过期后无法取回"`
	ReviewStatus        string         `gorm:"type:varchar(16);not null;default:not_required;index:idx_moderation_review_created,priority:1;comment:人工复核状态，not_required 无需复核、pending 待复核、approved 通过、rejected 驳回"` // not_required | pending | approved | rejected
	ReviewRevision      int            `gorm:"not null;default:0;comment:复核版本号，每次人工改判递增，提交时比对以防并发覆盖"`
	ReviewedBy          *uuid.UUID     `gorm:"type:char(36);comment:复核人用户，未复核为空"`
	ReviewNote          string         `gorm:"type:varchar(200);comment:复核备注"`
	ReviewedAt          *time.Time     `gorm:"comment:复核时间，未复核为空"`
	CompensatedMicros   int64          `gorm:"not null;default:0;comment:误判后补偿给用户的点数，单位微元，未补偿为 0"`
	CreatedAt           time.Time      `gorm:"index:idx_moderation_user_created,priority:2,sort:desc;index:idx_moderation_decision_created,priority:2,sort:desc;index:idx_moderation_review_created,priority:2,sort:desc;comment:送审时间"`
	UpdatedAt           time.Time      `gorm:"comment:最近更新时间"`
}

func (ModerationRecord) TableName() string { return "moderation_records" }
