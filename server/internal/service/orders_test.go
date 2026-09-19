package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/payment"
	"github.com/infinite-canvas/server/internal/platform/billing"
)

// fakeProvider 是支付渠道测试替身，避免单测依赖真实渠道 SDK 与网络。
type fakeProvider struct {
	createCalls  int
	callback     payment.CallbackResult
	queryResult  payment.CallbackResult
	queryErr     error
	createParams payment.Params
	createErr    error
	verifyErr    error
}

func (f *fakeProvider) Name() string { return "alipay" }

func (f *fakeProvider) CreatePayment(context.Context, *model.Order) (payment.Params, string, error) {
	f.createCalls++
	return f.createParams, "", f.createErr
}

func (f *fakeProvider) VerifyAndParse(context.Context, *http.Request) (payment.CallbackResult, error) {
	if f.verifyErr != nil {
		return payment.CallbackResult{}, f.verifyErr
	}
	return f.callback, nil
}

func (f *fakeProvider) QueryOrder(context.Context, *model.Order) (payment.CallbackResult, error) {
	if f.queryErr != nil {
		return payment.CallbackResult{}, f.queryErr
	}
	return f.queryResult, nil
}

func (f *fakeProvider) PaidAmountMicros(string) (int64, error) { return f.callback.PriceMicros, nil }

func newFakeRegistry(provider payment.Provider) *PaymentRegistry {
	return &PaymentRegistry{providers: map[string]payment.Provider{provider.Name(): provider}}
}

func newOrderServiceForTest(t *testing.T, g *gorm.DB, provider *fakeProvider) *OrderService {
	t.Helper()
	return NewOrderService(g, newFakeRegistry(provider))
}

func seedPaidOrder(t *testing.T, g *gorm.DB, order *model.Order) {
	t.Helper()
	if err := g.Create(order).Error; err != nil {
		t.Fatalf("写入订单失败: %v", err)
	}
}

func TestCreateOrderSnapshotsPackage(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{createParams: payment.Params{Type: payment.PaymentTypeRedirect, Payload: "https://example.com/pay"}}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	pack := model.CreditPackage{ID: "standard", Name: "标准包", PriceMicros: 30_000_000, BonusMicros: 3_000_000, Currency: "CNY", Enabled: true}
	if err := g.Create(&pack).Error; err != nil {
		t.Fatalf("写入档位失败: %v", err)
	}

	result, err := orders.CreateOrder(context.Background(), user, "standard", "", "alipay")
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if result.Order.PriceMicros != 30_000_000 || result.Order.PurchasedMicros != 30_000_000 || result.Order.GrantedMicros != 3_000_000 || result.Order.EntitlementDays != 0 {
		t.Fatalf("点数包订单快照错误（entitlementDays 恒 0）: %+v", result.Order)
	}
	// 快照不随档位修改而变化。
	if err := g.Model(&model.CreditPackage{}).Where("id = ?", "standard").Update("price_micros", 50_000_000).Error; err != nil {
		t.Fatalf("修改档位失败: %v", err)
	}
	var stored model.Order
	if err := g.First(&stored, "id = ?", result.Order.ID).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if stored.PriceMicros != 30_000_000 {
		t.Fatalf("订单快照不应被档位修改覆盖: %d", stored.PriceMicros)
	}
}

