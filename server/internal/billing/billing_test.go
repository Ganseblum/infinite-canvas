package billing

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/account"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/payment"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/testutil"
)

// fakeProvider 是满足 payment.Provider 的测试替身，避免单测依赖真实渠道 SDK。
type fakeProvider struct {
	createParams payment.Params
	callback     payment.CallbackResult
	queryResult  payment.CallbackResult
}

func (f *fakeProvider) Name() string { return "alipay" }

func (f *fakeProvider) CreatePayment(context.Context, *model.Order) (payment.Params, string, error) {
	return f.createParams, "", nil
}

func (f *fakeProvider) VerifyAndParse(context.Context, *http.Request) (payment.CallbackResult, error) {
	return f.callback, nil
}

func (f *fakeProvider) QueryOrder(context.Context, *model.Order) (payment.CallbackResult, error) {
	return f.queryResult, nil
}

func (f *fakeProvider) PaidAmountMicros(string) (int64, error) { return f.callback.PriceMicros, nil }

func newBillingRouter(t *testing.T, g *gorm.DB, cfg *config.Config, provider payment.Provider) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	registry := service.NewPaymentRegistryForTest([]payment.Provider{provider})
	creditH := NewCreditHandler(g, registry)
	orderSvc := service.NewOrderService(g, registry)
	orderH := NewOrderHandler(orderSvc)
	paymentH := NewPaymentHandler(orderSvc)
	accountH := account.NewAccountHandler(g, cfg, service.NewFreeGrantService(g), account.NewAuthHandler(g, cfg, testutil.TestMailer()))
	api := r.Group("/api/v1")
	active := middleware.RequireActiveUser(identity.NewService(g))
	me := api.Group("/me", middleware.Auth(secret), active)
	me.GET("", accountH.GetMe)
	me.POST("/deletion", accountH.RequestDeletion)
	me.POST("/deletion/cancel", accountH.CancelDeletion)

	billing := api.Group("", middleware.Auth(secret), active)
	billing.GET("/credits", creditH.GetBalance)
	billing.GET("/credits/transactions", creditH.ListTransactions)
	billing.GET("/credit-packages", creditH.ListPackages)
	billing.GET("/plans", creditH.ListPlans)

	orders := api.Group("/orders", middleware.Auth(secret), active)
	orders.POST("", middleware.RequireNotPendingDeletion(), orderH.Create)
	orders.GET("", orderH.List)
	orders.GET("/:id", orderH.Get)
	orders.POST("/:id/cancel", orderH.Cancel)

	api.POST("/payments/webhook/:provider", paymentH.Webhook)

	return r
}

func TestCreditsAndPackagesRequireAuth(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})

	if w := testutil.DoJSON(r, http.MethodGet, "/api/v1/credits", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问余额应 401, got %d", w.Code)
	}
	user := testutil.CreateUser(t, g, "credits@example.com", "creditsuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	if err := g.Create(&model.CreditPackage{ID: "standard", Name: "标准包", PriceMicros: 30_000_000, BonusMicros: 3_000_000, Currency: "CNY", Enabled: true, Sort: 1}).Error; err != nil {
		t.Fatalf("写入档位失败: %v", err)
	}
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/credits", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取余额失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["purchasedMicros"] != float64(0) || body["totalMicros"] != float64(0) {
		t.Fatalf("新用户余额应为 0: %v", body)
	}

	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/credit-packages", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取充值档位失败: code=%d body=%s", w.Code, w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	if len(items) != 1 || items[0]["id"] != "standard" {
		t.Fatalf("充值档位列表错误: %v", items)
	}
}

func TestMeIncludesCreditsPlanAndDeletion(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	user := testutil.CreateUser(t, g, "me2@example.com", "meuser2", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/me", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取 /api/me 失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	plan, ok := body["plan"].(map[string]any)
	if !ok || plan["id"] != "free" {
		t.Fatalf("默认档位错误: %v", body["plan"])
	}
	for _, key := range []string{"credits", "usage", "mediaExpiry", "deletion", "readOnly"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("/api/v1/me 缺少 %s 字段: %s", key, w.Body.String())
		}
	}
	deletion, ok := body["deletion"].(map[string]any)
	if !ok || deletion["status"] != "none" {
		t.Fatalf("deletion 初始状态错误: %v", body["deletion"])
	}
}

