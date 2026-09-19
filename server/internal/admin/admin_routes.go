package main

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/handler"
	"github.com/infinite-canvas/server/internal/middleware"
)

// openAdminRoutes 是唯一允许不带权限点注册的管理路由：GET /me 与 GET /meta 只要求有
// 后台角色（/me 渲染菜单与改密闸门，/meta 是多产品后台的引导数据），不能要求某个具体权限点。
var openAdminRoutes = map[string]bool{"GET /me": true, "GET /meta": true}

// adminRouteSpec 记录一条已注册的管理路由及其权限点，供路由表测试断言。
type adminRouteSpec struct {
	Method     string
	Path       string
	Permission string
}

// adminRoutes 是管理路由的唯一注册入口：只暴露带权限参数的方法，原始 *gin.RouterGroup 不外泄。
// 注册时即校验权限点存在于代码注册表，未知权限点直接 panic，让漏挂/写错权限在启动期暴露。
type adminRoutes struct {
	group *gin.RouterGroup
	specs []adminRouteSpec
}

func (r *adminRoutes) GET(path, permission string, h gin.HandlerFunc) {
	r.register("GET", path, permission, h)
}

func (r *adminRoutes) POST(path, permission string, h gin.HandlerFunc) {
	r.register("POST", path, permission, h)
}

func (r *adminRoutes) PATCH(path, permission string, h gin.HandlerFunc) {
	r.register("PATCH", path, permission, h)
}

func (r *adminRoutes) DELETE(path, permission string, h gin.HandlerFunc) {
	r.register("DELETE", path, permission, h)
}

func (r *adminRoutes) register(method, path, permission string, h gin.HandlerFunc) {
	full := method + " " + path
	if permission == "" {
		if !openAdminRoutes[full] {
			panic(fmt.Sprintf("管理路由 %s 必须注册权限点", full))
		}
	} else if !authz.IsKnown(permission) {
		panic(fmt.Sprintf("管理路由 %s 指定的权限点 %s 未在 authz 注册表中", full, permission))
	}
	handlers := make([]gin.HandlerFunc, 0, 2)
	if permission != "" {
		handlers = append(handlers, middleware.RequirePermission(permission))
	}
	handlers = append(handlers, h)
	r.group.Handle(method, path, handlers...)
	r.specs = append(r.specs, adminRouteSpec{Method: method, Path: path, Permission: permission})
}

