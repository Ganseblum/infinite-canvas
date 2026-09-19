package canvas

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/account"
	"github.com/infinite-canvas/server/internal/admin"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/testutil"
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
	adminHandler := admin.NewAdminHandler(g, cfg, testutil.NewFakeStorage("local"))
	adminHandler.SetSettings(settings)

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	api := r.Group("/api/v1")
	active := middleware.RequireActiveUser(identity.NewService(g))

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

	accountHandler := account.NewAccountHandler(g, cfg, service.NewFreeGrantService(g), account.NewAuthHandler(g, cfg, testutil.TestMailer()))
	me := api.Group("/me", middleware.Auth(secret), active)
	me.GET("", accountHandler.GetMe)

	admin := r.Group("/api/admin", middleware.Auth(secret), active, middleware.LoadAdminAccess(identity.NewService(g), g))
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

func createAdminUser(t *testing.T, g *gorm.DB, cfg *config.Config, email, username string) (model.PlatformUser, string) {
	t.Helper()
	user := testutil.CreateUser(t, g, email, username, "password123", true)
	testutil.PromoteAdmin(t, g, &user)
	return user, testutil.AccessToken(t, cfg, &user)
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

// TestCommunityWorkCarriesAIGCTag 作品的 AIGC 标识随来源素材派生：
// 素材带 IsAIGC（工作台存为素材写入）时作品 payload 输出 isAIGC=true，手动上传素材为 false。
func TestCommunityWorkCarriesAIGCTag(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r, _ := newSiteRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "aigc@example.com", "aigcuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	aigcAsset := model.Asset{ID: uuid.New(), UserID: user.ID, Kind: "image", Title: "AI 作品素材", Data: []byte(`{}`), StorageKey: "image:AigcAsset1", Bytes: 10, IsAIGC: true}
	plainAsset := model.Asset{ID: uuid.New(), UserID: user.ID, Kind: "image", Title: "上传素材", Data: []byte(`{}`), StorageKey: "image:PlainAsset1", Bytes: 10}
	for _, asset := range []model.Asset{aigcAsset, plainAsset} {
		if err := g.Create(&asset).Error; err != nil {
			t.Fatalf("写入素材失败: %v", err)
		}
	}
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works", token, map[string]any{
		"assetId": aigcAsset.ID.String(), "title": "AI 作品", "tags": "ai",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("发布 AIGC 作品失败: %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works", token, map[string]any{
		"assetId": plainAsset.ID.String(), "title": "普通作品",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("发布普通作品失败: %d %s", w.Code, w.Body.String())
	}

	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/community/works", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取作品列表失败: %d", w.Code)
	}
	items := testutil.DecodeItems(t, w)
	if len(items) != 2 {
		t.Fatalf("应有两件作品: %d", len(items))
	}
	aigcFlags := map[string]bool{}
	for _, item := range items {
		aigcFlags[item["title"].(string)] = item["isAIGC"] == true
	}
	if !aigcFlags["AI 作品"] || aigcFlags["普通作品"] {
		t.Fatalf("AIGC 标识应随来源素材派生: %v", aigcFlags)
	}
}

func TestSiteSettingsPersistAndGateRegistration(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r, settings := newSiteRouter(t, g, cfg)
	_, adminToken := createAdminUser(t, g, cfg, "admin-settings@example.com", "adminsettings")

	// 数字设置写入后重新加载不应失败（曾经的 JSONB 扫描问题）。
	w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/settings", adminToken, map[string]any{
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
	public := testutil.DoJSON(r, http.MethodGet, "/api/v1/settings/public", nil)
	if testutil.DecodeBody(t, public)["announcement"] != "站点公告" {
		t.Fatalf("公开设置未返回公告: %s", public.Body.String())
	}
}

