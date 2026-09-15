package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/smartwalle/alipay/v3"

	"github.com/infinite-canvas/server/internal/model"
)

// AlipayConfig 是支付宝渠道配置，全部来自环境变量。
type AlipayConfig struct {
	AppID           string
	PrivateKey      string
	AlipayPublicKey string
	NotifyURL       string
	ReturnURL       string
	Production      bool
}

type alipayProvider struct {
	client *alipay.Client
	cfg    AlipayConfig
}

// NewAlipay 使用官方 SDK 组装支付宝渠道。私钥或公钥缺失时返回错误，启动阶段即可发现。
func NewAlipay(cfg AlipayConfig) (Provider, error) {
	if cfg.AppID == "" || cfg.PrivateKey == "" || cfg.AlipayPublicKey == "" {
		return nil, errors.New("支付宝配置不完整：需要 ALIPAY_APP_ID、ALIPAY_PRIVATE_KEY、ALIPAY_PUBLIC_KEY")
	}
	client, err := alipay.New(cfg.AppID, cfg.PrivateKey, cfg.Production)
	if err != nil {
		return nil, fmt.Errorf("初始化支付宝客户端失败: %w", err)
	}
	if err := client.LoadAliPayPublicKey(cfg.AlipayPublicKey); err != nil {
		return nil, fmt.Errorf("加载支付宝公钥失败: %w", err)
	}
	return &alipayProvider{client: client, cfg: cfg}, nil
}

func (p *alipayProvider) Name() string { return "alipay" }

// CreatePayment 走电脑网站支付，返回可直接打开的收银台地址。
func (p *alipayProvider) CreatePayment(_ context.Context, order *model.Order) (Params, string, error) {
	payURL, err := p.client.TradePagePay(alipay.TradePagePay{
		Trade: alipay.Trade{
			Subject:     "优刻点数",
			OutTradeNo:  order.ID.String(),
			TotalAmount: microsToYuan(order.PriceMicros),
			ProductCode: "FAST_INSTANT_TRADE_PAY",
			NotifyURL:   p.cfg.NotifyURL,
			ReturnURL:   p.cfg.ReturnURL,
		},
	})
	if err != nil {
		return Params{}, "", fmt.Errorf("支付宝下单失败: %w", err)
	}
	return Params{Type: PaymentTypeRedirect, Payload: payURL.String()}, "", nil
}

// VerifyAndParse 校验异步通知签名并解析统一回调结果，金额只用于与本地订单快照比较。
func (p *alipayProvider) VerifyAndParse(ctx context.Context, r *http.Request) (CallbackResult, error) {
	if err := r.ParseForm(); err != nil {
		return CallbackResult{}, fmt.Errorf("解析支付宝回调失败: %w", err)
	}
	notification, err := p.client.DecodeNotification(ctx, r.PostForm)
	if err != nil {
		return CallbackResult{}, ErrInvalidSignature
	}
	if notification.AppId != "" && notification.AppId != p.cfg.AppID {
		return CallbackResult{}, ErrInvalidSignature
	}
	status := "pending"
	switch notification.TradeStatus {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		status = "paid"
	case alipay.TradeStatusClosed:
		status = "closed"
	}
	amountMicros, err := p.PaidAmountMicros(notification.TotalAmount)
	if err != nil {
		return CallbackResult{}, err
	}
	return CallbackResult{
		OutTradeNo:      notification.OutTradeNo,
		ProviderOrderID: notification.TradeNo,
		PriceMicros:     amountMicros,
		Status:          status,
	}, nil
}

// QueryOrder 主动查询，既是超时订单扫描的兜底，也是回调整体丢失时的补偿。
func (p *alipayProvider) QueryOrder(ctx context.Context, order *model.Order) (CallbackResult, error) {
	resp, err := p.client.TradeQuery(ctx, alipay.TradeQuery{OutTradeNo: order.ID.String()})
	if err != nil {
		return CallbackResult{}, fmt.Errorf("查询支付宝订单失败: %w", err)
	}
	if resp.Error.Code != "" && !resp.Error.IsSuccess() {
		// 订单在渠道侧不存在等业务错误视为「尚未支付」，由调用方决定是否置失败。
		return CallbackResult{OutTradeNo: order.ID.String(), Status: "pending"}, nil
	}
	status := "pending"
	switch resp.TradeStatus {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		status = "paid"
	case alipay.TradeStatusClosed:
		status = "closed"
	}
	amountMicros, err := p.PaidAmountMicros(resp.TotalAmount)
	if err != nil {
		return CallbackResult{}, err
	}
	return CallbackResult{
		OutTradeNo:      order.ID.String(),
		ProviderOrderID: resp.TradeNo,
		PriceMicros:     amountMicros,
		Status:          status,
	}, nil
}

// PaidAmountMicros 把支付宝返回的元字符串换算成微元。
func (p *alipayProvider) PaidAmountMicros(amount string) (int64, error) {
	yuan, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return 0, fmt.Errorf("支付宝金额无法解析: %s", amount)
	}
	return int64(yuan*1_000_000 + 0.5), nil
}

func microsToYuan(micros int64) string {
	return strconv.FormatFloat(float64(micros)/1_000_000, 'f', 2, 64)
}
