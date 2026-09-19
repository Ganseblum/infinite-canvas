package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
)

const easypayTestKey = "test-key-123"

// newTestEasyPay 组装一个指向给定网关地址的易支付渠道。
func newTestEasyPay(t *testing.T, apiBase string) Provider {
	t.Helper()
	provider, err := NewEasyPay(EasyPayConfig{
		APIBase:   apiBase,
		PID:       "1001",
		Key:       easypayTestKey,
		Type:      "alipay",
		NotifyURL: "https://app.example.com/api/payments/webhook/easypay",
		ReturnURL: "https://app.example.com/billing",
	})
	if err != nil {
		t.Fatalf("组装易支付渠道失败: %v", err)
	}
	return provider
}

// signedEasypayQuery 用测试密钥对参数签名并编码成回调查询串。
func signedEasypayQuery(params map[string]string) string {
	params["sign"] = easypaySign(params, easypayTestKey)
	params["sign_type"] = "MD5"
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	return query.Encode()
}

// TestEasypaySign 逐字符断言签名串构造：剔除 sign/sign_type/空值、key ASCII 升序、末尾直接追加密钥。
func TestEasypaySign(t *testing.T) {
	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ord-1",
		"notify_url":   "https://app.example.com/api/payments/webhook/easypay",
		"return_url":   "", // 空值必须剔除
		"name":         "优刻点数",
		"money":        "1.00",
		"sign":         "should-be-ignored",
		"sign_type":    "MD5",
	}
	want := "money=1.00&name=优刻点数&notify_url=https://app.example.com/api/payments/webhook/easypay&out_trade_no=ord-1&pid=1001&type=alipay"
	if got := easypaySignContent(params); got != want {
		t.Fatalf("待签名串不匹配:\n got  %s\n want %s", got, want)
	}
	// 待签名串末尾直接拼密钥后的 MD5 小写十六进制。
	if got := easypaySign(params, easypayTestKey); got != "5a02049884c84e65e5daf702fc86bdf3" {
		t.Fatalf("签名不匹配: %s", got)
	}
}

func TestNewEasyPayConfigValidation(t *testing.T) {
	if _, err := NewEasyPay(EasyPayConfig{APIBase: "https://pay.example.com"}); err == nil {
		t.Fatalf("缺少 PID/KEY 应报错")
	}
	if _, err := NewEasyPay(EasyPayConfig{APIBase: "https://pay.example.com", PID: "1001", Key: "k", Type: "qqpay"}); err == nil {
		t.Fatalf("非法 EASYPAY_TYPE 应报错")
	}
	// 网关地址应去掉 submit.php 后缀与尾斜杠。
	provider := newTestEasyPay(t, "https://pay.example.com/submit.php/")
	easy := provider.(*easypayProvider)
	if easy.cfg.APIBase != "https://pay.example.com" {
		t.Fatalf("网关地址归一化失败: %s", easy.cfg.APIBase)
	}
}

// TestEasyPayCreatePayment 断言下单生成 submit.php 收银台跳转地址，且参数可用同一密钥复算签名。
func TestEasyPayCreatePayment(t *testing.T) {
	provider := newTestEasyPay(t, "https://pay.example.com")
	order := &model.Order{ID: uuid.New(), PriceMicros: 1_500_000}
	params, providerOrderID, err := provider.CreatePayment(context.Background(), order)
	if err != nil {
		t.Fatalf("易支付下单失败: %v", err)
	}
	if providerOrderID != "" {
		t.Fatalf("页面跳转下单不应返回渠道单号: %s", providerOrderID)
	}
	if params.Type != PaymentTypeRedirect {
		t.Fatalf("易支付应走收银台跳转: %s", params.Type)
	}
	if !strings.HasPrefix(params.Payload, "https://pay.example.com/submit.php?") {
		t.Fatalf("下单地址错误: %s", params.Payload)
	}
	u, err := url.Parse(params.Payload)
	if err != nil {
		t.Fatalf("解析下单地址失败: %v", err)
	}
	q := u.Query()
	if q.Get("money") != "1.50" || q.Get("out_trade_no") != order.ID.String() ||
		q.Get("pid") != "1001" || q.Get("type") != "alipay" || q.Get("name") != "优刻点数" {
		t.Fatalf("下单参数错误: %v", q)
	}
	flat := map[string]string{}
	for name := range q {
		flat[name] = q.Get(name)
	}
	if easypaySign(flat, easypayTestKey) != q.Get("sign") {
		t.Fatalf("下单参数签名无法复算: %s", params.Payload)
	}
}