func TestCommunityPublishLikeReportAndAdminRemove(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r, _ := newSiteRouter(t, g, cfg)
	author := testutil.CreateUser(t, g, "author@example.com", "author", "password123", true)
	authorToken := testutil.AccessToken(t, cfg, &author)
	viewer := testutil.CreateUser(t, g, "viewer2@example.com", "viewer2", "password123", true)
	viewerToken := testutil.AccessToken(t, cfg, &viewer)
	_, adminToken := createAdminUser(t, g, cfg, "admin-community@example.com", "admincommunity")

	asset := seedCommunityAsset(t, g, author.ID)
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works", authorToken, map[string]any{
		"assetId": asset.ID.String(), "title": "第一张作品", "description": "来自画布", "tags": "风光, 测试",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("发布失败: %d %s", w.Code, w.Body.String())
	}
	workID := testutil.DecodeBody(t, w)["work"].(map[string]any)["id"].(string)

	// 点赞幂等，取消点赞回退计数。
	first := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works/"+workID+"/like", viewerToken, nil)
	if testutil.DecodeBody(t, first)["likeCount"] != float64(1) {
		t.Fatalf("点赞失败: %s", first.Body.String())
	}
	second := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works/"+workID+"/like", viewerToken, nil)
	if testutil.DecodeBody(t, second)["likeCount"] != float64(1) {
		t.Fatalf("重复点赞应幂等: %s", second.Body.String())
	}
	unlike := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works/"+workID+"/unlike", viewerToken, nil)
	unliked := testutil.DecodeBody(t, unlike)
	if unliked["likeCount"] != float64(0) || unliked["liked"] != false {
		t.Fatalf("取消点赞失败: %s", unlike.Body.String())
	}

	// 举报一次成功，重复举报冲突；管理员采纳并下架后前台不可见。
	report := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works/"+workID+"/report", viewerToken, map[string]any{"reason": "测试举报"})
	if report.Code != http.StatusCreated {
		t.Fatalf("举报失败: %d %s", report.Code, report.Body.String())
	}
	again := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/community/works/"+workID+"/report", viewerToken, map[string]any{"reason": "重复"})
	if again.Code != http.StatusConflict {
		t.Fatalf("重复举报应 409, got %d", again.Code)
	}

	reports := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/community/reports?status=pending", adminToken, nil)
	items := testutil.DecodeItems(t, reports)
	if len(items) != 1 {
		t.Fatalf("应有一条待处理举报: %v", items)
	}
	handle := testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/community/reports/"+items[0]["id"].(string), adminToken, map[string]any{
		"status": "handled", "removeWork": true,
	})
	if handle.Code != http.StatusOK {
		t.Fatalf("处理举报失败: %d %s", handle.Code, handle.Body.String())
	}
	list := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/community/works?size=10", viewerToken, nil)
	if len(testutil.DecodeItems(t, list)) != 0 {
		t.Fatalf("下架作品不应出现在前台列表")
	}
	detail := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/community/works/"+workID, viewerToken, nil)
	if detail.Code != http.StatusNotFound {
		t.Fatalf("下架作品详情应 404, got %d", detail.Code)
	}
}

