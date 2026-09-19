package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/testutil"
)

func newAdminMetaRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	h := NewAdminHandler(g, cfg, testutil.NewFakeStorage("local"))
	admin := r.Group("/api/admin", middleware.Auth(secret), middleware.RequireActiveUser(identity.NewService(g)), middleware.LoadAdminAccess(identity.NewService(g), g))
	admin.GET("/meta", h.AdminMeta)
	return r
}

// AdminMeta 是多产品后台的引导接口：产品标识固定，模块清单按调用者权限过滤。
func TestAdminMetaFiltersModulesByPermission(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMetaRouter(t, g, cfg)

	// 系统管理员：全部模块。
	admin := testutil.CreateUser(t, g, "meta-admin@example.com", "metaadmin", "password123", true)
	adminKey := authz.SystemRoleKey
	testutil.SetUserRole(t, g, &admin, &adminKey)
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/meta", testutil.AccessToken(t, cfg, &admin), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("管理员取 meta 应 200: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Product struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"product"`
		Modules []struct {
			Key         string `json:"key"`
			Permissions []struct {
				Key string `json:"key"`
			} `json:"permissions"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析 meta 失败: %v", err)
	}
	if body.Product.ID != "youc-canvas" || body.Product.Name == "" {
		t.Fatalf("产品标识不符: %+v", body.Product)
	}
	if len(body.Product.Version) == 0 {
		t.Fatalf("meta 应带版本号")
	}
	found := map[string]int{}
	for _, module := range body.Modules {
		found[module.Key] = len(module.Permissions)
	}
	if found["stats"] == 0 || found["roles"] == 0 {
		t.Fatalf("系统管理员应看到 stats 与 roles 模块: %v", found)
	}

	// 受限角色：只有 users.read，模块清单应只含 users 且只带这一个权限点。
	if err := g.Create(&model.Role{Key: "meta-viewer", Name: "只读查看"}).Error; err != nil {
		t.Fatalf("创建受限角色失败: %v", err)
	}
	if err := g.Create(&model.RolePermission{RoleKey: "meta-viewer", PermissionKey: authz.PermUsersRead}).Error; err != nil {
		t.Fatalf("分配受限角色权限失败: %v", err)
	}
	limited := testutil.CreateUser(t, g, "meta-limited@example.com", "metalimited", "password123", true)
	viewerKey := "meta-viewer"
	testutil.SetUserRole(t, g, &limited, &viewerKey)
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/meta", testutil.AccessToken(t, cfg, &limited), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("受限角色取 meta 应 200: %d %s", w.Code, w.Body.String())
	}
	body = struct {
		Product struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"product"`
		Modules []struct {
			Key         string `json:"key"`
			Permissions []struct {
				Key string `json:"key"`
			} `json:"permissions"`
		} `json:"modules"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析受限 meta 失败: %v", err)
	}
	if len(body.Modules) != 1 || body.Modules[0].Key != "users" {
		t.Fatalf("只授 users.read 时应只见 users 模块: %+v", body.Modules)
	}
	if len(body.Modules[0].Permissions) != 1 || body.Modules[0].Permissions[0].Key != authz.PermUsersRead {
		t.Fatalf("users 模块应只含 users.read: %+v", body.Modules[0].Permissions)
	}
}

// 无后台角色的账号不能取 meta。
func TestAdminMetaRejectsNonAdmin(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMetaRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "meta-user@example.com", "metauser", "password123", true)
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/meta", testutil.AccessToken(t, cfg, &user), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无后台角色应 403: %d", w.Code)
	}
}