// TestEasyPayVerifyAndParse 覆盖验签通过、篡改拒绝、非 SUCCESS 不到账与无效金额拒绝。
func TestEasyPayVerifyAndParse(t *testing.T) {
	provider := newTestEasyPay(t, "https://pay.example.com")
	base := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": uuid.New().String(),
		"trade_no":     "T20260918001",
		"money":        "1.00",
		"trade_status": "TRADE_SUCCESS",
	}

	// 验签通过：GET 回调解析出统一结果。
	r := httptest.NewRequest(http.MethodGet, "/api/v1/payments/webhook/easypay?"+signedEasypayQuery(cloneStringMap(base)), nil)
	result, err := provider.VerifyAndParse(context.Background(), r)
	if err != nil {
		t.Fatalf("合法回调应验签通过: %v", err)
	}
	if result.OutTradeNo != base["out_trade_no"] || result.ProviderOrderID != "T20260918001" ||
		result.PriceMicros != 1_000_000 || result.Status != "paid" {
		t.Fatalf("回调解析结果错误: %+v", result)
	}

	// POST 表单回调同样支持。
	signed := cloneStringMap(base)
	signed["sign"] = easypaySign(signed, easypayTestKey)
	signed["sign_type"] = "MD5"
	form := url.Values{}
	for name, value := range signed {
		form.Set(name, value)
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook/easypay", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := provider.VerifyAndParse(context.Background(), r); err != nil {
		t.Fatalf("POST 回调应验签通过: %v", err)
	}

	// 篡改金额：保留原签名、替换 money，应验签失败。
	tampered := cloneStringMap(base)
	tampered["money"] = "99.00"
	query := url.Values{}
	for name, value := range tampered {
		query.Set(name, value)
	}
	query.Set("sign", easypaySign(base, easypayTestKey))
	query.Set("sign_type", "MD5")
	r = httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	if _, err := provider.VerifyAndParse(context.Background(), r); err != ErrInvalidSignature {
		t.Fatalf("篡改金额应返回 ErrInvalidSignature, got %v", err)
	}

	// 非 TRADE_SUCCESS 不到账：TRADE_CLOSED 解析为 closed 且不要求金额。
	closed := cloneStringMap(base)
	closed["trade_status"] = "TRADE_CLOSED"
	delete(closed, "money")
	r = httptest.NewRequest(http.MethodGet, "/?"+signedEasypayQuery(closed), nil)
	result, err = provider.VerifyAndParse(context.Background(), r)
	if err != nil {
		t.Fatalf("关闭通知不应报错: %v", err)
	}
	if result.Status != "closed" || result.PriceMicros != 0 {
		t.Fatalf("关闭通知解析错误: %+v", result)
	}

	// 已支付但金额缺失或为 0：必须报错而不是返回 0 跳过订单快照比对。
	noMoney := cloneStringMap(base)
	delete(noMoney, "money")
	r = httptest.NewRequest(http.MethodGet, "/?"+signedEasypayQuery(noMoney), nil)
	if _, err := provider.VerifyAndParse(context.Background(), r); err == nil {
		t.Fatalf("已支付通知缺少金额应报错")
	}
	zero := cloneStringMap(base)
	zero["money"] = "0.00"
	r = httptest.NewRequest(http.MethodGet, "/?"+signedEasypayQuery(zero), nil)
	if _, err := provider.VerifyAndParse(context.Background(), r); err == nil {
		t.Fatalf("已支付通知金额为 0 应报错")
	}

	// 金额字段不是数字：PaidAmountMicros 必须返回错误而不是 0。
	if _, err := provider.PaidAmountMicros("abc"); err == nil {
		t.Fatalf("金额解析失败必须返回错误")
	}
}

// TestEasyPayQueryOrder 兼容顶层与 data 嵌套两种查单 JSON 结构。
func TestEasyPayQueryOrder(t *testing.T) {
	order := &model.Order{ID: uuid.New(), PriceMicros: 1_000_000}

	// 顶层结构：trade_status + 字符串金额。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析查单请求失败: %v", err)
		}
		if r.Form.Get("act") != "order" || r.Form.Get("pid") != "1001" || r.Form.Get("key") == "" ||
			r.Form.Get("out_trade_no") != order.ID.String() {
			t.Errorf("查单请求参数错误: %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"trade_status":"TRADE_SUCCESS","money":"1.00","trade_no":"T1","out_trade_no":"x"}`))
	}))
	defer srv.Close()
	result, err := newTestEasyPay(t, srv.URL).QueryOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("查单失败: %v", err)
	}
	if result.Status != "paid" || result.PriceMicros != 1_000_000 || result.ProviderOrderID != "T1" {
		t.Fatalf("顶层结构解析错误: %+v", result)
	}

	// data 嵌套结构：status 为数字、money 为数字。
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":1,"money":1.00,"trade_no":"T2","out_trade_no":"x"}}`))
	}))
	defer srv2.Close()
	result, err = newTestEasyPay(t, srv2.URL).QueryOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("查单失败: %v", err)
	}
	if result.Status != "paid" || result.PriceMicros != 1_000_000 || result.ProviderOrderID != "T2" {
		t.Fatalf("data 嵌套结构解析错误: %+v", result)
	}

	// 未支付与未知状态保持待支付，不误杀。
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	defer srv3.Close()
	result, err = newTestEasyPay(t, srv3.URL).QueryOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("查单失败: %v", err)
	}
	if result.Status != "pending" {
		t.Fatalf("未支付订单应保持待支付: %+v", result)
	}

	// 查单返回已支付但金额为 0：报错而不是静默按本地快照到账。
	srv4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"trade_status":"TRADE_SUCCESS","money":0}`))
	}))
	defer srv4.Close()
	if _, err := newTestEasyPay(t, srv4.URL).QueryOrder(context.Background(), order); err == nil {
		t.Fatalf("查单金额为 0 应报错")
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for name, value := range src {
		dst[name] = value
	}
	return dst
}
