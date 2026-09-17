package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// PlatformChannel 是平台自己的上游渠道，API Key 用 CREDENTIAL_MASTER_KEY 加密落库。
// 全服务只有这一把主密钥，解密后的明文只在发起上游请求的瞬间停留。
type PlatformChannel struct {
	ID         uuid.UUID `gorm:"type:char(36);primaryKey;comment:渠道主键"`
	Name       string    `gorm:"type:varchar(80);not null;comment:渠道名称，供管理后台识别"`
	BaseURL    string    `gorm:"type:varchar(300);not null;comment:上游接口基础地址，末尾斜杠会被去掉"`
	APIFormat  string    `gorm:"type:varchar(16);not null;comment:接口协议，openai、gemini、ark，决定使用哪个适配器"` // openai | gemini | ark
	Nonce      []byte    `gorm:"not null;comment:AES-256-GCM 加密使用的随机 nonce 原始字节，不是明文内容"`
	Payload    []byte    `gorm:"not null;comment:API Key 经 AES-256-GCM 加密后的密文，配 CREDENTIAL_MASTER_KEY 解密，库里不存明文 Key"` // AES-256-GCM 加密后的 API Key
	KeyVersion int       `gorm:"not null;default:1;comment:加密密钥版本号，用于主密钥轮换后识别旧密文"`
	Priority   int       `gorm:"not null;default:0;comment:同模型多渠道的调用优先级，数值小的先尝试"`
	Enabled    bool      `gorm:"not null;default:true;comment:是否启用，停用后不参与故障转移"`
	CreatedAt  time.Time `gorm:"comment:创建时间"`
	UpdatedAt  time.Time `gorm:"comment:最近更新时间"`
}

// AIRequest 记录一次生成请求的计费快照与状态。同步能力在进程重启后由启动收敛置为 failed。
type AIRequest struct {
	ID               uuid.UUID      `gorm:"type:char(36);primaryKey;comment:请求主键"`
	UserID           uuid.UUID      `gorm:"type:char(36);not null;index:idx_ai_request_user_created,priority:1;uniqueIndex:idx_ai_request_idempotency,priority:1;comment:发起用户，与幂等键组成唯一约束"`
	IdempotencyKey   *string        `gorm:"type:varchar(40);uniqueIndex:idx_ai_request_idempotency,priority:2;comment:客户端幂等键，同一用户用相同键重复提交只会落一条请求，为空表示不校验幂等"`
	Capability       string         `gorm:"type:varchar(16);not null;comment:能力类型，image 图片、video 视频、audio 音频、text 文本"` // image | video | audio | text
	Model            string         `gorm:"type:varchar(120);not null;comment:模型标识，取自 model_catalogs.name"`
	ChannelID        *uuid.UUID     `gorm:"type:char(36);comment:实际调用的平台渠道，未选到渠道时为空"`
	BaseCostMicros   int64          `gorm:"not null;default:0;comment:折扣前的原始点数，单位微元"`
	FinalCostMicros  int64          `gorm:"not null;default:0;comment:折扣后实际预扣的点数，单位微元"`
	PriceVersion     int            `gorm:"not null;default:0;comment:报价时读取的价格矩阵版本号，用于判断报价是否过期"`
	PromotionID      *uuid.UUID     `gorm:"type:char(36);comment:命中的促销 id，未命中折扣时为空"`
	PromotionVersion *int           `gorm:"comment:命中的促销版本号，未命中折扣时为空"`
	PromotionEnabled bool           `gorm:"not null;default:true;comment:下单时折扣总开关的快照，用于解释当时是否应用了促销"`
	PricingSnapshot  datatypes.JSON `gorm:"type:json;comment:报价快照 JSON，含参数、价格明细与折扣，便于事后回溯"`
	// ConsumeTransactionIDs 记录本次预扣写下的消费流水 id，失败退还按这些 id 原桶退回。
	ConsumeTransactionIDs datatypes.JSON `gorm:"type:json;comment:本次预扣写下的消费流水 id 数组，失败退款按这些 id 原桶退回"`
	Status                string         `gorm:"type:varchar(16);not null;index;comment:请求状态，running 进行中、succeeded 成功、failed 失败"` // running | succeeded | failed
	RefundPending         bool           `gorm:"not null;default:false;comment:退款待重试标记，为真表示预扣尚未退还，由补偿任务按该标记重试"`
	UpstreamStatus        int            `gorm:"comment:上游返回的 HTTP 状态码，未拿到响应时为 0"`
	DurationMs            int            `gorm:"comment:上游调用耗时，单位毫秒"`
	// 以下四个是用量分析维度，只在请求创建时写入一次，幂等命中既有请求时不回写。
	SessionID *string        `gorm:"type:varchar(64);index;comment:客户端会话标识，用于按会话统计活跃度，空表示未携带"`
	Params    datatypes.JSON `gorm:"type:json;comment:请求参数快照 JSON，报价参数按字符串键值序列化，空表示无参数"`
	ParamSpec string         `gorm:"type:varchar(64);not null;default:'';index;comment:主规格串，image 取 size、video 取 resolution、audio 取 voice，文本对话为空"`
	StatDate  string         `gorm:"type:varchar(10);not null;default:'';index;comment:统计日期，创建时间按 UTC+8 日界格式化为 2006-01-02，过滤时用字符串比较"`
	CreatedAt time.Time      `gorm:"index:idx_ai_request_user_created,priority:2,sort:desc;comment:创建时间"`
	UpdatedAt time.Time      `gorm:"comment:状态最近变更时间"`
}

// AITask 是视频等异步能力的上游任务。服务端主动轮询，进程重启后按非终态恢复。
type AITask struct {
	ID             uuid.UUID `gorm:"type:char(36);primaryKey;comment:异步任务主键"`
	UserID         uuid.UUID `gorm:"type:char(36);not null;index:idx_ai_task_user,priority:1;comment:发起用户"`
	RequestID      uuid.UUID `gorm:"type:char(36);not null;comment:对应的请求，指向 ai_requests"`
	Provider       string    `gorm:"type:varchar(16);not null;comment:上游厂商标识，决定轮询间隔与适配器"`
	UpstreamTaskID string    `gorm:"type:varchar(200);not null;comment:上游返回的任务 id，轮询时回传"`
	Status         string    `gorm:"type:varchar(16);not null;index:idx_ai_task_status_updated,priority:1;comment:任务状态，pending 进行中、succeeded 成功、failed 失败"` // pending | succeeded | failed
	GenerationID   uuid.UUID `gorm:"type:char(36);comment:成功后写入的生成记录 id，未完成时为空"`
	StorageKey     string    `gorm:"type:varchar(80);comment:产物落盘后的存储键，未完成时为空"`
	Error          string    `gorm:"type:text;comment:失败原因文本"`
	PollFailures   int       `gorm:"not null;default:0;comment:连续轮询失败次数，用于退避与放弃判定"`
	CreatedAt      time.Time `gorm:"comment:创建时间"`
	UpdatedAt      time.Time `gorm:"index:idx_ai_task_status_updated,priority:2;comment:最近轮询或状态变更时间"`
}

// PollAfterMs 返回该任务建议的轮询间隔。不同厂商的合理间隔差别很大，
// 这个知识跟着 provider 走，前端不再硬编码。
func (t AITask) PollAfterMs() int {
	switch t.Provider {
	case "gemini":
		return 10000
	default:
		return 5000
	}
}