func TestCallbackIdempotentAndAmountMismatch(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{callback: payment.CallbackResult{Status: "paid", PriceMicros: 30_000_000, ProviderOrderID: "trade-1"}}
	orders := newOrderServiceForTest(t, g, provider)
	points := billing.NewService(g, model.ProductCanvas)
	user := createUserRow(t, g)
	if err := points.EnsureAccount(g, user.ID); err != nil {
		t.Fatalf("建账本行失败: %v", err)
	}
	order := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000,
		GrantedMicros: 3_000_000, EntitlementDays: 30, Status: "pending",
	}
	seedPaidOrder(t, g, order)
	provider.callback.OutTradeNo = order.ID.String()

	if err := orders.HandleCallback(context.Background(), "alipay", provider.callback); err != nil {
		t.Fatalf("首次回调失败: %v", err)
	}
	// 重复回调只到账一次。
	provider.callback.ProviderOrderID = "trade-2"
	if err := orders.HandleCallback(context.Background(), "alipay", provider.callback); err != nil {
		t.Fatalf("重复回调应返回成功: %v", err)
	}
	balance, _ := points.Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 30_000_000 || balance.GrantedMicros != 3_000_000 {
		t.Fatalf("重复回调导致重复到账: %+v", balance)
	}
	var paidCount int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, billing.TxTypePurchase).Count(&paidCount)
	if paidCount != 2 {
		t.Fatalf("到账应写两条流水（购买+赠送）, got %d", paidCount)
	}
	var stored model.Order
	g.First(&stored, "id = ?", order.ID)
	if stored.Status != "paid" || stored.PaidAt == nil {
		t.Fatalf("订单状态未跃迁: %+v", stored)
	}

	// 金额不一致的订单拒绝到账。
	other := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30, Status: "pending",
	}
	seedPaidOrder(t, g, other)
	mismatch := payment.CallbackResult{Status: "paid", OutTradeNo: other.ID.String(), PriceMicros: 1, ProviderOrderID: "trade-3"}
	if err := orders.HandleCallback(context.Background(), "alipay", mismatch); err == nil {
		t.Fatalf("金额不一致应拒绝到账")
	}
	var untouched model.Order
	g.First(&untouched, "id = ?", other.ID)
	if untouched.Status != "pending" {
		t.Fatalf("金额不一致不应改变订单状态: %+v", untouched)
	}
}

func TestCancelOrder(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	other := createUserRow(t, g)
	order := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30, Status: "pending",
	}
	seedPaidOrder(t, g, order)

	if _, err := orders.CancelOrder(context.Background(), other.ID, order.ID); err == nil {
		t.Fatalf("跨用户取消应失败")
	}
	if _, err := orders.CancelOrder(context.Background(), user.ID, order.ID); err != nil {
		t.Fatalf("取消待支付订单失败: %v", err)
	}
	if _, err := orders.CancelOrder(context.Background(), user.ID, order.ID); err != nil {
		t.Fatalf("重复取消应幂等: %v", err)
	}
	// 已支付订单不能取消。
	paid := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30, Status: "paid",
	}
	seedPaidOrder(t, g, paid)
	if _, err := orders.CancelOrder(context.Background(), user.ID, paid.ID); err != ErrOrderAlreadyPaid {
		t.Fatalf("取消已支付订单应返回 ErrOrderAlreadyPaid, got %v", err)
	}
}

func TestExpirePendingOrdersQueriesProviderFirst(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{queryResult: payment.CallbackResult{Status: "paid", PriceMicros: 10_000_000, ProviderOrderID: "trade-9"}}
	orders := newOrderServiceForTest(t, g, provider)
	points := billing.NewService(g, model.ProductCanvas)
	user := createUserRow(t, g)
	if err := points.EnsureAccount(g, user.ID); err != nil {
		t.Fatalf("建账本行失败: %v", err)
	}
	order := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30,
		Status: "pending", CreatedAt: time.Now().Add(-time.Hour),
	}
	seedPaidOrder(t, g, order)

	// 渠道侧已支付时，超时扫描应补到账而不是置失败。
	if _, err := orders.ExpirePendingOrders(context.Background(), time.Now(), 30*time.Minute); err != nil {
		t.Fatalf("超时扫描失败: %v", err)
	}
	var stored model.Order
	g.First(&stored, "id = ?", order.ID)
	if stored.Status != "paid" {
		t.Fatalf("渠道已支付的超时订单应补到账, got %s", stored.Status)
	}
}

func TestPackagePriceMustBeWholeCents(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	pack := model.CreditPackage{ID: "odd", Name: "奇异价", PriceMicros: 12_345, Currency: "CNY", Enabled: true}
	if err := g.Create(&pack).Error; err != nil {
		t.Fatalf("写入档位失败: %v", err)
	}
	if _, err := orders.CreateOrder(context.Background(), user, "odd", "", "alipay"); err == nil {
		t.Fatalf("非整分价格应被拒绝")
	}
}

