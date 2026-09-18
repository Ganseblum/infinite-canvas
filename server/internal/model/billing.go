package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// CreditAccount 平台点数账户（credit_accounts）替换 credits：一用户一行、全产品共享余额（D3）。
// 不变量：purchased_micros 等于该用户 bucket=purchased 的流水之和，granted 同理。
// paid_until 已移除：付费身份由 membership_subscriptions.period_end 表达（D2/D6）。
type CreditAccount struct {
	UserID          uuid.UUID `gorm:"type:char(36);primaryKey;comment:平台账号，与 platform_users 一一对应，一用户一行"`
	PurchasedMicros int64     `gorm:"not null;default:0;comment:充值桶余额，单位微元（1 元 = 1000000 微元），只能由充值入账"`
	GrantedMicros   int64     `gorm:"not null;default:0;comment:赠送桶余额，单位微元（1 元 = 1000000 微元），来自活动发放与系统赠送"`
	UpdatedAt       time.Time `gorm:"comment:余额最近变动时间"`
}

type CreditTransaction struct {
	ID                    uuid.UUID  `gorm:"type:char(36);primaryKey;comment:流水主键"`
	UserID                uuid.UUID  `gorm:"type:char(36);index;not null;comment:流水所属用户"`
	Product               string     `gorm:"type:varchar(32);not null;default:youc-canvas;index;comment:产生流水的产品标识，全产品共享余额下按产品分账"`
	Bucket                string     `gorm:"type:varchar(16);not null;comment:点数的桶，purchased 充值桶、granted 赠送桶，退款按原桶退回"`                     // purchased | granted
	Type                  string     `gorm:"type:varchar(16);not null;comment:流水类型，purchase 充值、consume 消费、refund 退款、grant 发放、expire 过期作废"` // purchase | consume | refund | grant | expire
	AmountMicros          int64      `gorm:"not null;comment:本次变动金额，单位微元，正数为增加、负数为扣减"`
	BalanceAfterMicros    int64      `gorm:"not null;comment:本次变动后该桶的余额，单位微元，用于对账"`
	RefType               string     `gorm:"type:varchar(16);comment:关联业务类型，generation 生成请求、order 订单、admin 人工调整"`
	RefID                 string     `gorm:"type:varchar(64);index;comment:关联业务主键，与业务类型一起定位来源单据"`
	RefundOfTransactionID *uuid.UUID `gorm:"type:char(36);uniqueIndex;comment:被退还的那笔消费流水 id，仅退款流水有值，唯一约束防重复退款"`
	Note                  string     `gorm:"type:varchar(200);comment:流水备注，记录人工调整原因等"`
	CreatedAt             time.Time  `gorm:"index;comment:入账时间，流水按此排序"`
}

// UsageRecord 用量计数，按 (user_id, metric, period) 唯一，period 固定 total。
type UsageRecord struct {
	UserID    uuid.UUID `gorm:"type:char(36);primaryKey;uniqueIndex:idx_usage_user_metric,priority:1;comment:统计对象用户，与指标、周期组成联合主键"`
	Metric    string    `gorm:"type:varchar(32);primaryKey;uniqueIndex:idx_usage_user_metric,priority:2;comment:统计指标，如 storage_bytes 已用存储、free_image_trials 免费用图次数"`
	Period    string    `gorm:"type:varchar(16);primaryKey;uniqueIndex:idx_usage_user_metric,priority:3;comment:统计周期，当前固定 total 表示累计值"`
	Value     int64     `gorm:"not null;default:0;comment:指标累计值"`
	UpdatedAt time.Time `gorm:"comment:最近一次累加时间"`
}

// CreditPackage 点数包（credit_packages）：D5 拆分后只承载点数属性，
// 订阅属性（会员时长）在 membership_plans。
type CreditPackage struct {
	ID          string `gorm:"primaryKey;type:varchar(32);comment:套餐标识，管理后台创建时指定"`
	Name        string `gorm:"type:varchar(80);not null;comment:套餐显示名"`
	PriceMicros int64  `gorm:"not null;comment:套餐售价，单位微元（1 元 = 1000000 微元）"`
	BonusMicros int64  `gorm:"not null;default:0;comment:套餐附赠点数，单位微元，支付成功后与购买额一起入账"`
	Currency    string `gorm:"type:varchar(8);not null;default:CNY;comment:计价货币，如 CNY"`
	Enabled     bool   `gorm:"not null;default:true;comment:是否上架，下架后前台不可见且不可下单"`
	Sort        int    `gorm:"not null;default:0;comment:展示排序，数值小的排前面"`
}

