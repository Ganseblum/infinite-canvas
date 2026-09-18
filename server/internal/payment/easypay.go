package payment

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/infinite-canvas/server/internal/model"
)

// EasyPayConfig 是易支付（彩虹协议）渠道配置，全部来自环境变量。
type EasyPayConfig struct {
	APIBase   string // 网关地址，自动去掉尾斜杠与 submit.php/mapi.php/api.php 后缀
	PID       string // 商户 ID
	Key       string // 商户密钥，仅服务端保存，禁止出现在日志与响应中
	Type      string // 支付方式：alipay | wxpay，默认 alipay
	NotifyURL string // 异步通知地址
	ReturnURL string // 支付完成后的浏览器回跳地址
}

type easypayProvider struct {
	cfg    EasyPayConfig
	client *http.Client
}

// NewEasyPay 组装易支付渠道。配置缺失或非法时返回错误，启动阶段即可发现。
func NewEasyPay(cfg EasyPayConfig) (Provider, error) {
	if cfg.APIBase == "" || cfg.PID == "" || cfg.Key == "" {
		return nil, errors.New("易支付配置不完整：需要 EASYPAY_API_BASE、EASYPAY_PID、EASYPAY_KEY")
	}
	if cfg.Type == "" {
		cfg.Type = "alipay"
	}
	if cfg.Type != "alipay" && cfg.Type != "wxpay" {
		return nil, fmt.Errorf("EASYPAY_TYPE 取值非法: %s（只能是 alipay 或 wxpay）", cfg.Type)
	}
	cfg.APIBase = strings.TrimRight(cfg.APIBase, "/")
	for _, suffix := range []string{"/submit.php", "/mapi.php", "/api.php"} {
		cfg.APIBase = strings.TrimSuffix(cfg.APIBase, suffix)
	}
	return &easypayProvider{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (p *easypayProvider) Name() string { return "easypay" }

// easypaySignContent 构造待签名串：剔除 sign/sign_type 与空值后，按 key 的 ASCII 升序拼 k=v&。
func easypaySignContent(params map[string]string) string {
	names := make([]string, 0, len(params))
	for name, value := range params {
		if name == "sign" || name == "sign_type" || value == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+params[name])
	}
	return strings.Join(pairs, "&")
}

// easypaySign 按彩虹协议签名：待签名串末尾直接追加商户密钥（不加 &key=），取 MD5 小写十六进制。
func easypaySign(params map[string]string, key string) string {
	sum := md5.Sum([]byte(easypaySignContent(params) + key))
	return hex.EncodeToString(sum[:])
}

// CreatePayment 生成 submit.php 收银台跳转地址，前端拿到后整页跳转。
func (p *easypayProvider) CreatePayment(_ context.Context, order *model.Order) (Params, string, error) {
	params := map[string]string{
		"pid":          p.cfg.PID,
		"type":         p.cfg.Type,
		"out_trade_no": order.ID.String(),
		"notify_url":   p.cfg.NotifyURL,
		"return_url":   p.cfg.ReturnURL,
		"name":         "优刻点数",
		"money":        microsToYuan(order.PriceMicros),
	}
	params["sign"] = easypaySign(params, p.cfg.Key)
	params["sign_type"] = "MD5"
	query := url.Values{}
	for name, value := range params {
		if value == "" {
			continue
		}
		query.Set(name, value)
	}
	return Params{Type: PaymentTypeRedirect, Payload: p.cfg.APIBase + "/submit.php?" + query.Encode()}, "", nil
}

// VerifyAndParse 校验易支付异步通知（GET/POST）并解析统一回调结果。
// money 解析失败或为 0 一律报错而不是返回 0，避免金额为 0 时跳过订单快照比对。
func (p *easypayProvider) VerifyAndParse(_ context.Context, r *http.Request) (CallbackResult, error) {
	if err := r.ParseForm(); err != nil {
		return CallbackResult{}, fmt.Errorf("解析易支付回调失败: %w", err)
	}
	values := make(map[string]string, len(r.Form))
	for name, v := range r.Form {
		if len(v) > 0 {
			values[name] = v[0]
		}
	}
	// ParseForm 已完成 URL 解码，不再重复解码；恒时比较防止逐字节猜测签名。
	if subtle.ConstantTimeCompare([]byte(easypaySign(values, p.cfg.Key)), []byte(strings.ToLower(values["sign"]))) != 1 {
		return CallbackResult{}, ErrInvalidSignature
	}
	status := "pending"
	switch values["trade_status"] {
	case "TRADE_SUCCESS":
		status = "paid"
	case "TRADE_CLOSED":
		status = "closed"
	}
	result := CallbackResult{
		OutTradeNo:      values["out_trade_no"],
		ProviderOrderID: values["trade_no"],
		Status:          status,
	}
	if status == "paid" {
		amountMicros, err := p.PaidAmountMicros(values["money"])
		if err != nil || amountMicros <= 0 {
			return CallbackResult{}, fmt.Errorf("易支付回调金额无效: %q", values["money"])
		}
		result.PriceMicros = amountMicros
	}
	return result, nil
}

// QueryOrder 走 api.php 主动查询，供超时订单扫描兜底。密钥放在 POST 表单里，
// 报错时不携带 URL 与原始响应体，避免密钥或网关页面内容泄漏到日志。
func (p *easypayProvider) QueryOrder(ctx context.Context, order *model.Order) (CallbackResult, error) {
	form := url.Values{
		"act":          {"order"},
		"pid":          {p.cfg.PID},
		"key":          {p.cfg.Key},
		"out_trade_no": {order.ID.String()},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.APIBase+"/api.php", strings.NewReader(form.Encode()))
	if err != nil {
		return CallbackResult{}, fmt.Errorf("构造易支付查单请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("查询易支付订单失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CallbackResult{}, fmt.Errorf("读取易支付查单响应失败: HTTP %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return CallbackResult{}, fmt.Errorf("解析易支付查单响应失败: HTTP %d", resp.StatusCode)
	}
	// 不同易支付实现的状态字段在顶层或 data 嵌套，顶层没有状态信息时回退到 data。
	view := raw
	if data, ok := raw["data"].(map[string]any); ok &&
		easypayMapString(raw, "trade_status") == "" && easypayMapString(raw, "status") == "" {
		view = data
	}
	switch tradeStatus, status := easypayMapString(view, "trade_status"), easypayMapString(view, "status"); {
	case tradeStatus == "TRADE_SUCCESS" || status == "1" || status == "TRADE_SUCCESS":
		amountMicros, err := p.PaidAmountMicros(easypayMapString(view, "money"))
		if err != nil || amountMicros <= 0 {
			return CallbackResult{}, fmt.Errorf("易支付查单金额无效: %q", easypayMapString(view, "money"))
		}
		return CallbackResult{
			OutTradeNo:      order.ID.String(),
			ProviderOrderID: easypayMapString(view, "trade_no"),
			PriceMicros:     amountMicros,
			Status:          "paid",
		}, nil
	case tradeStatus == "TRADE_CLOSED" || status == "TRADE_CLOSED":
		return CallbackResult{OutTradeNo: order.ID.String(), Status: "closed"}, nil
	default:
		// 状态未知时保持待支付，由调用方决定后续，不误杀。
		return CallbackResult{OutTradeNo: order.ID.String(), Status: "pending"}, nil
	}
}

// PaidAmountMicros 把易支付返回的元字符串换算成微元，解析失败必须返回错误而不是 0。
func (p *easypayProvider) PaidAmountMicros(amount string) (int64, error) {
	yuan, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil {
		return 0, fmt.Errorf("易支付金额无法解析: %s", amount)
	}
	return int64(yuan*1_000_000 + 0.5), nil
}

// easypayMapString 从查单 JSON 里取字符串字段，兼容字符串与数字两种形态。
func easypayMapString(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}
