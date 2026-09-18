package handler

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
	"github.com/infinite-canvas/server/internal/service"
)

// newSiteRouter 组装社区、活动与管理端站点设置路由。
func newSiteRouter(t *testing.T, g *gorm.DB, cfg *config.Config) (*gin.Engine, *service.SiteSettingService) {
	t.Helper()
	settings := service.NewSiteSettingService(g)
	if err := settings.Load(t.Context()); err != nil {
		t.Fatalf("加载站点设置失败: %v", err)
	}
	community := NewCommunityHandler(g, settings)
	activity := NewActivityHandler(g, settings, service.NewFreeGrantService(g), cfg)
	adminHandler := NewAdminHandler(g, cfg, newFakeStorage("local"))
	adminHandler.SetSettings(settings)

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	api := r.Group("/api")
	active := middleware.RequireActiveUser(g)

	group := api.Group("/community", middleware.Auth(secret), active)
	group.GET("/works", community.List)
	group.GET("/works/mine", community.MyWorks)
	group.POST("/works", community.Publish)
	group.GET("/works/:id", community.Get)
	group.DELETE("/works/:id", community.Delete)
	group.POST("/works/:id/like", community.Like)
	group.POST("/works/:id/unlike", community.Like)
	group.POST("/works/:id/report", community.Report)
	group.GET("/users/:id", community.UserProfile)

	act := api.Group("/activity", middleware.Auth(secret), active)
	act.GET("/checkin", activity.CheckinStatus)
	act.POST("/checkin", activity.Checkin)
	act.GET("/invite", activity.InviteInfo)
	act.POST("/invite/bind", activity.BindInvite)

	api.GET("/settings/public", adminHandler.PublicSettings)

	accountHandler := NewAccountHandler(g, cfg, service.NewFreeGrantService(g), NewAuthHandler(g, cfg, testMailer()))
	me := api.Group("/me", middleware.Auth(secret), active)
	me.GET("", accountHandler.GetMe)

	admin := api.Group("/admin", middleware.Auth(secret), active, middleware.LoadAdminAccess(g))
	admin.GET("/settings", middleware.RequirePermission(authz.PermSettingsRead), adminHandler.GetSettings)
	admin.PATCH("/settings", middleware.RequirePermission(authz.PermSettingsWrite), adminHandler.UpdateSettings)
	admin.GET("/admins", middleware.RequirePermission(authz.PermRolesRead), adminHandler.ListAdmins)
	admin.POST("/admins", middleware.RequirePermission(authz.PermRolesManage), adminHandler.AddAdmin)
	admin.DELETE("/admins/:id", middleware.RequirePermission(authz.PermRolesManage), adminHandler.RemoveAdmin)
	admin.GET("/audit-logs", middleware.RequirePermission(authz.PermAuditRead), adminHandler.ListAuditLogs)
	admin.GET("/community/works", middleware.RequirePermission(authz.PermCommunityRead), adminHandler.ListCommunityWorks)
	admin.PATCH("/community/works/:id", middleware.RequirePermission(authz.PermCommunityWrite), adminHandler.PatchCommunityWork)
	admin.GET("/community/reports", middleware.RequirePermission(authz.PermCommunityRead), adminHandler.ListCommunityReports)
	admin.PATCH("/community/reports/:id", middleware.RequirePermission(authz.PermCommunityWrite), adminHandler.PatchCommunityReport)
	admin.GET("/stats/revenue", middleware.RequirePermission(authz.PermStatsRevenue), adminHandler.RevenueStats)
	return r, settings
}

func createAdminUser(t *testing.T, g *gorm.DB, cfg *config.Config, email, username string) (model.User, string) {
	t.Helper()
	user := createUser(t, g, email, username, "password123", true)
	promoteAdmin(t, g, &user)
	return user, accessToken(t, cfg, &user)
}

func seedCommunityAsset(t *testing.T, g *gorm.DB, userID uuid.UUID) model.Asset {
	t.Helper()
	asset := model.Asset{
		ID: uuid.New(), UserID: userID, Kind: "image", Title: "作品素材",
		Data: []byte(`{"storageKey":"image:WorkAsset1"}`), StorageKey: "image:WorkAsset1", Bytes: 10,
	}
	if err := g.Create(&asset).Error; err != nil {
		t.Fatalf("写入素材失败: %v", err)
	}
	return asset
}

