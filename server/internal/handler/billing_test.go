package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/payment"
	"github.com/infinite-canvas/server/internal/service"
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
	orderH := NewOrderHandler(g, registry)
	paymentH := NewPaymentHandler(g, registry)
	accountH := NewAccountHandler(g, cfg, service.NewFreeGrantService(g), NewAuthHandler(g, cfg, testMailer()))
	adminH := NewAdminHandler(g, cfg, newFakeStorage("local"))

	api := r.Group("/api")
	active := middleware.RequireActiveUser(g)
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

	admin := api.Group("/admin", middleware.Auth(secret), middleware.LoadAdminAccess(g))
	admin.GET("/stats", middleware.RequirePermission(authz.PermStatsRead), adminH.Stats)
	admin.GET("/users", middleware.RequirePermission(authz.PermUsersRead), adminH.ListUsers)
	admin.GET("/users/:id", middleware.RequirePermission(authz.PermUsersRead), adminH.GetUser)
	admin.PATCH("/users/:id", middleware.RequirePermission(authz.PermUsersWrite), adminH.PatchUser)
	admin.POST("/users/:id/credits", middleware.RequirePermission(authz.PermUsersCredits), adminH.AdjustCredits)
	admin.POST("/users/:id/usage/recalculate", middleware.RequirePermission(authz.PermUsersWrite), adminH.RecalculateUsage)
	admin.GET("/credit-packages", middleware.RequirePermission(authz.PermPackagesRead), adminH.ListPackages)
	admin.POST("/credit-packages", middleware.RequirePermission(authz.PermPackagesWrite), adminH.CreatePackage)
	admin.GET("/orders", middleware.RequirePermission(authz.PermOrdersRead), adminH.ListOrders)
	admin.GET("/model-promotions", middleware.RequirePermission(authz.PermModelsRead), adminH.ListPromotions)
	admin.POST("/model-promotions", middleware.RequirePermission(authz.PermModelsWrite), adminH.CreatePromotion)
	admin.PATCH("/model-promotions/:id", middleware.RequirePermission(authz.PermModelsWrite), adminH.UpdatePromotion)
	return r
}

func TestCreditsAndPackagesRequireAuth(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})

	if w := doJSON(r, http.MethodGet, "/api/credits", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问余额应 401, got %d", w.Code)
	}
	user := createUser(t, g, "credits@example.com", "creditsuser", "password123", true)
	token := accessToken(t, cfg, &user)

	if err := g.Create(&model.CreditPackage{ID: "standard", Name: "标准包", PriceMicros: 30_000_000, BonusMicros: 3_000_000, EntitlementDays: 30, Currency: "CNY", Enabled: true, Sort: 1}).Error; err != nil {
		t.Fatalf("写入档位失败: %v", err)
	}
	w := doAuthJSON(r, http.MethodGet, "/api/credits", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取余额失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["purchasedMicros"] != float64(0) || body["totalMicros"] != float64(0) {
		t.Fatalf("新用户余额应为 0: %v", body)
	}

	w = doAuthJSON(r, http.MethodGet, "/api/credit-packages", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取充值档位失败: code=%d body=%s", w.Code, w.Body.String())
	}
	items := decodeItems(t, w)
	if len(items) != 1 || items[0]["id"] != "standard" {
		t.Fatalf("充值档位列表错误: %v", items)
	}
}

func TestMeIncludesCreditsPlanAndDeletion(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	user := createUser(t, g, "me2@example.com", "meuser2", "password123", true)
	token := accessToken(t, cfg, &user)

	w := doAuthJSON(r, http.MethodGet, "/api/me", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取 /api/me 失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	plan, ok := body["plan"].(map[string]any)
	if !ok || plan["id"] != "free" {
		t.Fatalf("默认档位错误: %v", body["plan"])
	}
	for _, key := range []string{"credits", "usage", "mediaExpiry", "deletion", "readOnly"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("/api/me 缺少 %s 字段: %s", key, w.Body.String())
		}
	}
	deletion, ok := body["deletion"].(map[string]any)
	if !ok || deletion["status"] != "none" {
		t.Fatalf("deletion 初始状态错误: %v", body["deletion"])
	}
}

