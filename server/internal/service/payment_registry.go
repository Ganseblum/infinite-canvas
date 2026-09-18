package service

import (
	"errors"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/payment"
)

// PaymentRegistry 持有当前已配置的支付渠道。未配置的渠道自动不上架，
// 前端拿到的可选渠道与后端下单校验共用这一份数据。
type PaymentRegistry struct {
	providers map[string]payment.Provider
}

// NewPaymentRegistry 依据环境变量组装渠道，配置缺失或非法时返回明确的错误，
// 与第一期校验 SMTP 变量的做法一致：配了就必须是对的，不配则渠道自动不上架。
func NewPaymentRegistry(cfg *config.Config) (*PaymentRegistry, error) {
	registry := &PaymentRegistry{providers: map[string]payment.Provider{}}
	if cfg.AlipayAppID != "" {
		provider, err := payment.NewAlipay(payment.AlipayConfig{
			AppID:           cfg.AlipayAppID,
			PrivateKey:      cfg.AlipayPrivateKey,
			AlipayPublicKey: cfg.AlipayPublicKey,
			NotifyURL:       cfg.PaymentNotifyURL + "/alipay",
			ReturnURL:       cfg.AlipayReturnURL,
			Production:      cfg.AlipayProduction,
		})
		if err != nil {
			return nil, err
		}
		registry.providers[provider.Name()] = provider
	}
	if cfg.WechatAppID != "" {
		provider, err := payment.NewWechat(payment.WechatConfig{
			AppID:         cfg.WechatAppID,
			MchID:         cfg.WechatMchID,
			MchSerialNo:   cfg.WechatMchSerialNo,
			MchPrivateKey: cfg.WechatMchPrivateKey,
			APIv3Key:      cfg.WechatAPIv3Key,
			NotifyURL:     cfg.PaymentNotifyURL + "/wechat",
		})
		if err != nil {
			return nil, err
		}
		registry.providers[provider.Name()] = provider
	}
	// EASYPAY_ENABLED=true 时注册易支付，配置缺失或非法直接报启动错误；不开启则渠道自动不上架。
	if cfg.EasyPayEnabled {
		provider, err := payment.NewEasyPay(payment.EasyPayConfig{
			APIBase:   cfg.EasyPayAPIBase,
			PID:       cfg.EasyPayPID,
			Key:       cfg.EasyPayKey,
			Type:      cfg.EasyPayType,
			NotifyURL: cfg.PaymentNotifyURL + "/easypay",
			ReturnURL: cfg.EasyPayReturnURL,
		})
		if err != nil {
			return nil, err
		}
		registry.providers[provider.Name()] = provider
	}
	return registry, nil
}

// NewPaymentRegistryForTest 用给定的渠道构造注册表，供测试注入替身。
func NewPaymentRegistryForTest(providers []payment.Provider) *PaymentRegistry {
	registry := &PaymentRegistry{providers: map[string]payment.Provider{}}
	for _, provider := range providers {
		registry.providers[provider.Name()] = provider
	}
	return registry
}

// Get 返回指定渠道，未配置时返回 false。
func (r *PaymentRegistry) Get(name string) (payment.Provider, bool) {
	provider, ok := r.providers[name]
	return provider, ok
}

// Names 返回已配置渠道的标识列表。
func (r *PaymentRegistry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// Has 判断渠道是否已配置。
func (r *PaymentRegistry) Has(name string) bool {
	_, ok := r.providers[name]
	return ok
}

// ErrProviderUnavailable 表示请求的支付渠道未配置。
var ErrProviderUnavailable = errors.New("支付渠道未配置")