// 渠道确认未支付（pending/未知状态）的超时订单应置 failed 关单并计入 expired。
func TestExpirePendingOrdersClosesUnpaid(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{queryResult: payment.CallbackResult{Status: "pending"}}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	timeout := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30,
		Status: "pending", CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	unknown := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30,
		Status: "pending", CreatedAt: time.Now().Add(-time.Hour),
	}
	seedPaidOrder(t, g, timeout)
	seedPaidOrder(t, g, unknown)

	expired, err := orders.ExpirePendingOrders(context.Background(), time.Now(), 30*time.Minute)
	if err != nil {
		t.Fatalf("超时扫描失败: %v", err)
	}
	if expired != 2 {
		t.Fatalf("两笔渠道未支付的超时订单都应关单, got %d", expired)
	}
	for _, id := range []uuid.UUID{timeout.ID, unknown.ID} {
		var stored model.Order
		g.First(&stored, "id = ?", id)
		if stored.Status != "failed" {
			t.Fatalf("渠道未支付的超时订单应置失败, order=%s status=%s", id, stored.Status)
		}
	}
}

// 渠道查单报错：订单保留 pending，等下一轮扫描重试。
func TestExpirePendingOrdersQueryErrorKeepsPending(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{queryErr: errors.New("渠道查询超时")}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	order := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 10_000_000, Currency: "CNY", PurchasedMicros: 10_000_000, EntitlementDays: 30,
		Status: "pending", CreatedAt: time.Now().Add(-time.Hour),
	}
	seedPaidOrder(t, g, order)

	expired, err := orders.ExpirePendingOrders(context.Background(), time.Now(), 30*time.Minute)
	if err != nil {
		t.Fatalf("超时扫描失败: %v", err)
	}
	if expired != 0 {
		t.Fatalf("渠道查询报错不应关单, got %d", expired)
	}
	var stored model.Order
	g.First(&stored, "id = ?", order.ID)
	if stored.Status != "pending" {
		t.Fatalf("渠道查询报错时订单应保持待支付, got %s", stored.Status)
	}
}

// 渠道报 paid 但订单已终态（failed/refunded）：返回成功、状态不变，触发一次丢钱告警回调；
// 回调为 nil 时不 panic。
func TestMarkPaidStaleTriggersAlertCallback(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{callback: payment.CallbackResult{Status: "paid", PriceMicros: 30_000_000, ProviderOrderID: "trade-late"}}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	order := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000,
		GrantedMicros: 3_000_000, EntitlementDays: 30, Status: "failed",
	}
	seedPaidOrder(t, g, order)
	provider.callback.OutTradeNo = order.ID.String()

	var calls int
	var gotOrder *model.Order
	var gotProviderOrderID string
	orders.OnPaidOrderClosed = func(o *model.Order, providerOrderID string) {
		calls++
		gotOrder = o
		gotProviderOrderID = providerOrderID
	}
	if err := orders.HandleCallback(context.Background(), "alipay", provider.callback); err != nil {
		t.Fatalf("已关闭订单收到支付回调应返回成功: %v", err)
	}
	if calls != 1 || gotOrder == nil || gotOrder.ID != order.ID || gotOrder.Status != "failed" || gotProviderOrderID != "trade-late" {
		t.Fatalf("stale 到账应触发一次告警回调且参数正确: calls=%d order=%+v providerOrderID=%q", calls, gotOrder, gotProviderOrderID)
	}
	var stored model.Order
	g.First(&stored, "id = ?", order.ID)
	if stored.Status != "failed" {
		t.Fatalf("已失败订单不应被到账: %s", stored.Status)
	}

	// 回调为 nil 时不 panic。
	orders.OnPaidOrderClosed = nil
	other := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000,
		EntitlementDays: 30, Status: "refunded",
	}
	seedPaidOrder(t, g, other)
	provider.callback.OutTradeNo = other.ID.String()
	if err := orders.HandleCallback(context.Background(), "alipay", provider.callback); err != nil {
		t.Fatalf("refunded 订单收到支付回调应返回成功: %v", err)
	}
	var untouched model.Order
	g.First(&untouched, "id = ?", other.ID)
	if untouched.Status != "refunded" {
		t.Fatalf("refunded 订单不应被到账: %s", untouched.Status)
	}
}