func TestAccountDeletionLifecycle(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	user := createUser(t, g, "delete@example.com", "deleteuser", "password123", true)
	token := accessToken(t, cfg, &user)

	w := doAuthJSON(r, http.MethodPost, "/api/me/deletion", token, map[string]string{"password": "wrong-password"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("密码错误应 401, got %d", w.Code)
	}

	w = doAuthJSON(r, http.MethodPost, "/api/me/deletion", token, map[string]string{"password": "password123"})
	if w.Code != http.StatusOK {
		t.Fatalf("申请注销失败: code=%d body=%s", w.Code, w.Body.String())
	}
	first := decodeBody(t, w)["scheduledAt"]

	w = doAuthJSON(r, http.MethodPost, "/api/me/deletion", token, map[string]string{"password": "password123"})
	if got := decodeBody(t, w)["scheduledAt"]; got != first {
		t.Fatalf("重复申请不应重置倒计时: %v vs %v", got, first)
	}

	w = doAuthJSON(r, http.MethodPost, "/api/orders", token, map[string]string{"packageId": "standard", "provider": "alipay"})
	if w.Code != http.StatusForbidden || errorCode(t, w) != "ACCOUNT_PENDING_DELETION" {
		t.Fatalf("冷静期内下单应 403 ACCOUNT_PENDING_DELETION, got %d %s", w.Code, w.Body.String())
	}

	w = doAuthJSON(r, http.MethodPost, "/api/me/deletion/cancel", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("撤销注销失败: code=%d body=%s", w.Code, w.Body.String())
	}
	w = doAuthJSON(r, http.MethodPost, "/api/me/deletion/cancel", token, nil)
	if w.Code != http.StatusConflict || errorCode(t, w) != "DELETION_NOT_PENDING" {
		t.Fatalf("重复撤销应 409 DELETION_NOT_PENDING, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminRoutesRejectNonAdmin(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	user := createUser(t, g, "user@example.com", "normaluser", "password123", true)
	token := accessToken(t, cfg, &user)

	for _, path := range []string{"/api/admin/users", "/api/admin/stats", "/api/admin/orders", "/api/admin/credit-packages"} {
		w := doAuthJSON(r, http.MethodGet, path, token, nil)
		if w.Code != http.StatusForbidden || errorCode(t, w) != "FORBIDDEN" {
			t.Fatalf("非管理员访问 %s 应 403 FORBIDDEN, got %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminUserListAndCreditAdjust(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	admin := createUser(t, g, "admin@example.com", "adminuser", "password123", true)
	promoteAdmin(t, g, &admin)
	adminToken := accessToken(t, cfg, &admin)
	target := createUser(t, g, "target@example.com", "targetuser", "password123", true)

	w := doAuthJSON(r, http.MethodGet, "/api/admin/users?q=target", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("用户列表失败: code=%d body=%s", w.Code, w.Body.String())
	}
	items := decodeItems(t, w)
	if len(items) != 1 || items[0]["email"] != "target@example.com" {
		t.Fatalf("用户筛选结果错误: %v", items)
	}
	if items[0]["planId"] != "free" {
		t.Fatalf("默认档位应为 free: %v", items[0]["planId"])
	}

	w = doAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", adminToken, map[string]any{
		"bucket": "granted", "amountMicros": 500_000, "note": "客服补偿",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("调整点数失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["grantedMicros"] != float64(500_000) || body["purchasedMicros"] != float64(0) {
		t.Fatalf("调整后余额错误: %v", body)
	}
	w = doAuthJSON(r, http.MethodGet, "/api/admin/users/"+target.ID.String(), adminToken, nil)
	detail := decodeBody(t, w)["user"].(map[string]any)
	if detail["planId"] != "free" {
		t.Fatalf("仅赠送点数不应产生付费身份: %v", detail["planId"])
	}

	w = doAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", adminToken, map[string]any{
		"bucket": "granted", "amountMicros": -600_000, "note": "误操作",
	})
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("扣成负数应 402, got %d %s", w.Code, w.Body.String())
	}

	w = doAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", adminToken, map[string]any{
		"bucket": "granted", "amountMicros": 100,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少说明应 400, got %d", w.Code)
	}
}

func TestAdminPackageValidation(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	admin := createUser(t, g, "admin2@example.com", "adminuser2", "password123", true)
	promoteAdmin(t, g, &admin)
	token := accessToken(t, cfg, &admin)

	w := doAuthJSON(r, http.MethodPost, "/api/admin/credit-packages", token, map[string]any{
		"id": "weird", "name": "三厘五", "priceMicros": 12_345,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非整分价格应 400, got %d %s", w.Code, w.Body.String())
	}
	w = doAuthJSON(r, http.MethodPost, "/api/admin/credit-packages", token, map[string]any{
		"id": "ok", "name": "整数包", "priceMicros": 10_000_000, "bonusMicros": 1_000_000, "entitlementDays": 30,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("合法档位应创建成功: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminPromotionConflictAndAudit(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newBillingRouter(t, g, cfg, &fakeProvider{})
	admin := createUser(t, g, "admin3@example.com", "adminuser3", "password123", true)
	promoteAdmin(t, g, &admin)
	token := accessToken(t, cfg, &admin)

	modelID := uuid.New()
	catalogItem := model.ModelCatalog{
		ID: modelID, Name: "test-image", DisplayName: "测试图", Capability: "image", Provider: "openai",
		Constraints: []byte(`{"size":["1024x1024"],"quality":["low","high"]}`),
		CreditCost:  []byte(`{"version":1,"dimensions":["size","quality"],"prices":[{"params":{"size":"1024x1024","quality":"low"},"costMicros":100},{"params":{"size":"1024x1024","quality":"high"},"costMicros":200}]}`),
		Enabled:     true,
	}
	if err := g.Create(&catalogItem).Error; err != nil {
		t.Fatalf("写入模型失败: %v", err)
	}
	now := time.Now()
	payload := map[string]any{
		"modelId": modelID.String(), "name": "整模型 9 折", "matchParams": map[string]string{},
		"discountBps": 9000, "startsAt": now.Add(-time.Hour).Format(time.RFC3339), "endsAt": now.Add(time.Hour).Format(time.RFC3339),
	}
	w := doAuthJSON(r, http.MethodPost, "/api/admin/model-promotions", token, payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建折扣失败: %d %s", w.Code, w.Body.String())
	}
	w = doAuthJSON(r, http.MethodPost, "/api/admin/model-promotions", token, payload)
	if w.Code != http.StatusConflict || errorCode(t, w) != "PROMOTION_CONFLICT" {
		t.Fatalf("冲突折扣应 409 PROMOTION_CONFLICT, got %d %s", w.Code, w.Body.String())
	}
	bad := map[string]any{
		"modelId": modelID.String(), "name": "超低折扣", "matchParams": map[string]string{"quality": "low"},
		"discountBps": 12000, "startsAt": now.Add(-time.Hour).Format(time.RFC3339), "endsAt": now.Add(time.Hour).Format(time.RFC3339),
	}
	w = doAuthJSON(r, http.MethodPost, "/api/admin/model-promotions", token, bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法折扣应 400, got %d %s", w.Code, w.Body.String())
	}
	var audits int64
	if err := g.Model(&model.AdminAuditLog{}).Where("action = ?", "promotion.create").Count(&audits).Error; err != nil {
		t.Fatalf("统计审计记录失败: %v", err)
	}
	if audits != 1 {
		t.Fatalf("应写入一条审计记录, got %d", audits)
	}
}
