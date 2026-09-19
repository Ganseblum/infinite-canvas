package admin

import (
	"net/http"
	"strings"
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

// newAdminAccessRouter 按生产同样的组级中间件与权限参数注册管理路由，用于 RBAC 行为测试。
func newAdminAccessRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	h := NewAdminHandler(g, cfg, testutil.NewFakeStorage("local"))
	admin := r.Group("/api/admin", middleware.Auth(secret), middleware.RequireActiveUser(identity.NewService(g)), middleware.LoadAdminAccess(identity.NewService(g), g))
	admin.GET("/me", h.Me)
	admin.GET("/users", middleware.RequirePermission(authz.PermUsersRead), h.ListUsers)
	admin.PATCH("/users/:id", middleware.RequirePermission(authz.PermUsersWrite), h.PatchUser)
	admin.POST("/users/:id/credits", middleware.RequirePermission(authz.PermUsersCredits), h.AdjustCredits)
	admin.GET("/roles", middleware.RequirePermission(authz.PermRolesRead), h.ListRoles)
	admin.POST("/roles", middleware.RequirePermission(authz.PermRolesManage), h.CreateRole)
	admin.PATCH("/roles/:key", middleware.RequirePermission(authz.PermRolesManage), h.UpdateRole)
	admin.DELETE("/roles/:key", middleware.RequirePermission(authz.PermRolesManage), h.DeleteRole)
	admin.GET("/permissions", middleware.RequirePermission(authz.PermRolesRead), h.ListPermissions)
	admin.PATCH("/users/:id/role", middleware.RequirePermission(authz.PermRolesManage), h.AssignUserRole)
	return r
}

func createRole(t *testing.T, r http.Handler, token, key, name string) {
	t.Helper()
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/roles", token, map[string]any{"key": key, "name": name})
	if w.Code != http.StatusCreated {
		t.Fatalf("创建角色 %s 失败: %d %s", key, w.Code, w.Body.String())
	}
}

func assignRole(t *testing.T, r http.Handler, token, userID, roleKey string) {
	t.Helper()
	var body map[string]any
	if roleKey != "" {
		body = map[string]any{"roleKey": roleKey}
	} else {
		body = map[string]any{"roleKey": nil}
	}
	w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/users/"+userID+"/role", token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("分配角色失败: %d %s", w.Code, w.Body.String())
	}
}

