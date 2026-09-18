package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
)

func newAuthzTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatalf("获取底层连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := g.AutoMigrate(&model.PlatformUser{}, &model.Role{}, &model.Permission{}, &model.RolePermission{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := authz.Sync(g); err != nil {
		t.Fatalf("同步角色与权限目录失败: %v", err)
	}
	return g
}

func newAuthzUser(t *testing.T, g *gorm.DB, roleKey *string) model.PlatformUser {
	t.Helper()
	user := model.PlatformUser{
		ID:           uuid.New(),
		Email:        uuid.NewString() + "@example.com",
		Username:     "u" + uuid.NewString()[:8],
		PasswordHash: "x",
		Role:         authz.RoleProjection(roleKey),
		RoleKey:      roleKey,
		Status:       "active",
	}
	if err := g.Create(&user).Error; err != nil {
		t.Fatalf("写入用户失败: %v", err)
	}
	return user
}

// newAuthzRouter 按生产结构挂载：Auth → LoadAdminAccess（withLoader 控制）→ RequirePermission。
func newAuthzRouter(t *testing.T, g *gorm.DB, withLoader bool, permission string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	secret := []byte("middleware-authz-test-secret-middleware")
	group := r.Group("/api/admin", Auth(secret))
	if withLoader {
		group.Use(LoadAdminAccess(identity.NewService(g), g))
	}
	group.GET("/probe", RequirePermission(permission), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func authzRequest(r http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/probe", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequirePermissionFailsClosedWithoutLoader(t *testing.T) {
	g := newAuthzTestDB(t)
	key := authz.SystemRoleKey
	user := newAuthzUser(t, g, &key)
	t.Cleanup(func() {})
	token, err := auth.IssueAccessToken(&user, []byte("middleware-authz-test-secret-middleware"))
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	// 漏挂 LoadAdminAccess 时必须拒绝，而不是放行。
	r := newAuthzRouter(t, g, false, authz.PermStatsRead)
	if w := authzRequest(r, token); w.Code != http.StatusForbidden {
		t.Fatalf("缺少 LoadAdminAccess 应 403, got %d %s", w.Code, w.Body.String())
	}
}

func TestRequirePermissionRejectsUnknownKey(t *testing.T) {
	g := newAuthzTestDB(t)
	key := authz.SystemRoleKey
	user := newAuthzUser(t, g, &key)
	token, err := auth.IssueAccessToken(&user, []byte("middleware-authz-test-secret-middleware"))
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	r := newAuthzRouter(t, g, true, "stats.unknown")
	if w := authzRequest(r, token); w.Code != http.StatusForbidden {
		t.Fatalf("未注册的权限点应 403, got %d %s", w.Code, w.Body.String())
	}
}

func TestLoadAdminAccessDeniesUsersWithoutRole(t *testing.T) {
	g := newAuthzTestDB(t)
	user := newAuthzUser(t, g, nil)
	token, err := auth.IssueAccessToken(&user, []byte("middleware-authz-test-secret-middleware"))
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	r := newAuthzRouter(t, g, true, authz.PermStatsRead)
	if w := authzRequest(r, token); w.Code != http.StatusForbidden {
		t.Fatalf("无后台角色应 403, got %d %s", w.Code, w.Body.String())
	}
}

func TestLoadAdminAccessDeniesDanglingRoleKey(t *testing.T) {
	g := newAuthzTestDB(t)
	ghost := "ghost-role"
	user := newAuthzUser(t, g, &ghost)
	token, err := auth.IssueAccessToken(&user, []byte("middleware-authz-test-secret-middleware"))
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	r := newAuthzRouter(t, g, true, authz.PermStatsRead)
	if w := authzRequest(r, token); w.Code != http.StatusForbidden {
		t.Fatalf("角色已不存在应失败关闭 403, got %d %s", w.Code, w.Body.String())
	}
}

func TestLoadAdminAccessGrantsSystemRoleAndAssignedPermissions(t *testing.T) {
	g := newAuthzTestDB(t)
	systemKey := authz.SystemRoleKey
	admin := newAuthzUser(t, g, &systemKey)
	adminToken, err := auth.IssueAccessToken(&admin, []byte("middleware-authz-test-secret-middleware"))
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	// 系统角色隐式拥有全部权限，即使 role_permissions 里一行都没有。
	r := newAuthzRouter(t, g, true, authz.PermOrdersRefund)
	if w := authzRequest(r, adminToken); w.Code != http.StatusOK {
		t.Fatalf("系统角色应隐式放行全部权限, got %d %s", w.Code, w.Body.String())
	}

	editor := "editor"
	if err := g.Create(&model.Role{Key: editor, Name: "编辑", IsSystem: false}).Error; err != nil {
		t.Fatalf("写入自定义角色失败: %v", err)
	}
	if err := g.Create(&model.RolePermission{RoleKey: editor, PermissionKey: authz.PermOrdersRefund}).Error; err != nil {
		t.Fatalf("写入权限分配失败: %v", err)
	}
	editorUser := newAuthzUser(t, g, &editor)
	editorToken, err := auth.IssueAccessToken(&editorUser, []byte("middleware-authz-test-secret-middleware"))
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	if w := authzRequest(r, editorToken); w.Code != http.StatusOK {
		t.Fatalf("已授予的权限应放行, got %d %s", w.Code, w.Body.String())
	}
	notGranted := newAuthzRouter(t, g, true, authz.PermStatsRead)
	if w := authzRequest(notGranted, editorToken); w.Code != http.StatusForbidden {
		t.Fatalf("未授予的权限应 403, got %d %s", w.Code, w.Body.String())
	}
}
