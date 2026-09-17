package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Canvas 画布。revision 是单条记录粒度的乐观锁，每次写入递增。
type Canvas struct {
	ID              uuid.UUID      `gorm:"type:char(36);primaryKey;comment:画布主键"`
	UserID          uuid.UUID      `gorm:"type:char(36);not null;index:idx_canvas_user_updated,priority:1;comment:画布所属用户"`
	Title           string         `gorm:"type:varchar(200);not null;comment:画布标题"`
	Data            datatypes.JSON `gorm:"type:json;not null;comment:画布内容 JSON，含节点与连线"`
	Revision        int64          `gorm:"not null;default:1;comment:乐观锁版本号，每次写入递增，提交时比对以防并发覆盖"`
	NodeCount       int            `gorm:"not null;default:0;comment:节点数量，列表页展示用"`
	ConnectionCount int            `gorm:"not null;default:0;comment:连线数量，列表页展示用"`
	CoverKey        string         `gorm:"type:varchar(80);not null;default:'';comment:封面图的存储键，取自画布内引用的图片节点，空串表示无封面"`
	CreatedAt       time.Time      `gorm:"comment:创建时间"`
	UpdatedAt       time.Time      `gorm:"comment:最近更新时间"`
	DeletedAt       gorm.DeletedAt `gorm:"index:idx_canvas_user_updated,priority:2;comment:软删除时间，非空表示已进入回收站"`
}

// Asset 素材。标签存 asset_tags 关联表，不存 JSON 数组。
type Asset struct {
	ID               uuid.UUID      `gorm:"type:char(36);primaryKey;comment:素材主键"`
	UserID           uuid.UUID      `gorm:"type:char(36);not null;index:idx_asset_user_updated,priority:1;comment:素材所属用户"`
	Kind             string         `gorm:"type:varchar(16);not null;comment:素材类型，text 文本、image 图片、video 视频"` // text | image | video
	Title            string         `gorm:"type:varchar(200);not null;comment:素材标题"`
	Data             datatypes.JSON `gorm:"type:json;not null;comment:素材内容 JSON，文本存正文、图片存尺寸等元信息"`
	StorageKey       string         `gorm:"type:varchar(80);not null;default:'';comment:原始文件的存储键，指向 media_files.storage_key，空串表示纯文本素材"`
	Bytes            int64          `gorm:"not null;default:0;comment:原始文件占用字节数，纯文本素材为 0"`
	ModerationStatus string         `gorm:"type:varchar(16);not null;default:skipped;comment:审核状态，skipped 未送审、pending 审核中、passed 已通过、rejected 已拒绝"`
	CreatedAt        time.Time      `gorm:"comment:创建时间"`
	UpdatedAt        time.Time      `gorm:"comment:最近更新时间"`
	DeletedAt        gorm.DeletedAt `gorm:"index:idx_asset_user_updated,priority:2;comment:软删除时间，非空表示已删除"`
}

// AssetTag 素材标签关联表：(asset_id, tag) 唯一；(tag, asset_id) 服务标签筛选。
type AssetTag struct {
	AssetID uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_asset_tag,priority:1;index:idx_tag_asset,priority:2;comment:素材 id，与标签组成唯一约束"`
	Tag     string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_asset_tag,priority:2;index:idx_tag_asset,priority:1;comment:标签文本，按素材与标签双向索引用于筛选"`
}

func (AssetTag) TableName() string { return "asset_tags" }

// Generation 生成记录。刻意没有 UpdatedAt：除 pending 终态收敛外写完不再变，
// 排序始终按 CreatedAt。本期没有写入方，由第四期转发链路写入与收敛。
type Generation struct {
	ID               uuid.UUID      `gorm:"type:char(36);primaryKey;comment:生成记录主键"`
	UserID           uuid.UUID      `gorm:"type:char(36);not null;index:idx_generation_user_kind_created,priority:1;comment:发起生成的用户"`
	Kind             string         `gorm:"type:varchar(16);not null;index:idx_generation_user_kind_created,priority:2;comment:生成类型，image 图片、video 视频"` // image | video
	Status           string         `gorm:"type:varchar(16);not null;comment:生成状态，pending 进行中、success 成功、failed 失败"`                                    // pending | success | failed
	Prompt           string         `gorm:"type:text;comment:提示词原文"`
	Model            string         `gorm:"type:varchar(120);comment:使用的模型标识，取自 model_catalogs.name"`
	Config           datatypes.JSON `gorm:"type:json;comment:本次生成的报价快照 JSON，含参数与价格明细"`
	Result           datatypes.JSON `gorm:"type:json;comment:生成结果 JSON，含产物存储键等"`
	DurationMs       int            `gorm:"not null;default:0;comment:上游调用耗时，单位毫秒"`
	ModerationStatus string         `gorm:"type:varchar(16);not null;default:skipped;comment:审核状态，skipped 未送审、passed 已通过、rejected 已拒绝"`
	CreatedAt        time.Time      `gorm:"index:idx_generation_user_kind_created,priority:3,sort:desc;comment:创建时间，列表按此倒序"`
	DeletedAt        gorm.DeletedAt `gorm:"comment:软删除时间"`
}

// MediaFile 媒体文件索引。走硬删除，没有 DeletedAt：删媒体时磁盘/对象上的
// 文件也要一并删掉，留一行已删除的记录没有意义。
type MediaFile struct {
	ID               uuid.UUID `gorm:"type:char(36);primaryKey;comment:媒体文件主键"`
	UserID           uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:idx_media_user_key,priority:1;comment:归属用户，与存储键组成唯一约束"`
	StorageKey       string    `gorm:"type:varchar(80);not null;uniqueIndex:idx_media_user_key,priority:2;comment:存储键，格式为 类型前缀：对象ID，画布与素材都通过它引用本行"`
	ObjectPath       string    `gorm:"type:varchar(300);not null;comment:对象在存储后端的实际路径或对象键"`
	MimeType         string    `gorm:"type:varchar(120);not null;comment:文件 MIME 类型"`
	Bytes            int64     `gorm:"not null;comment:文件字节数，用于用量统计与配额"`
	Checksum         string    `gorm:"type:varchar(64);not null;comment:文件内容校验和，用于去重与完整性校验"`
	ModerationStatus string    `gorm:"type:varchar(16);not null;default:skipped;comment:审核状态，skipped 未送审、pending 审核中、passed 已通过、rejected 已拒绝"`
	CreatedAt        time.Time `gorm:"comment:创建时间"`
}
