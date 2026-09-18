package service

import (
	"context"
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
