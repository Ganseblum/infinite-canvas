package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// OAuthClient 是 OIDC 接入客户端（oauth_clients，PLAN-PLATFORM-ACCOUNT-MEMBERSHIP T09）：
// 未来 blog 等新产品以标准 OAuth + PKCE 接入平台登录。client_secret 只存 SHA-256 摘要
// 不存明文（评审门④：secret 的存储口径）；redirect_uri 授权时必须与注册列表之一完全一致。
type OAuthClient struct {
	ID uuid.UUID `gorm:"type:char(36);primaryKey;comment:客户端主键"`
	// Product 与 /admin/meta 的 product.id 同值，画布侧固定 ProductCanvas（youc-canvas）。
	Product          string         `gorm:"type:varchar(32);not null;comment:所属产品标识，与 /admin/meta 的 product.id 同值，画布侧固定 youc-canvas"`
	Name             string         `gorm:"type:varchar(120);not null;comment:客户端显示名"`
	ClientID         string         `gorm:"type:varchar(64);uniqueIndex;not null;comment:客户端 ID，ic_ + 32 位十六进制随机数"`
	ClientSecretHash string         `gorm:"type:varchar(128);not null;comment:客户端密钥的 SHA-256 十六进制摘要，不存明文"`
	RedirectURIs     datatypes.JSON `gorm:"type:json;not null;comment:注册的回调地址 JSON 数组，授权时 redirect_uri 必须与其一完全一致"`
	Enabled          bool           `gorm:"not null;default:true;comment:是否启用，停用后拒绝授权与签发"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// TableName 固定表名为 oauth_clients（PLAN T09 契约表名）。
func (OAuthClient) TableName() string { return "oauth_clients" }