func TestSiteSettingsPersistAndGateRegistration(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r, settings := newSiteRouter(t, g, cfg)
	_, adminToken := createAdminUser(t, g, cfg, "admin-settings@example.com", "adminsettings")

	// 数字设置写入后重新加载不应失败（曾经的 JSONB 扫描问题）。
	w := doAuthJSON(r, http.MethodPatch, "/api/admin/settings", adminToken, map[string]any{
		"announcement":        "站点公告",
		"checkinRewardMicros": 50000,
		"registrationEnabled": false,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("更新设置失败: %d %s", w.Code, w.Body.String())
	}
	if err := settings.Load(t.Context()); err != nil {
		t.Fatalf("重新加载设置失败: %v", err)
	}
	if settings.CheckinRewardMicros() != 50000 {
		t.Fatalf("设置未持久化: %d", settings.CheckinRewardMicros())
	}
	public := doJSON(r, http.MethodGet, "/api/settings/public", nil)
	if decodeBody(t, public)["announcement"] != "站点公告" {
		t.Fatalf("公开设置未返回公告: %s", public.Body.String())
	}
}

func TestCommunityPublishLikeReportAndAdminRemove(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r, _ := newSiteRouter(t, g, cfg)
	author := createUser(t, g, "author@example.com", "author", "password123", true)
	authorToken := accessToken(t, cfg, &author)
	viewer := createUser(t, g, "viewer2@example.com", "viewer2", "password123", true)
	viewerToken := accessToken(t, cfg, &viewer)
	_, adminToken := createAdminUser(t, g, cfg, "admin-community@example.com", "admincommunity")

	asset := seedCommunityAsset(t, g, author.ID)
	w := doAuthJSON(r, http.MethodPost, "/api/community/works", authorToken, map[string]any{
		"assetId": asset.ID.String(), "title": "第一张作品", "description": "来自画布", "tags": "风光, 测试",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("发布失败: %d %s", w.Code, w.Body.String())
	}
	workID := decodeBody(t, w)["work"].(map[string]any)["id"].(string)

	// 点赞幂等，取消点赞回退计数。
	first := doAuthJSON(r, http.MethodPost, "/api/community/works/"+workID+"/like", viewerToken, nil)
	if decodeBody(t, first)["likeCount"] != float64(1) {
		t.Fatalf("点赞失败: %s", first.Body.String())
	}
	second := doAuthJSON(r, http.MethodPost, "/api/community/works/"+workID+"/like", viewerToken, nil)
	if decodeBody(t, second)["likeCount"] != float64(1) {
		t.Fatalf("重复点赞应幂等: %s", second.Body.String())
	}
	unlike := doAuthJSON(r, http.MethodPost, "/api/community/works/"+workID+"/unlike", viewerToken, nil)
	unliked := decodeBody(t, unlike)
	if unliked["likeCount"] != float64(0) || unliked["liked"] != false {
		t.Fatalf("取消点赞失败: %s", unlike.Body.String())
	}

	// 举报一次成功，重复举报冲突；管理员采纳并下架后前台不可见。
	report := doAuthJSON(r, http.MethodPost, "/api/community/works/"+workID+"/report", viewerToken, map[string]any{"reason": "测试举报"})
	if report.Code != http.StatusCreated {
		t.Fatalf("举报失败: %d %s", report.Code, report.Body.String())
	}
	again := doAuthJSON(r, http.MethodPost, "/api/community/works/"+workID+"/report", viewerToken, map[string]any{"reason": "重复"})
	if again.Code != http.StatusConflict {
		t.Fatalf("重复举报应 409, got %d", again.Code)
	}

	reports := doAuthJSON(r, http.MethodGet, "/api/admin/community/reports?status=pending", adminToken, nil)
	items := decodeItems(t, reports)
	if len(items) != 1 {
		t.Fatalf("应有一条待处理举报: %v", items)
	}
	handle := doAuthJSON(r, http.MethodPatch, "/api/admin/community/reports/"+items[0]["id"].(string), adminToken, map[string]any{
		"status": "handled", "removeWork": true,
	})
	if handle.Code != http.StatusOK {
		t.Fatalf("处理举报失败: %d %s", handle.Code, handle.Body.String())
	}
	list := doAuthJSON(r, http.MethodGet, "/api/community/works?size=10", viewerToken, nil)
	if len(decodeItems(t, list)) != 0 {
		t.Fatalf("下架作品不应出现在前台列表")
	}
	detail := doAuthJSON(r, http.MethodGet, "/api/community/works/"+workID, viewerToken, nil)
	if detail.Code != http.StatusNotFound {
		t.Fatalf("下架作品详情应 404, got %d", detail.Code)
	}
}

func TestActivityCheckinAndInviteGrantedOnly(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r, settings := newSiteRouter(t, g, cfg)
	if err := settings.Set(t.Context(), service.SettingCheckinRewardMicros, 30000, nil); err != nil {
		t.Fatalf("写入签到奖励失败: %v", err)
	}
	if err := settings.Set(t.Context(), service.SettingInviteRewardMicros, 70000, nil); err != nil {
		t.Fatalf("写入邀请奖励失败: %v", err)
	}
	if err := settings.Set(t.Context(), service.SettingInviteeRewardMicros, 20000, nil); err != nil {
		t.Fatalf("写入受邀奖励失败: %v", err)
	}

	inviter := createUser(t, g, "inviter@example.com", "inviter", "password123", true)
	inviterToken := accessToken(t, cfg, &inviter)
	invitee := createUser(t, g, "invitee@example.com", "invitee", "password123", true)
	// 邀请防刷要求被邀请人注册满 inviteMinAccountAge，测试账号回拨注册时间。
	if err := g.Model(&model.User{}).Where("id = ?", invitee.ID).
		Update("created_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("回拨邀请账号注册时间失败: %v", err)
	}
	inviteeToken := accessToken(t, cfg, &invitee)

	// 签到：首次发放，重复幂等。
	first := doAuthJSON(r, http.MethodPost, "/api/activity/checkin", inviterToken, nil)
	if decodeBody(t, first)["granted"] != true {
		t.Fatalf("首次签到应发放: %s", first.Body.String())
	}
	second := doAuthJSON(r, http.MethodPost, "/api/activity/checkin", inviterToken, nil)
	if decodeBody(t, second)["granted"] != false {
		t.Fatalf("重复签到不应再发放: %s", second.Body.String())
	}
	balance, _ := service.NewCreditService(g).Balance(t.Context(), inviter.ID)
	if balance.GrantedMicros != 30000 || balance.PurchasedMicros != 0 {
		t.Fatalf("签到奖励只应进入赠送桶: %+v", balance)
	}

	// 邀请码懒生成 + 绑定发放双方奖励，且不产生付费身份。
	info := doAuthJSON(r, http.MethodGet, "/api/activity/invite", inviterToken, nil)
	code, _ := decodeBody(t, info)["code"].(string)
	if code == "" {
		t.Fatalf("应生成邀请码: %s", info.Body.String())
	}
	bind := doAuthJSON(r, http.MethodPost, "/api/activity/invite/bind", inviteeToken, map[string]any{"code": code})
	if bind.Code != http.StatusOK {
		t.Fatalf("绑定邀请失败: %d %s", bind.Code, bind.Body.String())
	}
	repeat := doAuthJSON(r, http.MethodPost, "/api/activity/invite/bind", inviteeToken, map[string]any{"code": code})
	if repeat.Code != http.StatusConflict {
		t.Fatalf("重复绑定应 409, got %d", repeat.Code)
	}
	inviterBalance, _ := service.NewCreditService(g).Balance(t.Context(), inviter.ID)
	inviteeBalance, _ := service.NewCreditService(g).Balance(t.Context(), invitee.ID)
	if inviterBalance.GrantedMicros != 100000 || inviteeBalance.GrantedMicros != 20000 {
		t.Fatalf("邀请奖励金额错误: inviter=%d invitee=%d", inviterBalance.GrantedMicros, inviteeBalance.GrantedMicros)
	}
	me := doAuthJSON(r, http.MethodGet, "/api/me", inviteeToken, nil)
	if decodeBody(t, me)["plan"].(map[string]any)["id"] != "free" {
		t.Fatalf("赠送奖励不应产生付费身份")
	}
}

func TestAdminManagementGuards(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r, _ := newSiteRouter(t, g, cfg)
	admin, adminToken := createAdminUser(t, g, cfg, "admin-guard@example.com", "adminguard")
	target := createUser(t, g, "target-admin@example.com", "targetadmin", "password123", true)
	normal := createUser(t, g, "normal@example.com", "normaluser2", "password123", true)
	normalToken := accessToken(t, cfg, &normal)

	// 非管理员访问管理接口一律 403。
	for _, path := range []string{"/api/admin/settings", "/api/admin/admins", "/api/admin/audit-logs", "/api/admin/stats/revenue"} {
		w := doAuthJSON(r, http.MethodGet, path, normalToken, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("非管理员访问 %s 应 403, got %d", path, w.Code)
		}
	}
	// 提升已有用户成功，重复提升冲突。
	add := doAuthJSON(r, http.MethodPost, "/api/admin/admins", adminToken, map[string]any{"email": target.Email})
	if add.Code != http.StatusOK {
		t.Fatalf("提升管理员失败: %d %s", add.Code, add.Body.String())
	}
	repeat := doAuthJSON(r, http.MethodPost, "/api/admin/admins", adminToken, map[string]any{"email": target.Email})
	if repeat.Code != http.StatusConflict {
		t.Fatalf("重复提升应 409, got %d", repeat.Code)
	}
	// 不能撤销自己。
	self := doAuthJSON(r, http.MethodDelete, "/api/admin/admins/"+admin.ID.String(), adminToken, nil)
	if self.Code != http.StatusBadRequest {
		t.Fatalf("撤销自己应 400, got %d", self.Code)
	}
	// 可以撤销另一位管理员。
	remove := doAuthJSON(r, http.MethodDelete, "/api/admin/admins/"+target.ID.String(), adminToken, nil)
	if remove.Code != http.StatusNoContent {
		t.Fatalf("撤销管理员失败: %d %s", remove.Code, remove.Body.String())
	}
	var reloaded model.User
	g.First(&reloaded, "id = ?", target.ID)
	if reloaded.Role != "user" {
		t.Fatalf("撤销后角色应为 user, got %s", reloaded.Role)
	}
}
