package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"

	"github.com/infinite-canvas/server/internal/model"
)

// WechatConfig 是微信支付渠道配置，全部来自环境变量。
type WechatConfig struct {
	AppID         string
	MchID         string
	MchSerialNo   string
	MchPrivateKey string // PEM 文件路径
	APIv3Key      string
	NotifyURL     string
}

type wechatProvider struct {
	api     *native.NativeApiService
	handler *notify.Handler
	cfg     WechatConfig
}

// NewWechat 使用官方 SDK 组装微信支付渠道：签名、验签与敏感字段解密全部交给 SDK，
// 平台证书由 SDK 自动下载并定时更新。配置缺失或非法时返回错误，启动阶段即可发现。
func NewWechat(cfg WechatConfig) (Provider, error) {
	if cfg.AppID == "" || cfg.MchID == "" || cfg.MchSerialNo == "" || cfg.MchPrivateKey == "" || cfg.APIv3Key == "" {
		return nil, errors.New("微信支付配置不完整：需要 WECHATPAY_APP_ID、WECHATPAY_MCH_ID、WECHATPAY_MCH_SERIAL_NO、WECHATPAY_MCH_PRIVATE_KEY、WECHATPAY_API_V3_KEY")
	}
	if len(cfg.APIv3Key) != 32 {
		return nil, errors.New("WECHATPAY_API_V3_KEY 必须为 32 字节")
	}
	privateKey, err := utils.LoadPrivateKeyWithPath(cfg.MchPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("加载微信支付商户私钥失败: %w", err)
	}
	ctx := context.Background()
	client, err := core.NewClient(ctx,
		option.WithWechatPayAutoAuthCipher(cfg.MchID, cfg.MchSerialNo, privateKey, cfg.APIv3Key),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化微信支付客户端失败: %w", err)
	}
	// 通知处理共用同一套平台证书验签能力，回调验签失败一律拒绝。
	visitor := downloader.MgrInstance().GetCertificateVisitor(cfg.MchID)
	handler, err := notify.NewRSANotifyHandler(cfg.APIv3Key, verifiers.NewSHA256WithRSAVerifier(visitor))
	if err != nil {
		return nil, fmt.Errorf("初始化微信支付回调处理器失败: %w", err)
	}
	return &wechatProvider{
		api:     &native.NativeApiService{Client: client},
		handler: handler,
		cfg:     cfg,
	}, nil
}

func (p *wechatProvider) Name() string { return "wechat" }

// CreatePayment 走 Native 扫码支付，返回二维码链接。
func (p *wechatProvider) CreatePayment(ctx context.Context, order *model.Order) (Params, string, error) {
	resp, _, err := p.api.Prepay(ctx, native.PrepayRequest{
		Appid:       &p.cfg.AppID,
		Mchid:       &p.cfg.MchID,
		Description: strPtr("优刻点数"),
		OutTradeNo:  strPtr(order.ID.String()),
		NotifyUrl:   &p.cfg.NotifyURL,
		Amount:      &native.Amount{Total: int64Ptr(OrderAmountMicrosToCents(order.PriceMicros))},
	})
	if err != nil {
		return Params{}, "", fmt.Errorf("微信支付下单失败: %w", err)
	}
	if resp.CodeUrl == nil {
		return Params{}, "", errors.New("微信支付未返回二维码链接")
	}
	return Params{Type: PaymentTypeQRCode, Payload: *resp.CodeUrl}, "", nil
}

// VerifyAndParse 校验回调签名并解密报文，金额取回调的分换算微元。
func (p *wechatProvider) VerifyAndParse(ctx context.Context, r *http.Request) (CallbackResult, error) {
	transaction := new(payments.Transaction)
	if _, err := p.handler.ParseNotifyRequest(ctx, r, transaction); err != nil {
		return CallbackResult{}, ErrInvalidSignature
	}
	if transaction.OutTradeNo == nil || transaction.TradeState == nil {
		return CallbackResult{}, errors.New("微信支付回调缺少订单号或状态")
	}
	status := "pending"
	switch *transaction.TradeState {
	case "SUCCESS":
		status = "paid"
	case "CLOSED", "REVOKED", "PAYERROR":
		status = "closed"
	}
	var amountMicros int64
	if transaction.Amount != nil && transaction.Amount.Total != nil {
		amountMicros = *transaction.Amount.Total * 10000
	}
	result := CallbackResult{
		OutTradeNo:  *transaction.OutTradeNo,
		PriceMicros: amountMicros,
		Status:      status,
	}
	if transaction.TransactionId != nil {
		result.ProviderOrderID = *transaction.TransactionId
	}
	return result, nil
}

// QueryOrder 主动查询渠道侧真实状态，供超时订单扫描兜底与回调丢失时补偿。
func (p *wechatProvider) QueryOrder(ctx context.Context, order *model.Order) (CallbackResult, error) {
	resp, _, err := p.api.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: strPtr(order.ID.String()),
		Mchid:      &p.cfg.MchID,
	})
	if err != nil {
		return CallbackResult{}, fmt.Errorf("查询微信支付订单失败: %w", err)
	}
	status := "pending"
	if resp.TradeState != nil {
		switch *resp.TradeState {
		case "SUCCESS":
			status = "paid"
		case "CLOSED", "REVOKED", "PAYERROR":
			status = "closed"
		}
	}
	var amountMicros int64
	if resp.Amount != nil && resp.Amount.Total != nil {
		amountMicros = *resp.Amount.Total * 10000
	}
	result := CallbackResult{OutTradeNo: order.ID.String(), PriceMicros: amountMicros, Status: status}
	if resp.TransactionId != nil {
		result.ProviderOrderID = *resp.TransactionId
	}
	return result, nil
}

// PaidAmountMicros 微信返回的金额单位是分，换算成微元。
func (p *wechatProvider) PaidAmountMicros(amount string) (int64, error) {
	cents, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("微信支付金额无法解析: %s", amount)
	}
	return cents * 10000, nil
}

func strPtr(value string) *string { return &value }

func int64Ptr(value int64) *int64 { return &value }