func TestAccountDeletionLifecycle(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	user := testutil.CreateUser(t, g, "delete@example.com", "deleteuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion", token, map[string]string{"password": "wrong-password"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("密码错误应 401, got %d", w.Code)
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion", token, map[string]string{"password": "password123"})
	if w.Code != http.StatusOK {
		t.Fatalf("申请注销失败: code=%d body=%s", w.Code, w.Body.String())
	}
	first := testutil.DecodeBody(t, w)["scheduledAt"]

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion", token, map[string]string{"password": "password123"})
	if got := testutil.DecodeBody(t, w)["scheduledAt"]; got != first {
		t.Fatalf("重复申请不应重置倒计时: %v vs %v", got, first)
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/orders", token, map[string]string{"packageId": "standard", "provider": "alipay"})
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "ACCOUNT_PENDING_DELETION" {
		t.Fatalf("冷静期内下单应 403 ACCOUNT_PENDING_DELETION, got %d %s", w.Code, w.Body.String())
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion/cancel", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("撤销注销失败: code=%d body=%s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion/cancel", token, nil)
	if w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "DELETION_NOT_PENDING" {
		t.Fatalf("重复撤销应 409 DELETION_NOT_PENDING, got %d %s", w.Code, w.Body.String())
	}
}

// TestMembershipOrderWebhookGrantsCreditsAndSubscription 会员订单支付回调端到端：
// 渠道回调到账后同一事务完成「双桶加点 + 发会员」，订阅、流水与存储配额一次到位，重复回调只到账一次。
func TestMembershipOrderWebhookGrantsCreditsAndSubscription(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	provider := &fakeProvider{}
	r := newBillingRouter(t, g, cfg, provider)
	user := testutil.CreateUser(t, g, "member@example.com", "memberuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	// 下单购买会员档位 paid（种子价 30 元、30 天）
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/orders", token, map[string]string{"planId": "paid", "provider": "alipay"})
	if w.Code != http.StatusCreated {
		t.Fatalf("创建会员订单失败: code=%d body=%s", w.Code, w.Body.String())
	}
	orderBody, _ := testutil.DecodeBody(t, w)["order"].(map[string]any)
	orderID, _ := orderBody["id"].(string)
	if orderID == "" {
		t.Fatalf("下单响应缺少订单 id: %s", w.Body.String())
	}

	// 渠道回调到账，实付金额与订单快照一致
	provider.callback = payment.CallbackResult{OutTradeNo: orderID, ProviderOrderID: "ch-member-1", PriceMicros: 30_000_000, Status: "paid"}
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/payments/webhook/alipay", map[string]string{"out_trade_no": orderID})
	if w.Code != http.StatusOK || w.Body.String() != "success" {
		t.Fatalf("支付回调应 200 success: code=%d body=%s", w.Code, w.Body.String())
	}

	var order model.Order
	if err := g.First(&order, "id = ?", orderID).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if order.Status != "paid" || order.PaidAt == nil {
		t.Fatalf("订单应已到账: status=%s paidAt=%v", order.Status, order.PaidAt)
	}

	// 会员订单不加点（PurchasedMicros 只有点数包订单才设置）：无 purchase 流水、余额保持 0。
	var txCount int64
	if err := g.Model(&model.CreditTransaction{}).Where("user_id = ?", user.ID).Count(&txCount).Error; err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if txCount != 0 {
		t.Fatalf("会员订单不应产生点数流水, got %d", txCount)
	}
	var credits model.CreditAccount
	if err := g.First(&credits, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取点数账户失败: %v", err)
	}
	if credits.PurchasedMicros != 0 || credits.GrantedMicros != 0 {
		t.Fatalf("会员订单不应改变点数余额: %+v", credits)
	}

	var sub model.MembershipSubscription
	if err := g.First(&sub, "user_id = ? and plan_id = ? and status = ?", user.ID, "paid", "active").Error; err != nil {
		t.Fatalf("应生成 paid 生效订阅: %v", err)
	}
	if sub.SourceRef != orderID {
		t.Fatalf("订阅来源应为订单 id, got %s", sub.SourceRef)
	}

	var storage model.StorageAccount
	if err := g.First(&storage, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取存储账户失败: %v", err)
	}
	if storage.QuotaBytes != int64(1024*1024*1024) {
		t.Fatalf("付费档位配额应回写 1GB, got %d", storage.QuotaBytes)
	}

	// 重复回调幂等：不重复加点、不重复发会员
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/payments/webhook/alipay", map[string]string{"out_trade_no": orderID})
	if w.Code != http.StatusOK {
		t.Fatalf("重复回调应仍返回渠道成功响应: %d", w.Code)
	}
	var subCount int64
	if err := g.Model(&model.MembershipSubscription{}).Where("user_id = ? and source_ref = ?", user.ID, orderID).Count(&subCount).Error; err != nil {
		t.Fatalf("查询订阅失败: %v", err)
	}
	if subCount != 1 {
		t.Fatalf("重复回调不应重复发会员: subs=%d", subCount)
	}
}

// TestPendingDeletionCanLoginRefreshAndCancel 冷静期全链路：
// 申请注销后登录与刷新不再被 403 拦截（撤销入口必须可达），撤销后账号恢复 active。
func TestPendingDeletionCanLoginRefreshAndCancel(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := account.NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)
	// 同一引擎上补挂注销申请/撤销，中间件与 main.go 的 me 分组一致
	accountH := account.NewAccountHandler(g, cfg, service.NewFreeGrantService(g), h)
	me := r.Group("/api/v1/me", middleware.Auth([]byte(cfg.JWTSecret)), middleware.RequireActiveUser(identity.NewService(g)))
	me.POST("/deletion", accountH.RequestDeletion)
	me.POST("/deletion/cancel", accountH.CancelDeletion)

	user := testutil.CreateUser(t, g, "cooling@example.com", "coolinguser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion", token, map[string]string{"password": "password123"})
	if w.Code != http.StatusOK {
		t.Fatalf("申请注销失败: code=%d body=%s", w.Code, w.Body.String())
	}

	// 冷静期内登录仍成功（旧实现一律 403 ACCOUNT_DISABLED，撤销入口不可达）
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "cooling@example.com", "password": "password123"})
	if w.Code != http.StatusOK {
		t.Fatalf("冷静期登录应成功: code=%d body=%s", w.Code, w.Body.String())
	}
	cookie := testutil.FindCookie(w, account.RefreshCookieName)
	if cookie == nil {
		t.Fatal("冷静期登录未下发 refresh cookie")
	}

	// 冷静期内刷新仍成功
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("冷静期刷新应成功: code=%d body=%s", w.Code, w.Body.String())
	}

	// 撤销注销，账号恢复 active
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/deletion/cancel", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("撤销注销失败: code=%d body=%s", w.Code, w.Body.String())
	}
	var fresh model.PlatformUser
	if err := g.First(&fresh, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if fresh.Status != "active" {
		t.Fatalf("撤销后账号应恢复 active, got %s", fresh.Status)
	}

	// 封禁账号仍拒绝登录
	banned := testutil.CreateUser(t, g, "banned@example.com", "banneduser", "password123", true)
	if err := g.Model(&model.PlatformUser{}).Where("id = ?", banned.ID).Update("status", "disabled").Error; err != nil {
		t.Fatalf("置为封禁失败: %v", err)
	}
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "banned@example.com", "password": "password123"})
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "ACCOUNT_DISABLED" {
		t.Fatalf("封禁账号登录应 403 ACCOUNT_DISABLED, got %d %s", w.Code, w.Body.String())
	}
}
