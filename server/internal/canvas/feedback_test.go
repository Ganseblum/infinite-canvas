package canvas

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/testutil"
)

// newFeedbackRouter 组装用户面反馈工单与生成反馈路由，与 main.go 中间件链一致（限流器除外）。
func newFeedbackRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	active := middleware.RequireActiveUser(identity.NewService(g))
	feedbackH := NewFeedbackHandler(g)
	generationH := NewGenerationHandler(g)
	api := r.Group("/api/v1")
	feedback := api.Group("/feedback", middleware.Auth(secret), active)
	MountFeedbackRoutes(feedback, feedbackH)
	generations := api.Group("/generations", middleware.Auth(secret), active)
	MountGenerationRoutes(generations, generationH)
	return r
}

func seedSuccessGeneration(t *testing.T, g *gorm.DB, userID uuid.UUID) model.Generation {
	t.Helper()
	generation := model.Generation{
		ID: uuid.New(), UserID: userID, Kind: "image", Status: "success",
		Model: "acceptance-image", Prompt: "验收生成",
	}
	if err := g.Create(&generation).Error; err != nil {
		t.Fatalf("写入生成记录失败: %v", err)
	}
	return generation
}

func TestFeedbackTicketLifecycle(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newFeedbackRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "fb@example.com", "fbuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	// 校验：类别白名单与正文长度。
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/feedback", token, map[string]string{"category": "bogus", "content": "反馈内容"})
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "VALIDATION_FAILED" {
		t.Fatalf("非法类别应 400, got %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/feedback", token, map[string]string{"category": "quality", "content": "短"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("过短正文应 400, got %d", w.Code)
	}

	// 创建 → 我的工单 → 详情。
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/feedback", token, map[string]string{"category": "quality", "content": "生成效果很差，希望改进"})
	if w.Code != http.StatusCreated {
		t.Fatalf("创建工单失败: %d %s", w.Code, w.Body.String())
	}
	ticketID := testutil.DecodeBody(t, w)["ticket"].(map[string]any)["id"].(string)

	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/feedback?status=open", token, nil)
	if w.Code != http.StatusOK || testutil.DecodeBody(t, w)["total"] != float64(1) {
		t.Fatalf("我的工单列表错误: %d %s", w.Code, w.Body.String())
	}

	// 客服回复（直接写库模拟）→ 用户看到对话。
	reply := model.FeedbackTicketReply{ID: uuid.New(), TicketID: uuid.MustParse(ticketID), UserID: user.ID, IsStaff: true, Content: "已收到，我们会改进", CreatedAt: time.Now()}
	if err := g.Create(&reply).Error; err != nil {
		t.Fatalf("写入客服回复失败: %v", err)
	}
	if err := g.Model(&model.FeedbackTicket{}).Where("id = ?", ticketID).Update("status", "resolved").Error; err != nil {
		t.Fatalf("置为已解决失败: %v", err)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/feedback/"+ticketID, token, nil)
	body := testutil.DecodeBody(t, w)
	if body["ticket"].(map[string]any)["status"] != "resolved" || len(body["replies"].([]any)) != 1 {
		t.Fatalf("详情应含状态与回复: %v", body)
	}

	// 用户在已解决工单上回复 → 重新打开。
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/feedback/"+ticketID+"/replies", token, map[string]string{"content": "问题还在"})
	if w.Code != http.StatusCreated {
		t.Fatalf("用户回复失败: %d %s", w.Code, w.Body.String())
	}
	var ticket model.FeedbackTicket
	if err := g.First(&ticket, "id = ?", ticketID).Error; err != nil {
		t.Fatalf("读取工单失败: %v", err)
	}
	if ticket.Status != "open" || ticket.ReplyCount != 1 {
		// 客服回复是直接写库的（不走 handler），不递增计数；用户回复 +1。
		t.Fatalf("用户回复应重新打开工单: status=%s replies=%d", ticket.Status, ticket.ReplyCount)
	}

	// 关闭后重复关闭 409；他人访问 404。
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/feedback/"+ticketID+"/close", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("关闭工单失败: %d", w.Code)
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/feedback/"+ticketID+"/close", token, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("重复关闭应 409, got %d", w.Code)
	}
	other := testutil.CreateUser(t, g, "fb2@example.com", "fbuser2", "password123", true)
	otherToken := testutil.AccessToken(t, cfg, &other)
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/feedback/"+ticketID, otherToken, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("他人访问工单应 404, got %d", w.Code)
	}
}

func TestGenerationFeedbackUpsertAndOwnership(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newFeedbackRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "gfb@example.com", "gfbuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)
	generation := seedSuccessGeneration(t, g, user.ID)
	failed := seedSuccessGeneration(t, g, user.ID)
	if err := g.Model(&model.Generation{}).Where("id = ?", failed.ID).Update("status", "failed").Error; err != nil {
		t.Fatalf("置失败失败: %v", err)
	}

	// 非法 rating 400。
	w := testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/generations/"+generation.ID.String()+"/feedback", token, map[string]any{"rating": 0})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 rating 应 400, got %d", w.Code)
	}

	// 点踩带标签与说明；重复提交覆盖。
	body := map[string]any{"rating": -1, "labels": "生成效果差, 不符合提示词, 生成效果差", "note": "猫画成了狗"}
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/generations/"+generation.ID.String()+"/feedback", token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("点踩失败: %d %s", w.Code, w.Body.String())
	}
	first := testutil.DecodeBody(t, w)
	if first["rating"] != float64(-1) || first["labels"].([]any)[0] != "生成效果差" {
		t.Fatalf("点踩载荷错误: %v", first)
	}
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/generations/"+generation.ID.String()+"/feedback", token, map[string]any{"rating": 1})
	if w.Code != http.StatusOK || testutil.DecodeBody(t, w)["rating"] != float64(1) {
		t.Fatalf("重复提交应覆盖为点赞")
	}
	var count int64
	g.Model(&model.GenerationFeedback{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 1 {
		t.Fatalf("同一生成记录只应有一条反馈, got %d", count)
	}

	// 撤销 → 读回为空。
	w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/generations/"+generation.ID.String()+"/feedback", token, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("撤销反馈应 204, got %d", w.Code)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations/"+generation.ID.String()+"/feedback", token, nil)
	if testutil.DecodeBody(t, w)["feedback"] != nil {
		t.Fatalf("撤销后读回应为空")
	}

	// 失败的生成不可反馈；他人的生成 404。
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/generations/"+failed.ID.String()+"/feedback", token, map[string]any{"rating": 1})
	if w.Code != http.StatusNotFound {
		t.Fatalf("失败生成应 404, got %d", w.Code)
	}
	otherGeneration := seedSuccessGeneration(t, g, uuid.New())
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/generations/"+otherGeneration.ID.String()+"/feedback", token, map[string]any{"rating": 1})
	if w.Code != http.StatusNotFound {
		t.Fatalf("他人生成应 404, got %d", w.Code)
	}
}