func grantPermissions(t *testing.T, r http.Handler, token, roleKey string, permissions []string) {
	t.Helper()
	w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/roles/"+roleKey, token, map[string]any{"permissions": permissions})
	if w.Code != http.StatusOK {
		t.Fatalf("分配权限失败: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminMeReturnsRoleAndPermissions(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "me-admin@example.com", "meadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	token := testutil.AccessToken(t, cfg, &admin)

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/me", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取 /admin/me 失败: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	role, ok := body["role"].(map[string]any)
	if !ok || role["key"] != "admin" || role["isSystem"] != true {
		t.Fatalf("系统角色应隐式全量: %s", w.Body.String())
	}
	permissions, ok := body["permissions"].([]any)
	if !ok || len(permissions) != len(authz.Keys()) {
		t.Fatalf("系统角色权限集合应为全部 %d 个权限点: %s", len(authz.Keys()), w.Body.String())
	}

	// 没有后台角色的普通用户连 /admin/me 都不能访问。
	normal := testutil.CreateUser(t, g, "me-normal@example.com", "menormal", "password123", true)
	normalToken := testutil.AccessToken(t, cfg, &normal)
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/me", normalToken, nil)
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "FORBIDDEN" {
		t.Fatalf("无后台角色访问 /admin/me 应 403 FORBIDDEN, got %d %s", w.Code, w.Body.String())
	}
}

func TestPermissionDeniedForUserWithoutRole(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	normal := testutil.CreateUser(t, g, "norole@example.com", "norole", "password123", true)
	token := testutil.AccessToken(t, cfg, &normal)

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", token, nil)
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "FORBIDDEN" {
		t.Fatalf("无角色用户访问管理接口应 403 FORBIDDEN, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), authz.PermUsersRead) {
		t.Fatalf("403 响应不允许回传缺失的权限点，避免把接口当权限探针: %s", w.Body.String())
	}
}

func TestCustomRoleOnlyGrantsAssignedRoutes(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "rbac-admin@example.com", "rbacadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)

	createRole(t, r, adminToken, "support", "客服")
	operator := testutil.CreateUser(t, g, "rbac-support@example.com", "rbacsupport", "password123", true)
	operatorToken := testutil.AccessToken(t, cfg, &operator)
	assignRole(t, r, adminToken, operator.ID.String(), "support")

	// 角色还没有任何权限：管理接口一律 403。
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", operatorToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("空权限角色访问用户列表应 403, got %d %s", w.Code, w.Body.String())
	}

	grantPermissions(t, r, adminToken, "support", []string{authz.PermUsersRead})
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", operatorToken, nil); w.Code != http.StatusOK {
		t.Fatalf("已授予 users.read 应放行, got %d %s", w.Code, w.Body.String())
	}
	// 未被授予的路由必须拒绝。
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/roles", operatorToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("未授予 roles.read 应 403, got %d %s", w.Code, w.Body.String())
	}
	target := testutil.CreateUser(t, g, "rbac-target@example.com", "rbactarget", "password123", true)
	creditBody := map[string]any{"bucket": "granted", "amountMicros": 100, "note": "测试"}
	if w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", operatorToken, creditBody); w.Code != http.StatusForbidden {
		t.Fatalf("未授予 users.credits 应 403, got %d %s", w.Code, w.Body.String())
	}

	// 追加授权后同一条 access token 立刻生效（权限每请求查库，不读 JWT claim）。
	grantPermissions(t, r, adminToken, "support", []string{authz.PermUsersRead, authz.PermUsersCredits})
	if w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/credits", operatorToken, creditBody); w.Code != http.StatusOK {
		t.Fatalf("授予 users.credits 后应立即放行, got %d %s", w.Code, w.Body.String())
	}
	me := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/me", operatorToken, nil)
	meBody := testutil.DecodeBody(t, me)
	perms, _ := meBody["permissions"].([]any)
	if len(perms) != 2 {
		t.Fatalf("自定义角色的权限集合应只含被授予的两项: %s", me.Body.String())
	}
}

// TestFailClosedOnUnknownPermissionKey 覆盖失败关闭：role_permissions 出现注册表外的 key 时，
// 中间件忽略它而不是放行；typo 只会让谁都进不去，不会让谁都能进。
func TestFailClosedOnUnknownPermissionKey(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "typo-admin@example.com", "typoadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)

	createRole(t, r, adminToken, "broken", "错别字角色")
	operator := testutil.CreateUser(t, g, "typo-user@example.com", "typouser", "password123", true)
	operatorToken := testutil.AccessToken(t, cfg, &operator)
	assignRole(t, r, adminToken, operator.ID.String(), "broken")
	if err := g.Create(&model.RolePermission{RoleKey: "broken", PermissionKey: "users.raed"}).Error; err != nil {
		t.Fatalf("写入错误权限分配失败: %v", err)
	}

	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", operatorToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("未注册的权限 key 必须拒绝, got %d %s", w.Code, w.Body.String())
	}
	me := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/me", operatorToken, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("角色本身有效，/admin/me 应 200, got %d", me.Code)
	}
	perms, _ := testutil.DecodeBody(t, me)["permissions"].([]any)
	if len(perms) != 0 {
		t.Fatalf("未注册的权限不允许出现在权限集合里: %s", me.Body.String())
	}
}