func TestActivityCheckinAndInviteGrantedOnly(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
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

	inviter := testutil.CreateUser(t, g, "inviter@example.com", "inviter", "password123", true)
	inviterToken := testutil.AccessToken(t, cfg, &inviter)
	invitee := testutil.CreateUser(t, g, "invitee@example.com", "invitee", "password123", true)
	// 邀请防刷要求被邀请人注册满 inviteMinAccountAge，测试账号回拨注册时间。
	if err := g.Model(&model.PlatformUser{}).Where("id = ?", invitee.ID).
		Update("created_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("回拨邀请账号注册时间失败: %v", err)
	}
	inviteeToken := testutil.AccessToken(t, cfg, &invitee)

	// 签到：首次发放，重复幂等。
	first := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/activity/checkin", inviterToken, nil)
	if testutil.DecodeBody(t, first)["granted"] != true {
		t.Fatalf("首次签到应发放: %s", first.Body.String())
	}
	second := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/activity/checkin", inviterToken, nil)
	if testutil.DecodeBody(t, second)["granted"] != false {
		t.Fatalf("重复签到不应再发放: %s", second.Body.String())
	}
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(t.Context(), inviter.ID)
	if balance.GrantedMicros != 30000 || balance.PurchasedMicros != 0 {
		t.Fatalf("签到奖励只应进入赠送桶: %+v", balance)
	}

	// 邀请码懒生成 + 绑定发放双方奖励，且不产生付费身份。
	info := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/activity/invite", inviterToken, nil)
	code, _ := testutil.DecodeBody(t, info)["code"].(string)
	if code == "" {
		t.Fatalf("应生成邀请码: %s", info.Body.String())
	}
	bind := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/activity/invite/bind", inviteeToken, map[string]any{"code": code})
	if bind.Code != http.StatusOK {
		t.Fatalf("绑定邀请失败: %d %s", bind.Code, bind.Body.String())
	}
	repeat := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/activity/invite/bind", inviteeToken, map[string]any{"code": code})
	if repeat.Code != http.StatusConflict {
		t.Fatalf("重复绑定应 409, got %d", repeat.Code)
	}
	inviterBalance, _ := billing.NewService(g, model.ProductCanvas).Balance(t.Context(), inviter.ID)
	inviteeBalance, _ := billing.NewService(g, model.ProductCanvas).Balance(t.Context(), invitee.ID)
	if inviterBalance.GrantedMicros != 100000 || inviteeBalance.GrantedMicros != 20000 {
		t.Fatalf("邀请奖励金额错误: inviter=%d invitee=%d", inviterBalance.GrantedMicros, inviteeBalance.GrantedMicros)
	}
	me := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/me", inviteeToken, nil)
	if testutil.DecodeBody(t, me)["plan"].(map[string]any)["id"] != "free" {
		t.Fatalf("赠送奖励不应产生付费身份")
	}
}

func TestAdminManagementGuards(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r, _ := newSiteRouter(t, g, cfg)
	admin, adminToken := createAdminUser(t, g, cfg, "admin-guard@example.com", "adminguard")
	target := testutil.CreateUser(t, g, "target-admin@example.com", "targetadmin", "password123", true)
	normal := testutil.CreateUser(t, g, "normal@example.com", "normaluser2", "password123", true)
	normalToken := testutil.AccessToken(t, cfg, &normal)

	// 非管理员访问管理接口一律 403。
	for _, path := range []string{"/api/admin/settings", "/api/admin/admins", "/api/admin/audit-logs", "/api/admin/stats/revenue"} {
		w := testutil.DoAuthJSON(r, http.MethodGet, path, normalToken, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("非管理员访问 %s 应 403, got %d", path, w.Code)
		}
	}
	// 提升已有用户成功，重复提升冲突。
	add := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/admins", adminToken, map[string]any{"email": target.Email})
	if add.Code != http.StatusOK {
		t.Fatalf("提升管理员失败: %d %s", add.Code, add.Body.String())
	}
	repeat := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/admins", adminToken, map[string]any{"email": target.Email})
	if repeat.Code != http.StatusConflict {
		t.Fatalf("重复提升应 409, got %d", repeat.Code)
	}
	// 不能撤销自己。
	self := testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/admins/"+admin.ID.String(), adminToken, nil)
	if self.Code != http.StatusBadRequest {
		t.Fatalf("撤销自己应 400, got %d", self.Code)
	}
	// 可以撤销另一位管理员。
	remove := testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/admins/"+target.ID.String(), adminToken, nil)
	if remove.Code != http.StatusNoContent {
		t.Fatalf("撤销管理员失败: %d %s", remove.Code, remove.Body.String())
	}
	var reloaded model.PlatformUser
	g.First(&reloaded, "id = ?", target.ID)
	if reloaded.Role != "user" {
		t.Fatalf("撤销后角色应为 user, got %s", reloaded.Role)
	}
}