// registerAdminRoutes 注册全部管理路由并返回路由表，调用方必须已经挂好
// Auth、RequireActiveUser 与 LoadAdminAccess 三个组级中间件。
func registerAdminRoutes(g *gin.RouterGroup, h *handler.AdminHandler) []adminRouteSpec {
	r := &adminRoutes{group: g}

	r.GET("/me", "", h.Me)
	r.GET("/meta", "", h.AdminMeta)
	r.GET("/stats", authz.PermStatsRead, h.Stats)
	r.GET("/stats/revenue", authz.PermStatsRevenue, h.RevenueStats)
	r.GET("/analytics/usage", authz.PermStatsUsage, h.UsageAnalytics)

	r.GET("/users", authz.PermUsersRead, h.ListUsers)
	r.POST("/users", authz.PermRolesManage, h.CreateUser)
	r.GET("/users/:id", authz.PermUsersRead, h.GetUser)
	r.PATCH("/users/:id", authz.PermUsersWrite, h.PatchUser)
	r.POST("/users/:id/password", authz.PermUsersWrite, h.ResetPassword)
	r.POST("/users/:id/credits", authz.PermUsersCredits, h.AdjustCredits)
	r.POST("/users/:id/usage/recalculate", authz.PermUsersWrite, h.RecalculateUsage)
	r.POST("/users/:id/media/reclaim", authz.PermUsersWrite, h.ReclaimMedia)
	r.PATCH("/users/:id/role", authz.PermRolesManage, h.AssignUserRole)

	r.GET("/models", authz.PermModelsRead, h.ListModels)
	r.POST("/models", authz.PermModelsWrite, h.CreateModel)
	r.PATCH("/models/:id", authz.PermModelsWrite, h.UpdateModel)
	r.DELETE("/models/:id", authz.PermModelsWrite, h.DeleteModel)
	r.GET("/model-promotions", authz.PermModelsRead, h.ListPromotions)
	r.POST("/model-promotions", authz.PermModelsWrite, h.CreatePromotion)
	r.PATCH("/model-promotions/:id", authz.PermModelsWrite, h.UpdatePromotion)

	r.GET("/credit-packages", authz.PermPackagesRead, h.ListPackages)
	r.POST("/credit-packages", authz.PermPackagesWrite, h.CreatePackage)
	r.PATCH("/credit-packages/:id", authz.PermPackagesWrite, h.UpdatePackage)
	r.GET("/orders", authz.PermOrdersRead, h.ListOrders)
	r.POST("/requests/refunds/retry", authz.PermOrdersRefund, h.RetryRefunds)

	// ===== 会员订阅 =====
	r.GET("/membership/subscriptions", authz.PermMembershipRead, h.ListSubscriptions)
	r.POST("/membership/grant", authz.PermMembershipWrite, h.GrantSubscription)
	r.POST("/membership/compensate", authz.PermMembershipWrite, h.CompensateSubscription)
	r.DELETE("/membership/subscriptions/:id", authz.PermMembershipWrite, h.RevokeSubscription)

	// ===== SSO 接入客户端（PLAN T10）=====
	r.GET("/sso/clients", authz.PermSSORead, h.ListOAuthClients)
	r.POST("/sso/clients", authz.PermSSOWrite, h.CreateOAuthClient)
	r.PATCH("/sso/clients/:id", authz.PermSSOWrite, h.UpdateOAuthClient)
	r.POST("/sso/clients/:id/reset-secret", authz.PermSSOWrite, h.ResetOAuthClientSecret)
	r.DELETE("/sso/clients/:id", authz.PermSSOWrite, h.DeleteOAuthClient)

	r.GET("/channels", authz.PermChannelsRead, h.ListChannels)
	r.POST("/channels", authz.PermChannelsWrite, h.CreateChannel)
	r.PATCH("/channels/:id", authz.PermChannelsWrite, h.UpdateChannel)
	r.DELETE("/channels/:id", authz.PermChannelsWrite, h.DeleteChannel)

	r.GET("/moderation/records", authz.PermModerationRead, h.ListModerationRecords)
	r.GET("/moderation/records/:id", authz.PermModerationRead, h.GetModerationRecord)
	r.GET("/moderation/records/:id/preview", authz.PermModerationRead, h.PreviewModerationArtifact)
	r.PATCH("/moderation/records/:id", authz.PermModerationReview, h.ReviewModerationRecord)
	r.POST("/moderation/records/:id/compensate", authz.PermModerationCompensate, h.CompensateModeration)
	r.GET("/moderation/stats", authz.PermModerationRead, h.ModerationStats)

	r.GET("/settings", authz.PermSettingsRead, h.GetSettings)
	r.PATCH("/settings", authz.PermSettingsWrite, h.UpdateSettings)

	// 过渡期兼容：旧 /admins 接口内部映射到角色模型。
	r.GET("/admins", authz.PermRolesRead, h.ListAdmins)
	r.POST("/admins", authz.PermRolesManage, h.AddAdmin)
	r.DELETE("/admins/:id", authz.PermRolesManage, h.RemoveAdmin)

	r.GET("/roles", authz.PermRolesRead, h.ListRoles)
	r.POST("/roles", authz.PermRolesManage, h.CreateRole)
	r.PATCH("/roles/:key", authz.PermRolesManage, h.UpdateRole)
	r.DELETE("/roles/:key", authz.PermRolesManage, h.DeleteRole)
	r.GET("/permissions", authz.PermRolesRead, h.ListPermissions)

	r.GET("/audit-logs", authz.PermAuditRead, h.ListAuditLogs)
	r.GET("/community/works", authz.PermCommunityRead, h.ListCommunityWorks)
	r.PATCH("/community/works/:id", authz.PermCommunityWrite, h.PatchCommunityWork)
	r.GET("/community/reports", authz.PermCommunityRead, h.ListCommunityReports)
	r.PATCH("/community/reports/:id", authz.PermCommunityWrite, h.PatchCommunityReport)
	return r.specs
}