// TestRoleChangeTakesEffectImmediately 覆盖降权立即生效：不改 token、不等 15 分钟。
func TestRoleChangeTakesEffectImmediately(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "demote-admin@example.com", "demoteadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)
	createRole(t, r, adminToken, "viewer", "只读")
	grantPermissions(t, r, adminToken, "viewer", []string{authz.PermUsersRead})

	operator := testutil.CreateUser(t, g, "demote-user@example.com", "demoteuser", "password123", true)
	operatorToken := testutil.AccessToken(t, cfg, &operator)
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", operatorToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("尚未授权时应 403, got %d", w.Code)
	}
	assignRole(t, r, adminToken, operator.ID.String(), "viewer")
	// 同一条 token 立刻可用：说明授权来自库里的角色，不是签发时的 JWT claim。
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", operatorToken, nil); w.Code != http.StatusOK {
		t.Fatalf("授权后同一条 token 应立即生效, got %d %s", w.Code, w.Body.String())
	}
	assignRole(t, r, adminToken, operator.ID.String(), "")
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", operatorToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("降权后同一条 token 必须立刻 403, got %d", w.Code)
	}
	// refresh token 同步撤销。
	var revoked int64
	if err := g.Model(&model.Session{}).Where("user_id = ? AND revoked_at IS NULL", operator.ID).Count(&revoked).Error; err != nil {
		t.Fatalf("统计 refresh token 失败: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("角色变更后该用户的 refresh token 应全部撤销, 未撤销 %d 条", revoked)
	}
}

// TestLockoutGuards 覆盖防锁死四类操作：改自己角色、动最后一个系统角色成员、
// 删除仍有成员的角色、编辑系统角色权限，全部被拒且不留状态变更。
func TestLockoutGuards(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "lock-admin@example.com", "lockadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)

	// 1) 不能修改自己的角色。
	w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/users/"+admin.ID.String()+"/role", adminToken, map[string]any{"roleKey": nil})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("修改自己的角色应被拒, got %d %s", w.Code, w.Body.String())
	}

	// 2) 系统角色不可编辑权限、不可删除。
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/roles/admin", adminToken, map[string]any{"permissions": []string{authz.PermStatsRead}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("编辑系统角色权限应被拒, got %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/roles/admin", adminToken, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("删除系统角色应被拒, got %d %s", w.Code, w.Body.String())
	}
	// 显示名允许修改，系统角色仍在。
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/roles/admin", adminToken, map[string]any{"name": "超级管理员"})
	if w.Code != http.StatusOK {
		t.Fatalf("系统角色显示名应可修改, got %d %s", w.Code, w.Body.String())
	}
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users", adminToken, nil); w.Code != http.StatusOK {
		t.Fatalf("系统角色不允许被锁死, got %d %s", w.Code, w.Body.String())
	}

	// 3) 不能删除仍有成员的角色。
	createRole(t, r, adminToken, "editor", "编辑")
	member := testutil.CreateUser(t, g, "lock-member@example.com", "lockmember", "password123", true)
	assignRole(t, r, adminToken, member.ID.String(), "editor")
	w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/roles/editor", adminToken, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("删除仍有成员的角色应被拒, got %d %s", w.Code, w.Body.String())
	}
	var editorCount int64
	g.Model(&model.Role{}).Where("role_key = ?", "editor").Count(&editorCount)
	if editorCount != 1 {
		t.Fatalf("被拒的删除不允许产生状态变更")
	}
	assignRole(t, r, adminToken, member.ID.String(), "")
	if w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/roles/editor", adminToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("成员清空后应可删除角色, got %d %s", w.Code, w.Body.String())
	}

	// 4) 系统角色必须保留至少一个 active 用户：用带 roles.manage 的自定义角色去尝试。
	createRole(t, r, adminToken, "ops", "运维")
	grantPermissions(t, r, adminToken, "ops", []string{authz.PermRolesManage, authz.PermUsersWrite})
	operator := testutil.CreateUser(t, g, "lock-ops@example.com", "lockops", "password123", true)
	assignRole(t, r, adminToken, operator.ID.String(), "ops")
	operatorToken := testutil.AccessToken(t, cfg, &operator)
	var auditBefore int64
	g.Model(&model.AdminAuditLog{}).Where("action = ?", "user.role").Count(&auditBefore)

	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/users/"+admin.ID.String()+"/role", operatorToken, map[string]any{"roleKey": nil})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("降掉最后一个系统角色成员应被拒, got %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/users/"+admin.ID.String(), operatorToken, map[string]any{"status": "disabled"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("封禁最后一个系统角色成员应被拒, got %d %s", w.Code, w.Body.String())
	}
	var reloaded model.PlatformUser
	if err := g.First(&reloaded, "id = ?", admin.ID).Error; err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	if !isSystemRoleKey(reloaded.RoleKey) || reloaded.Status != "active" {
		t.Fatalf("被拒的操作不允许改动系统角色成员: role_key=%v status=%s", reloaded.RoleKey, reloaded.Status)
	}
	// 被拒操作不写审计：审计只记录真正发生的变更。
	var auditAfter int64
	g.Model(&model.AdminAuditLog{}).Where("action = ?", "user.role").Count(&auditAfter)
	if auditAfter != auditBefore {
		t.Fatalf("被拒操作不应写审计, 之前 %d 条, 之后 %d 条", auditBefore, auditAfter)
	}
}

