package model

import (
	"time"

	"github.com/google/uuid"
)

// ===== membership 域 =====

// MembershipPlan 会员订阅档位（membership_plans）：D5 拆分后承接订阅属性，
// 点数包属性在 credit_packages。seed 保留 free/paid/sunset 三个 id。
type MembershipPlan struct {
	ID            string `gorm:"primaryKey;type:varchar(32);comment:会员档位标识，free 免费、paid 付费、sunset 日落宽限"` // free | paid | sunset
	Name          string `gorm:"type:varchar(80);not null;comment:档位显示名"`
	StorageBytes  int64  `gorm:"not null;comment:该档位的平台共享池存储配额上限（D1），单位字节"`
	MaxFileBytes  int64  `gorm:"not null;comment:该档位单个上传文件的体积上限，单位字节"`
	RetentionDays int    `gorm:"not null;comment:媒体保留天数，超期由清理任务回收"`
	PriceMicros   int64  `gorm:"not null;default:0;comment:订阅价，单位微元（1 元 = 1000000 微元）；0 表示不可购买"`
	DurationDays  int    `gorm:"not null;default:0;comment:每次购买的订阅周期天数；0 表示不可购买"`
	Currency      string `gorm:"type:varchar(8);not null;default:CNY;comment:计价货币，如 CNY"`
	Enabled       bool   `gorm:"not null;default:true;comment:是否上架，下架后前台不可见且不可购买"`
	Sort          int    `gorm:"not null;default:0;comment:展示排序，数值小的排前面"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MembershipSubscription 会员订阅（membership_subscriptions）：D2 平台会员，
// 一次订阅全产品生效。付费身份由 period_end 表达，不再写在点数账户上；
// 到期后 60 天内为日落宽限（graceEndsAt = period_end + 60 天）。
type MembershipSubscription struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey;comment:订阅主键"`
	UserID    uuid.UUID `gorm:"type:char(36);index;not null;comment:订阅人平台账号"`
	PlanID    string    `gorm:"type:varchar(32);not null;comment:订阅的会员档位，指向 membership_plans.id"`
	Status    string    `gorm:"type:varchar(16);not null;default:active;index;comment:active 生效中、ended 已结束"` // active | ended
	StartedAt time.Time `gorm:"not null;comment:订阅开始时间"`
	PeriodEnd time.Time `gorm:"not null;comment:当前周期结束时间，付费身份与日落宽限的判定依据"`
	SourceRef string    `gorm:"type:varchar(64);comment:来源单据，order id 或 admin 补偿原因"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ===== billing 域 =====

// StorageAccount 平台共享池账户（storage_accounts）：一用户一行，配额由当前会员档位
// 决定（membership.SyncQuota 回写），UsedBytes 是全部产品用量聚合的快速计数。
type StorageAccount struct {
	UserID     uuid.UUID `gorm:"type:char(36);primaryKey;comment:平台账号，一用户一行"`
	QuotaBytes int64     `gorm:"not null;default:0;comment:存储配额上限，单位字节，由会员档位决定"`
	UsedBytes  int64     `gorm:"not null;default:0;comment:全部产品用量聚合，单位字节，与 storage_usage 分产品计数对账"`
	UpdatedAt  time.Time
}

// StorageUsage 分产品存储用量（storage_usage）：共享池的三层记账第二层，
// (user_id, product) 唯一；夜间对账任务比对 media_files 聚合、本表与 storage_accounts。
type StorageUsage struct {
	UserID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_storage_usage_user_product,priority:1;comment:平台账号"`
	Product   string    `gorm:"type:varchar(32);primaryKey;uniqueIndex:idx_storage_usage_user_product,priority:2;comment:产品标识，画布侧固定 youc-canvas"`
	Bytes     int64     `gorm:"not null;default:0;comment:该产品占用字节数"`
	UpdatedAt time.Time
}

// TableName 固定表名为 storage_usage（契约表名；storage 域 Recalculate 的裸 SQL 也按它引用）。
func (StorageUsage) TableName() string { return "storage_usage" }

// ===== 通用 =====

// ProductCanvas 是画布产品在 product 维度上的固定取值，与 /admin/meta 的 product.id 同值。
const ProductCanvas = "youc-canvas"
