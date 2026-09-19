package ai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/testutil"
)

// TestBeginRequestWritesDimsAndDuplicateKeepsThem 验证分析维度随首次创建写入，
// 幂等命中既有请求时不得覆盖已有值。
func TestBeginRequestWritesDimsAndDuplicateKeepsThem(t *testing.T) {
	g := testutil.NewTestDB(t)
	h := NewAIHandler(g, nil, nil, nil, testutil.NewFakeStorage("local"), "", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/ai/images", nil)
	user := testutil.CreateUser(t, g, "dims@example.com", "dims", "password123", true)
	catalogItem := model.ModelCatalog{ID: uuid.New(), Name: "dims-model", Capability: "image", Enabled: true}
	payload := service.QuotePayload{BillingMode: service.BillingModeCredits, FinalCostMicros: 0}

	if _, _, ok := h.beginRequest(c, user, catalogItem, "image", payload, "dims-key-1", requestDims{
		SessionID: "  session-abc  ",
		Params:    map[string]string{"size": "1024x1024"},
		Spec:      "1024x1024",
	}); !ok {
		t.Fatal("首次 beginRequest 应成功")
	}
	var first model.AIRequest
	if err := g.Where("idempotency_key = ?", "dims-key-1").First(&first).Error; err != nil {
		t.Fatalf("读取请求失败: %v", err)
	}
	if first.SessionID == nil || *first.SessionID != "session-abc" {
		t.Fatalf("会话标识应去空白后写入, got %v", first.SessionID)
	}
	if first.ParamSpec != "1024x1024" {
		t.Fatalf("主规格串应为 1024x1024, got %s", first.ParamSpec)
	}
	if len(first.Params) == 0 || !strings.Contains(string(first.Params), "1024x1024") {
		t.Fatalf("参数快照应包含 size, got %s", string(first.Params))
	}
	wantDate := time.Now().In(model.StatZone).Format(model.StatDateFormat)
	if first.StatDate != wantDate {
		t.Fatalf("统计日期应为 UTC+8 今日 %s, got %s", wantDate, first.StatDate)
	}

	if _, _, ok := h.beginRequest(c, user, catalogItem, "image", payload, "dims-key-1", requestDims{
		SessionID: "session-other",
		Params:    map[string]string{"size": "512x512"},
		Spec:      "512x512",
	}); ok {
		t.Fatal("重复幂等键不应创建新请求")
	}
	var reloaded model.AIRequest
	if err := g.First(&reloaded, "id = ?", first.ID).Error; err != nil {
		t.Fatalf("重读请求失败: %v", err)
	}
	if reloaded.SessionID == nil || *reloaded.SessionID != "session-abc" {
		t.Fatalf("重复提交不得覆盖会话标识, got %v", reloaded.SessionID)
	}
	if reloaded.ParamSpec != "1024x1024" {
		t.Fatalf("重复提交不得覆盖主规格串, got %s", reloaded.ParamSpec)
	}
}

func TestSessionIDOfSanitizes(t *testing.T) {
	if sessionIDOf("   ") != nil {
		t.Fatal("空白会话标识应返回 nil")
	}
	long := strings.Repeat("字", 70)
	got := sessionIDOf(long)
	if got == nil || len([]rune(*got)) != 64 {
		t.Fatalf("超长会话标识应按字符截断到 64, got %v", got)
	}
	got = sessionIDOf(" s1 ")
	if got == nil || *got != "s1" {
		t.Fatalf("会话标识应去首尾空白, got %v", got)
	}
}