type Order struct {
	ID              uuid.UUID  `gorm:"type:char(36);primaryKey;comment:订单主键"`
	UserID          uuid.UUID  `gorm:"type:char(36);index:idx_order_user_created,priority:1;index:idx_order_status_created,priority:1;not null;comment:下单用户"`
	Product         string     `gorm:"type:varchar(32);not null;default:youc-canvas;comment:下单产品标识，共享余额下按产品分账"`
	Provider        string     `gorm:"type:varchar(16);not null;comment:支付渠道，alipay 支付宝、wechat 微信支付"` // alipay | wechat
	ProviderOrderID *string    `gorm:"type:varchar(128);uniqueIndex;comment:支付渠道侧订单号，用于回调对账，未支付前为空"`
	PackageID       string     `gorm:"type:varchar(32);not null;default:'';comment:购买的点数包标识，指向 credit_packages；购买会员时为空"`
	PlanID          *string    `gorm:"type:varchar(32);index;comment:购买的会员档位，指向 membership_plans；购买点数包时为空"`
	PriceMicros     int64      `gorm:"not null;comment:订单实付金额，单位微元（1 元 = 1000000 微元）"`
	Currency        string     `gorm:"type:varchar(8);not null;comment:结算货币，如 CNY"`
	PurchasedMicros int64      `gorm:"not null;comment:本单入账到充值桶的点数，单位微元"`
	GrantedMicros   int64      `gorm:"not null;comment:本单入账到赠送桶的点数，单位微元，含套餐附赠"`
	EntitlementDays int        `gorm:"not null;default:0;comment:本单带来的付费权益天数；点数包订单为 0，会员订单为档位周期快照"`
	Status          string     `gorm:"type:varchar(16);not null;default:pending;index:idx_order_status_created,priority:2;comment:订单状态，pending 待支付、paid 已支付、failed 支付失败、refunded 已退款"`
	PaidAt          *time.Time `gorm:"comment:支付成功时间，未支付为空"`
	CreatedAt       time.Time  `gorm:"index:idx_order_user_created,priority:2,sort:desc;comment:下单时间"`
	UpdatedAt       time.Time  `gorm:"comment:订单状态最近变更时间"`
}

// ModelCatalog 平台模型目录，是前端唯一的模型来源。
// constraints 与 credit_cost 的键集与第四期转发接口共用，改一侧必须同步另一侧。
type ModelCatalog struct {
	ID                uuid.UUID      `gorm:"type:char(36);primaryKey;comment:模型主键"`
	Name              string         `gorm:"type:varchar(120);not null;uniqueIndex;comment:模型标识，前端与上游请求都用它，全表唯一"`
	DisplayName       string         `gorm:"type:varchar(120);not null;comment:模型展示名"`
	Capability        string         `gorm:"type:varchar(16);not null;index;comment:能力类型，image 图片、video 视频、text 文本、audio 音频"` // image | video | text | audio
	Provider          string         `gorm:"type:varchar(60);not null;comment:上游厂商名，用于选择适配器"`
	Constraints       datatypes.JSON `gorm:"type:json;comment:可选参数白名单 JSON，按维度限定取值范围并给出全部可选值"`
	CreditCost        datatypes.JSON `gorm:"type:json;comment:价格矩阵 JSON，按维度组合给出单次点数消耗与价格版本号"`
	ChannelIDs        datatypes.JSON `gorm:"type:json;comment:可用平台渠道 id 数组 JSON，顺序即故障转移顺序"` // 有序的平台渠道 id 数组，顺序即故障转移顺序
	FreeTrialEligible bool           `gorm:"not null;default:false;index;comment:是否参与免费试用额度"`
	Enabled           bool           `gorm:"not null;default:true;comment:是否上架，下架后前台不可见"`
	Sort              int            `gorm:"not null;default:0;comment:展示排序，数值小的排前面"`
	CreatedAt         time.Time      `gorm:"comment:创建时间"`
	UpdatedAt         time.Time      `gorm:"comment:最近更新时间"`
}

type ModelPricePromotion struct {
	ID          uuid.UUID      `gorm:"type:char(36);primaryKey;comment:促销主键"`
	ModelID     uuid.UUID      `gorm:"type:char(36);not null;index;comment:适用的模型，指向 model_catalogs"`
	Name        string         `gorm:"type:varchar(120);not null;comment:促销名称"`
	MatchParams datatypes.JSON `gorm:"type:json;not null;comment:命中条件 JSON，键值需与请求参数一致才应用折扣"`
	DiscountBPS int            `gorm:"not null;comment:折扣基点，8000 表示八折即实付为原价的百分之八十"` // 8000 = 8 折
	Priority    int            `gorm:"not null;default:0;comment:同模型多条促销的优先级，数值大的先命中"`
	Version     int            `gorm:"not null;default:1;comment:促销版本号，改价后递增，旧报价凭证据此判定失效"`
	Status      string         `gorm:"type:varchar(16);not null;default:draft;index;comment:促销状态，draft 草稿、scheduled 待开始、active 生效中、ended 已结束、disabled 已停用"` // draft | scheduled | active | ended | disabled
	StartsAt    time.Time      `gorm:"not null;comment:生效开始时间"`
	EndsAt      time.Time      `gorm:"not null;comment:生效结束时间"`
	CreatedAt   time.Time      `gorm:"comment:创建时间"`
	UpdatedAt   time.Time      `gorm:"comment:最近更新时间"`
}

// AdminAuditLog 管理操作审计，只能追加。摘要只保存脱敏后的价格、折扣、状态与版本。
type AdminAuditLog struct {
	ID            uuid.UUID      `gorm:"type:char(36);primaryKey;comment:审计日志主键"`
	ActorUserID   uuid.UUID      `gorm:"type:char(36);index;not null;comment:操作人用户"`
	Action        string         `gorm:"type:varchar(64);not null;index;comment:操作类型标识，如创建促销、调整套餐"`
	TargetType    string         `gorm:"type:varchar(32);not null;comment:操作对象类型，如 model_promotion、credit_package"`
	TargetID      string         `gorm:"type:varchar(64);not null;index;comment:操作对象主键"`
	RequestID     string         `gorm:"type:varchar(64);comment:请求追踪 id，可串联同一次操作产生的日志"`
	BeforeSummary datatypes.JSON `gorm:"type:json;comment:变更前摘要 JSON，只保留脱敏后的价格、折扣、状态与版本"`
	AfterSummary  datatypes.JSON `gorm:"type:json;comment:变更后摘要 JSON，字段与变更前摘要一致"`
	Reason        string         `gorm:"type:varchar(200);comment:操作原因，由操作人填写"`
	CreatedAt     time.Time      `gorm:"index;comment:操作时间"`
}
