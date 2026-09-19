package admin

import (
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
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/testutil"
)

// newAdminBillingRouter 组装管理面用户/点数/档位/折扣路由的测试引擎。
// 与 billing 包的用户侧夹具各自独立，避免测试包互相依赖。
func newAdminBillingRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	adminH := NewAdminHandler(g, cfg, testutil.NewFakeStorage("local"))

	admin := r.Group("/api/admin", middleware.Auth(secret), middleware.LoadAdminAccess(identity.NewService(g), g))
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

func TestAdminRoutesRejectNonAdmin(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminBillingRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "user@example.com", "normaluser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	for _, path := range []string{"/api/admin/users", "/api/admin/stats", "/api/admin/orders", "/api/admin/credit-packages"} {
		w := testutil.DoAuthJSON(r, http.MethodGet, path, token, nil)
		if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "FORBIDDEN" {
			t.Fatalf("非管理员访问 %s 应 403 FORBIDDEN, got %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminUserListAndCreditAdjust(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminBillingRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "admin@example.com", "adminuser", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)
	target := testutil.CreateUser(t, g, "target@example.com", "targetuser", "password123", true)

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users?q=target", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("用户列表失败: code=%d body=%s", w.Code, w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	if len(items) != 1 || items[0]["email"] != "target@example.com" {
		t.Fatalf("用户筛选结果错误: %v", items)
	}
	if items[0]["planId"] != "free" {
		t.Fatalf("默认档位应为 free: %v", items[0]["planId"])
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", adminToken, map[string]any{
		"bucket": "granted", "amountMicros": 500_000, "note": "客服补偿",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("调整点数失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["grantedMicros"] != float64(500_000) || body["purchasedMicros"] != float64(0) {
		t.Fatalf("调整后余额错误: %v", body)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users/"+target.ID.String(), adminToken, nil)
	detail := testutil.DecodeBody(t, w)["user"].(map[string]any)
	if detail["planId"] != "free" {
		t.Fatalf("仅赠送点数不应产生付费身份: %v", detail["planId"])
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", adminToken, map[string]any{
		"bucket": "granted", "amountMicros": -600_000, "note": "误操作",
	})
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("扣成负数应 402, got %d %s", w.Code, w.Body.String())
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", adminToken, map[string]any{
		"bucket": "granted", "amountMicros": 100,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少说明应 400, got %d", w.Code)
	}
}

func TestAdminPackageValidation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminBillingRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "admin2@example.com", "adminuser2", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	token := testutil.AccessToken(t, cfg, &admin)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/credit-packages", token, map[string]any{
		"id": "weird", "name": "三厘五", "priceMicros": 12_345,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非整分价格应 400, got %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/credit-packages", token, map[string]any{
		"id": "ok", "name": "整数包", "priceMicros": 10_000_000, "bonusMicros": 1_000_000, "entitlementDays": 30,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("合法档位应创建成功: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminPromotionConflictAndAudit(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminBillingRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "admin3@example.com", "adminuser3", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	token := testutil.AccessToken(t, cfg, &admin)

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
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/model-promotions", token, payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建折扣失败: %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/model-promotions", token, payload)
	if w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "PROMOTION_CONFLICT" {
		t.Fatalf("冲突折扣应 409 PROMOTION_CONFLICT, got %d %s", w.Code, w.Body.String())
	}
	bad := map[string]any{
		"modelId": modelID.String(), "name": "超低折扣", "matchParams": map[string]string{"quality": "low"},
		"discountBps": 12000, "startsAt": now.Add(-time.Hour).Format(time.RFC3339), "endsAt": now.Add(time.Hour).Format(time.RFC3339),
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/model-promotions", token, bad)
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
