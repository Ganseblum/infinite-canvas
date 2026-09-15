// Package payment 收敛支付宝与微信两个渠道的差异。
// handler 只认这里统一后的结构，接一个新渠道不应该改到 handler。
package payment

import (
	"context"
	"errors"
	"net/http"

	"github.com/infinite-canvas/server/internal/model"
)

// ErrInvalidSignature 表示回调验签失败，调用方映射为 400 INVALID_SIGNATURE。
var ErrInvalidSignature = errors.New("回调验签失败")

// PaymentType 是前端渲染支付方式的唯一依据，前端不写按渠道分支的判断。
type PaymentType string

const (
	PaymentTypeQRCode       PaymentType = "qrcode"
	PaymentTypeRedirect     PaymentType = "redirect"
	PaymentTypeClientSecret PaymentType = "clientSecret"
)

// Params 是下单成功后返回给前端的支付参数。
type Params struct {
	Type    PaymentType `json:"type"`
	Payload string      `json:"payload"`
}

// CallbackResult 是回调经各渠道适配函数统一后的结构。
type CallbackResult struct {
	OutTradeNo      string // 我方订单号，即 orders.id
	ProviderOrderID string // 渠道侧交易号
	PriceMicros     int64  // 渠道实收金额，微元
	Status          string // paid | pending | closed
}

// Provider 是支付渠道适配接口。验签一律交给各渠道官方 SDK，绝不手写签名逻辑。
type Provider interface {
	// Name 返回渠道标识，如 alipay、wechat。
	Name() string
	// CreatePayment 生成支付参数，同时返回渠道侧单号（没有时为空串）。
	CreatePayment(ctx context.Context, order *model.Order) (Params, string, error)
	// VerifyAndParse 校验回调签名并解析出统一结果，验签失败返回 ErrInvalidSignature。
	VerifyAndParse(ctx context.Context, r *http.Request) (CallbackResult, error)
	// QueryOrder 主动查询渠道侧真实状态，供超时订单扫描兜底。
	QueryOrder(ctx context.Context, order *model.Order) (CallbackResult, error)
	// PaidAmountMicros 把渠道返回的金额换算为微元。
	PaidAmountMicros(amount string) (int64, error)
}

// OrderAmountMicrosToCents 把微元换算成渠道要求的分，价格不是整分时调用方应提前拒绝。
func OrderAmountMicrosToCents(priceMicros int64) int64 {
	return priceMicros / 10000
}
