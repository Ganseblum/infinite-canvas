package main

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/handler"
)

// expectedAdminRouteCount 是管理后台的路由总数：41 条既有路由 + 7 条 RBAC 路由 + 管理员建号 + 用量分析。
// +1 条 /admin/meta（多产品后台引导，免权限点）+ 4 条会员订阅路由。
// 增删管理路由必须同步改这个数字，让漏改权限的改动无法悄悄通过。
const expectedAdminRouteCount = 60

func newAdminRouteTestEngine(t *testing.T) (*gin.Engine, []adminRouteSpec) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewAdminHandlerWithUpstream(nil, &config.Config{}, nil, nil)
	specs := registerAdminRoutes(r.Group("/api/admin"), h)
	return r, specs
}

func TestAdminRoutesAllCarryRegisteredPermission(t *testing.T) {
	r, specs := newAdminRouteTestEngine(t)
	if len(specs) != expectedAdminRouteCount {
		t.Fatalf("管理路由条数应为 %d, got %d", expectedAdminRouteCount, len(specs))
	}
	if len(r.Routes()) != expectedAdminRouteCount {
		t.Fatalf("gin 路由表条数应为 %d, got %d", expectedAdminRouteCount, len(r.Routes()))
	}
	registered := make(map[string]bool, len(r.Routes()))
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	// allowlist 只允许 GET /admin/me 与 GET /admin/meta 不带权限点：都只要求「有后台角色」。
	openRoutes := map[string]bool{"GET /me": true, "GET /meta": true}
	for _, spec := range specs {
		full := spec.Method + " " + spec.Path
		if !registered[spec.Method+" /api/admin"+spec.Path] {
			t.Fatalf("路由 %s 未真正注册到 gin", full)
		}
		if spec.Permission == "" {
			if !openRoutes[full] {
				t.Fatalf("管理路由 %s 缺少权限点（allowlist 只允许 GET /me）", full)
			}
			continue
		}
		if !authz.IsKnown(spec.Permission) {
			t.Fatalf("路由 %s 的权限点 %s 不在 authz 注册表中", full, spec.Permission)
		}
	}
	// 反向检查：gin 路由表里的每条管理路由都必须出现在路由表清单里。
	for _, route := range r.Routes() {
		found := false
		for _, spec := range specs {
			if spec.Method == route.Method && "/api/admin"+spec.Path == route.Path {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("gin 路由表里存在未登记权限点清单的路由 %s %s", route.Method, route.Path)
		}
	}
}

// TestEveryRegisteredPermissionIsUsedBySomeRoute 反向锁住权限点与路由的对应关系：
// 注册表新增权限点却没有任何路由认领时，测试失败，避免“界面能配但实际没有执行点”。
func TestEveryRegisteredPermissionIsUsedBySomeRoute(t *testing.T) {
	_, specs := newAdminRouteTestEngine(t)
	used := map[string]bool{}
	for _, spec := range specs {
		if spec.Permission != "" {
			used[spec.Permission] = true
		}
	}
	for _, key := range authz.Keys() {
		if !used[key] {
			t.Fatalf("权限点 %s 没有任何管理路由使用", key)
		}
	}
	if len(used) != len(authz.Keys()) {
		t.Fatalf("路由使用的权限点数应等于注册表数量 %d, got %d", len(authz.Keys()), len(used))
	}
}

// TestAdminMembershipRoutesRegistered 锁定会员订阅四条路由的权限点：
// 列表读 membership.read，发放/补偿/作废写 membership.write。
func TestAdminMembershipRoutesRegistered(t *testing.T) {
	_, specs := newAdminRouteTestEngine(t)
	want := map[string]string{
		"GET /membership/subscriptions":        authz.PermMembershipRead,
		"POST /membership/grant":               authz.PermMembershipWrite,
		"POST /membership/compensate":          authz.PermMembershipWrite,
		"DELETE /membership/subscriptions/:id": authz.PermMembershipWrite,
	}
	got := make(map[string]string, len(specs))
	for _, spec := range specs {
		got[spec.Method+" "+spec.Path] = spec.Permission
	}
	for full, permission := range want {
		if got[full] != permission {
			t.Fatalf("路由 %s 应注册权限点 %s, got %q", full, permission, got[full])
		}
	}
}

func TestAdminRouteRegistrarPanicsOnUnknownPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewAdminHandlerWithUpstream(nil, &config.Config{}, nil, nil)
	defer func() {
		if recover() == nil {
			t.Fatalf("未注册的权限点必须在启动期 panic")
		}
	}()
	r := &adminRoutes{group: gin.New().Group("/api/admin")}
	r.GET("/boom", "stats.unknown", h.Me)
}

func TestAdminRouteRegistrarPanicsOnMissingPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewAdminHandlerWithUpstream(nil, &config.Config{}, nil, nil)
	defer func() {
		if recover() == nil {
			t.Fatalf("非 allowlist 路由缺少权限点必须在启动期 panic")
		}
	}()
	r := &adminRoutes{group: gin.New().Group("/api/admin")}
	r.POST("/boom", "", h.Me)
}