// paid→paid 重复回调幂等：不重复到账，也不触发丢钱告警回调（只有 stale 分支触发）。
func TestMarkPaidIdempotentDoesNotTriggerCallback(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{callback: payment.CallbackResult{Status: "paid", PriceMicros: 30_000_000, ProviderOrderID: "trade-again"}}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	order := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000,
		GrantedMicros: 3_000_000, EntitlementDays: 30, Status: "paid",
	}
	seedPaidOrder(t, g, order)
	provider.callback.OutTradeNo = order.ID.String()

	var calls int
	orders.OnPaidOrderClosed = func(*model.Order, string) { calls++ }
	if err := orders.HandleCallback(context.Background(), "alipay", provider.callback); err != nil {
		t.Fatalf("已支付订单重复回调应返回成功: %v", err)
	}
	if calls != 0 {
		t.Fatalf("幂等到账不应触发告警回调, got %d", calls)
	}
	var stored model.Order
	g.First(&stored, "id = ?", order.ID)
	if stored.Status != "paid" {
		t.Fatalf("订单应保持已支付: %s", stored.Status)
	}
}

// 下单成功后自动关闭同用户旧待支付单（防旧支付链接被误付），其它用户与新单不受影响。
func TestCreateOrderCancelsOtherPendingOrders(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{createParams: payment.Params{Type: payment.PaymentTypeRedirect, Payload: "https://example.com/pay"}}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	pack := model.CreditPackage{ID: "standard", Name: "标准包", PriceMicros: 30_000_000, BonusMicros: 3_000_000, Currency: "CNY", Enabled: true}
	if err := g.Create(&pack).Error; err != nil {
		t.Fatalf("写入档位失败: %v", err)
	}
	old := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000, Status: "pending",
	}
	otherUser := createUserRow(t, g)
	otherOrder := &model.Order{
		ID: uuid.New(), UserID: otherUser.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000, Status: "pending",
	}
	seedPaidOrder(t, g, old)
	seedPaidOrder(t, g, otherOrder)

	result, err := orders.CreateOrder(context.Background(), user, "standard", "", "alipay")
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	var stale model.Order
	g.First(&stale, "id = ?", old.ID)
	if stale.Status != "failed" {
		t.Fatalf("同用户旧待支付单应被关闭: %s", stale.Status)
	}
	var kept model.Order
	g.First(&kept, "id = ?", otherOrder.ID)
	if kept.Status != "pending" {
		t.Fatalf("其它用户的待支付单不应被取消: %s", kept.Status)
	}
	var fresh model.Order
	g.First(&fresh, "id = ?", result.Order.ID)
	if fresh.Status != "pending" {
		t.Fatalf("新单应保持待支付: %s", fresh.Status)
	}
}

// 渠道下单失败：新单置 failed，同用户旧待支付单保留（不取消）。
func TestCreateOrderPaymentFailureKeepsOldPending(t *testing.T) {
	g := newServiceDB(t)
	provider := &fakeProvider{createErr: errors.New("渠道下单失败")}
	orders := newOrderServiceForTest(t, g, provider)
	user := createUserRow(t, g)
	pack := model.CreditPackage{ID: "standard", Name: "标准包", PriceMicros: 30_000_000, BonusMicros: 3_000_000, Currency: "CNY", Enabled: true}
	if err := g.Create(&pack).Error; err != nil {
		t.Fatalf("写入档位失败: %v", err)
	}
	old := &model.Order{
		ID: uuid.New(), UserID: user.ID, Provider: "alipay", PackageID: "standard",
		PriceMicros: 30_000_000, Currency: "CNY", PurchasedMicros: 30_000_000, Status: "pending",
	}
	seedPaidOrder(t, g, old)

	if _, err := orders.CreateOrder(context.Background(), user, "standard", "", "alipay"); err == nil {
		t.Fatalf("渠道下单失败应返回错误")
	}
	var stale model.Order
	g.First(&stale, "id = ?", old.ID)
	if stale.Status != "pending" {
		t.Fatalf("渠道下单失败不应取消旧待支付单: %s", stale.Status)
	}
	var failedCount int64
	g.Model(&model.Order{}).Where("user_id = ? AND status = ?", user.ID, "failed").Count(&failedCount)
	if failedCount != 1 {
		t.Fatalf("失败的新单应置 failed, got %d", failedCount)
	}
}