// TestRoleChangesWriteAuditWithTextSnapshot 覆盖审计摘要存文本快照：
// 角色被删除后，历史摘要仍能还原角色名与权限 key。
func TestRoleChangesWriteAuditWithTextSnapshot(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "audit-admin@example.com", "auditadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)

	createRole(t, r, adminToken, "support-audit", "客服支持")
	grantPermissions(t, r, adminToken, "support-audit", []string{authz.PermUsersRead, authz.PermUsersCredits})
	member := testutil.CreateUser(t, g, "audit-member@example.com", "auditmember", "password123", true)
	assignRole(t, r, adminToken, member.ID.String(), "support-audit")

	var permissionLog model.AdminAuditLog
	if err := g.Where("action = ?", "role.permissions").Order("created_at DESC").First(&permissionLog).Error; err != nil {
		t.Fatalf("应写角色权限审计: %v", err)
	}
	after := string(permissionLog.AfterSummary)
	if permissionLog.TargetType != "role" || permissionLog.TargetID != "support-audit" {
		t.Fatalf("角色审计的目标应为角色 key: %+v", permissionLog)
	}
	if !strings.Contains(after, "客服支持") || !strings.Contains(after, authz.PermUsersRead) {
		t.Fatalf("权限审计应存角色名与权限 key 的文本快照: %s", after)
	}

	var roleLog model.AdminAuditLog
	if err := g.Where("action = ?", "user.role").Order("created_at DESC").First(&roleLog).Error; err != nil {
		t.Fatalf("应写成员角色审计: %v", err)
	}
	if !strings.Contains(string(roleLog.AfterSummary), "客服支持") {
		t.Fatalf("成员角色审计应存角色名文本快照: %s", roleLog.AfterSummary)
	}

	// 删除角色后，历史审计仍能还原角色名与权限。
	assignRole(t, r, adminToken, member.ID.String(), "")
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/roles/support-audit", adminToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除角色失败: %d %s", w.Code, w.Body.String())
	}
	var deleteLog model.AdminAuditLog
	if err := g.Where("action = ?", "role.delete").Order("created_at DESC").First(&deleteLog).Error; err != nil {
		t.Fatalf("应写删除角色审计: %v", err)
	}
	before := string(deleteLog.BeforeSummary)
	if !strings.Contains(before, "客服支持") || !strings.Contains(before, authz.PermUsersCredits) {
		t.Fatalf("删除角色的审计应保留角色名与权限快照: %s", before)
	}
	var gone int64
	g.Model(&model.Role{}).Where("role_key = ?", "support-audit").Count(&gone)
	if gone != 0 {
		t.Fatalf("角色应已删除")
	}
}

func TestListPermissionsReturnsRegistry(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminAccessRouter(t, g, cfg)
	admin := testutil.CreateUser(t, g, "perm-admin@example.com", "permadmin", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	token := testutil.AccessToken(t, cfg, &admin)

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/permissions", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取权限清单失败: %d %s", w.Code, w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	total := 0
	modules := map[string]bool{}
	for _, group := range items {
		module, _ := group["module"].(string)
		if module == "" || group["moduleName"] == "" {
			t.Fatalf("权限分组缺少模块信息: %v", group)
		}
		modules[module] = true
		perms, ok := group["permissions"].([]any)
		if !ok {
			t.Fatalf("权限分组缺少 permissions 数组: %v", group)
		}
		total += len(perms)
	}
	if total != len(authz.Keys()) {
		t.Fatalf("权限点总数应为 %d, got %d", len(authz.Keys()), total)
	}
	if len(modules) < 5 {
		t.Fatalf("权限点应覆盖多个模块, got %v", modules)
	}
}
