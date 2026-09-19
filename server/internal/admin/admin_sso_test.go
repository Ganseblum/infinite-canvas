package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
)

// decodeSSOBody 解析 JSON 响应体到目标结构。
func decodeSSOBody(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, w.Body.String())
	}
}

// newAdminSSORouter 按生产同样的中间件链注册 SSO 客户端五条路由。
func newAdminSSORouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	adminH := NewAdminHandler(g, cfg, newFakeStorage("local"))
	admin := r.Group("/api/admin",
		middleware.Auth(secret),
		middleware.RequireActiveUser(identity.NewService(g)),
		middleware.LoadAdminAccess(identity.NewService(g), g),
	)
	admin.GET("/sso/clients", middleware.RequirePermission(authz.PermSSORead), adminH.ListOAuthClients)
	admin.POST("/sso/clients", middleware.RequirePermission(authz.PermSSOWrite), adminH.CreateOAuthClient)
	admin.PATCH("/sso/clients/:id", middleware.RequirePermission(authz.PermSSOWrite), adminH.UpdateOAuthClient)
	admin.POST("/sso/clients/:id/reset-secret", middleware.RequirePermission(authz.PermSSOWrite), adminH.ResetOAuthClientSecret)
	admin.DELETE("/sso/clients/:id", middleware.RequirePermission(authz.PermSSOWrite), adminH.DeleteOAuthClient)
	return r
}

func TestCreateOAuthClientReturnsSecretOnce(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminSSORouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "sso-admin@example.com", "sso-admin")

	w := doAuthJSON(r, http.MethodPost, "/api/admin/sso/clients", token, map[string]any{
		"name":         "blog",
		"redirectUris": []string{"https://blog.example.com/callback", "http://localhost:5173/callback"},
		"productId":    "youc-canvas",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("创建应 201, got %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Client       map[string]any `json:"client"`
		ClientSecret string         `json:"clientSecret"`
	}
	decodeSSOBody(t, w, &body)
	if body.Client["clientId"] == "" || !strings.HasPrefix(body.Client["clientId"].(string), "ic_") {
		t.Fatalf("clientId 应以 ic_ 开头: %v", body.Client["clientId"])
	}
	if len(body.ClientSecret) != 48 {
		t.Fatalf("明文 secret 应为 48 位十六进制, got %d", len(body.ClientSecret))
	}
	uris := body.Client["redirectUris"].([]any)
	if len(uris) != 2 {
		t.Fatalf("回调清单应原样保留两项: %v", uris)
	}
	// 库里只存摘要不存明文。
	var row model.OAuthClient
	if err := g.First(&row, "client_id = ?", body.Client["clientId"]).Error; err != nil {
		t.Fatalf("读取客户端失败: %v", err)
	}
	if row.ClientSecretHash == body.ClientSecret || len(row.ClientSecretHash) != 64 {
		t.Fatalf("库内必须只存 64 位 SHA-256 摘要: %s", row.ClientSecretHash)
	}
}

func TestCreateOAuthClientRejectsInsecureRedirect(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminSSORouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "sso-admin2@example.com", "sso-admin2")

	w := doAuthJSON(r, http.MethodPost, "/api/admin/sso/clients", token, map[string]any{
		"name":         "bad",
		"redirectUris": []string{"http://evil.example.com/callback"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非回环 http 回调应 400, got %d %s", w.Code, w.Body.String())
	}
	if fields := errFields(t, w); fields["redirectUris"] == "" {
		t.Fatalf("应带 redirectUris 字段错误: %s", w.Body.String())
	}
}

func TestResetOAuthClientSecretRotatesHash(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminSSORouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "sso-admin3@example.com", "sso-admin3")

	w := doAuthJSON(r, http.MethodPost, "/api/admin/sso/clients", token, map[string]any{
		"name": "app", "redirectUris": []string{"https://app.example.com/cb"}, "productId": "youc-canvas",
	})
	var created struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
		ClientSecret string `json:"clientSecret"`
	}
	decodeSSOBody(t, w, &created)
	oldHash := ""
	var row model.OAuthClient
	g.First(&row, "id = ?", created.Client.ID)
	oldHash = row.ClientSecretHash

	w = doAuthJSON(r, http.MethodPost, "/api/admin/sso/clients/"+created.Client.ID+"/reset-secret", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("重置应 200, got %d %s", w.Code, w.Body.String())
	}
	g.First(&row, "id = ?", created.Client.ID)
	if row.ClientSecretHash == oldHash {
		t.Fatal("重置后摘要必须变化")
	}
}

func TestDeleteOAuthClient(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminSSORouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "sso-admin4@example.com", "sso-admin4")

	w := doAuthJSON(r, http.MethodPost, "/api/admin/sso/clients", token, map[string]any{
		"name": "gone", "redirectUris": []string{"https://gone.example.com/cb"}, "productId": "youc-canvas",
	})
	var created struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	decodeSSOBody(t, w, &created)
	w = doAuthJSON(r, http.MethodDelete, "/api/admin/sso/clients/"+created.Client.ID, token, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("删除应 204, got %d %s", w.Code, w.Body.String())
	}
	w = doAuthJSON(r, http.MethodDelete, "/api/admin/sso/clients/"+created.Client.ID, token, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("重复删除应 404, got %d", w.Code)
	}
}
