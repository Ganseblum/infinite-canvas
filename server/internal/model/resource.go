package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Canvas 画布。revision 是单条记录粒度的乐观锁，每次写入递增。
type Canvas struct {
	ID              uuid.UUID      `gorm:"type:char(36);primaryKey"`
	UserID          uuid.UUID      `gorm:"type:char(36);not null;index:idx_canvas_user_updated,priority:1"`
	Title           string         `gorm:"type:varchar(200);not null"`
	Data            datatypes.JSON `gorm:"type:json;not null"`
	Revision        int64          `gorm:"not null;default:1"`
	NodeCount       int            `gorm:"not null;default:0"`
	ConnectionCount int            `gorm:"not null;default:0"`
	CoverKey        string         `gorm:"type:varchar(80);not null;default:''"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       gorm.DeletedAt `gorm:"index:idx_canvas_user_updated,priority:2"`
}

// Asset 素材。标签存 asset_tags 关联表，不存 JSON 数组。
type Asset struct {
	ID               uuid.UUID      `gorm:"type:char(36);primaryKey"`
	UserID           uuid.UUID      `gorm:"type:char(36);not null;index:idx_asset_user_updated,priority:1"`
	Kind             string         `gorm:"type:varchar(16);not null"` // text | image | video
	Title            string         `gorm:"type:varchar(200);not null"`
	Data             datatypes.JSON `gorm:"type:json;not null"`
	StorageKey       string         `gorm:"type:varchar(80);not null;default:''"`
	Bytes            int64          `gorm:"not null;default:0"`
	ModerationStatus string         `gorm:"type:varchar(16);not null;default:skipped"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        gorm.DeletedAt `gorm:"index:idx_asset_user_updated,priority:2"`
}

// AssetTag 素材标签关联表：(asset_id, tag) 唯一；(tag, asset_id) 服务标签筛选。
type AssetTag struct {
	AssetID uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_asset_tag,priority:1;index:idx_tag_asset,priority:2"`
	Tag     string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_asset_tag,priority:2;index:idx_tag_asset,priority:1"`
}

func (AssetTag) TableName() string { return "asset_tags" }

// Generation 生成记录。刻意没有 UpdatedAt：除 pending 终态收敛外写完不再变，
// 排序始终按 CreatedAt。本期没有写入方，由第四期转发链路写入与收敛。
type Generation struct {
	ID               uuid.UUID      `gorm:"type:char(36);primaryKey"`
	UserID           uuid.UUID      `gorm:"type:char(36);not null;index:idx_generation_user_kind_created,priority:1"`
	Kind             string         `gorm:"type:varchar(16);not null;index:idx_generation_user_kind_created,priority:2"` // image | video
	Status           string         `gorm:"type:varchar(16);not null"`                                                   // pending | success | failed
	Prompt           string         `gorm:"type:text"`
	Model            string         `gorm:"type:varchar(120)"`
	Config           datatypes.JSON `gorm:"type:json"`
	Result           datatypes.JSON `gorm:"type:json"`
	DurationMs       int            `gorm:"not null;default:0"`
	ModerationStatus string         `gorm:"type:varchar(16);not null;default:skipped"`
	CreatedAt        time.Time      `gorm:"index:idx_generation_user_kind_created,priority:3,sort:desc"`
	DeletedAt        gorm.DeletedAt
}

// MediaFile 媒体文件索引。走硬删除，没有 DeletedAt：删媒体时磁盘/对象上的
// 文件也要一并删掉，留一行已删除的记录没有意义。
type MediaFile struct {
	ID               uuid.UUID `gorm:"type:char(36);primaryKey"`
	UserID           uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_media_user_key,priority:1"`
	StorageKey       string    `gorm:"type:varchar(80);not null;uniqueIndex:idx_media_user_key,priority:2"`
	ObjectPath       string    `gorm:"type:varchar(300);not null"`
	MimeType         string    `gorm:"type:varchar(120);not null"`
	Bytes            int64     `gorm:"not null"`
	Checksum         string    `gorm:"type:varchar(64);not null"`
	ModerationStatus string    `gorm:"type:varchar(16);not null;default:skipped"`
	CreatedAt        time.Time
}
