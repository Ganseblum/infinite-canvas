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

// newFeedbackConsoleRouter 注册反馈工单台与生成反馈的只读/处理路由，与生产中间件链一致。
func newFeedbackConsoleRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	active := middleware.RequireActiveUser(identity.NewService(g))
	adminH := NewAdminHandler(g, cfg, testutil.NewFakeStorage("local"))
	admin := r.Group("/api/admin", middleware.Auth(secret), active, middleware.LoadAdminAccess(identity.NewService(g), g))
	admin.GET("/feedback/tickets", middleware.RequirePermission(authz.PermFeedbackRead), adminH.ListFeedbackTickets)
	admin.GET("/feedback/tickets/:id", middleware.RequirePermission(authz.PermFeedbackRead), adminH.GetFeedbackTicket)
	admin.POST("/feedback/tickets/:id/replies", middleware.RequirePermission(authz.PermFeedbackWrite), adminH.ReplyFeedbackTicket)
	admin.PATCH("/feedback/tickets/:id/status", middleware.RequirePermission(authz.PermFeedbackWrite), adminH.UpdateFeedbackTicketStatus)
	admin.GET("/feedback/generations", middleware.RequirePermission(authz.PermFeedbackRead), adminH.ListGenerationFeedbacks)
	return r
}

func seedFeedbackTicket(t *testing.T, g *gorm.DB, userID uuid.UUID) model.FeedbackTicket {
	t.Helper()
	ticket := model.FeedbackTicket{
		ID: uuid.New(), UserID: userID, Category: "quality", Status: "open",
		Content: "生成效果很差", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := g.Create(&ticket).Error; err != nil {
		t.Fatalf("写入工单失败: %v", err)
	}
	return ticket
}

func TestFeedbackConsolePermissionAndLifecycle(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newFeedbackConsoleRouter(t, g, cfg)

	// support 系统角色由启动同步种子，给操作者任命 support 角色即可进工单台。
	if err := authz.Sync(g); err != nil {
		t.Fatalf("authz 同步失败: %v", err)
	}
	support := testutil.CreateUser(t, g, "supportop@example.com", "supportop", "password123", true)
	supportKey := authz.SupportRoleKey
	if err := g.Model(&model.PlatformUser{}).Where("id = ?", support.ID).Update("role_key", supportKey).Error; err != nil {
		t.Fatalf("任命客服角色失败: %v", err)
	}
	supportToken := testutil.AccessToken(t, cfg, &support)

	// 无后台角色的用户一律 403。
	plain := testutil.CreateUser(t, g, "plainop@example.com", "plainop", "password123", true)
	plainToken := testutil.AccessToken(t, cfg, &plain)
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/feedback/tickets", plainToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无角色访问工单台应 403, got %d", w.Code)
	}

	// 用户提交工单（直接写库）→ support 角色可见、可回复、可置状态。
	user := testutil.CreateUser(t, g, "ticketop@example.com", "ticketop", "password123", true)
	ticket := seedFeedbackTicket(t, g, user.ID)

	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/feedback/tickets?status=open", supportToken, nil)
	if w.Code != http.StatusOK || testutil.DecodeBody(t, w)["total"] != float64(1) {
		t.Fatalf("客服查看工单列表失败: %d %s", w.Code, w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	if items[0]["user"].(map[string]any)["email"] != "ticketop@example.com" {
		t.Fatalf("列表应带提交人信息: %v", items[0])
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/feedback/tickets/"+ticket.ID.String()+"/replies", supportToken, map[string]string{"content": "已收到，将安排处理"})
	if w.Code != http.StatusCreated {
		t.Fatalf("客服回复失败: %d %s", w.Code, w.Body.String())
	}
	var afterReply model.FeedbackTicket
	g.First(&afterReply, "id = ?", ticket.ID)
	if afterReply.ReplyCount != 1 {
		t.Fatalf("客服回复应递增计数, got %d", afterReply.ReplyCount)
	}

	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/feedback/tickets/"+ticket.ID.String()+"/status", supportToken, map[string]string{"status": "resolved"})
	if w.Code != http.StatusOK {
		t.Fatalf("置为已解决失败: %d %s", w.Code, w.Body.String())
	}
	var afterResolve model.FeedbackTicket
	g.First(&afterResolve, "id = ?", ticket.ID)
	if afterResolve.Status != "resolved" || afterResolve.ResolvedAt == nil {
		t.Fatalf("解决状态与时间应落库: %+v", afterResolve)
	}

	// 非法状态 400。
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/feedback/tickets/"+ticket.ID.String()+"/status", supportToken, map[string]string{"status": "bogus"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法状态应 400, got %d", w.Code)
	}
}

func TestGenerationFeedbackConsoleList(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newFeedbackConsoleRouter(t, g, cfg)
	if err := authz.Sync(g); err != nil {
		t.Fatalf("authz 同步失败: %v", err)
	}
	support := testutil.CreateUser(t, g, "supportfb@example.com", "supportfb", "password123", true)
	if err := g.Model(&model.PlatformUser{}).Where("id = ?", support.ID).Update("role_key", authz.SupportRoleKey).Error; err != nil {
		t.Fatalf("任命客服角色失败: %v", err)
	}
	token := testutil.AccessToken(t, cfg, &support)

	user := testutil.CreateUser(t, g, "genfb@example.com", "genfb", "password123", true)
	feedback := model.GenerationFeedback{
		ID: uuid.New(), UserID: user.ID, GenerationID: uuid.New(),
		Rating: -1, Labels: "生成效果差", Note: "完全不像提示词", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := g.Create(&feedback).Error; err != nil {
		t.Fatalf("写入生成反馈失败: %v", err)
	}

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/feedback/generations?rating=-1", token, nil)
	if w.Code != http.StatusOK || testutil.DecodeBody(t, w)["total"] != float64(1) {
		t.Fatalf("生成反馈列表失败: %d %s", w.Code, w.Body.String())
	}
	item := testutil.DecodeItems(t, w)[0]
	if item["rating"] != float64(-1) || item["user"].(map[string]any)["email"] != "genfb@example.com" {
		t.Fatalf("生成反馈载荷错误: %v", item)
	}
}
