package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/testutil"
)

// seedAIRequest 直接写入一条 AIRequest。statDate 由调用方显式指定，
// 空字符串模拟迁移前的历史行（不参与统计）。
func seedAIRequest(t *testing.T, g *gorm.DB, userID uuid.UUID, capability, modelName, status string, cost int64, sessionID *string, params map[string]string, spec, statDate string) {
	t.Helper()
	request := model.AIRequest{
		ID:              uuid.New(),
		UserID:          userID,
		Capability:      capability,
		Model:           modelName,
		FinalCostMicros: cost,
		Status:          status,
		SessionID:       sessionID,
		ParamSpec:       spec,
		StatDate:        statDate,
	}
	if len(params) > 0 {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("序列化参数快照失败: %v", err)
		}
		request.Params = datatypes.JSON(raw)
	}
	if err := g.Create(&request).Error; err != nil {
		t.Fatalf("写入 AIRequest 失败: %v", err)
	}
}

// statDateOf 返回 UTC+8 日界下往前 offsetDays 天的统计日期。
func statDateOf(offsetDays int) string {
	return time.Now().In(model.StatZone).AddDate(0, 0, -offsetDays).Format(model.StatDateFormat)
}

func newUsageAnalyticsRouter(t *testing.T, g *gorm.DB) *gin.Engine {
	t.Helper()
	r := gin.New()
	h := NewAdminHandler(g, testutil.TestConfig(), testutil.NewFakeStorage("local"))
	r.GET("/api/admin/analytics/usage", h.UsageAnalytics)
	return r
}

func numField(t *testing.T, obj map[string]any, key string) float64 {
	t.Helper()
	value, ok := obj[key].(float64)
	if !ok {
		t.Fatalf("字段 %s 缺失或不是数字: %v", key, obj[key])
	}
	return value
}

func objArray(t *testing.T, body map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := body[key].([]any)
	if !ok {
		t.Fatalf("响应缺少 %s 数组: %v", key, body)
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("%s 元素不是对象: %v", key, entry)
		}
		rows = append(rows, item)
	}
	return rows
}

func findRow(t *testing.T, rows []map[string]any, key, want string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row[key] == want {
			return row
		}
	}
	t.Fatalf("未找到 %s=%s 的行: %v", key, want, rows)
	return nil
}

func TestUsageAnalyticsAggregates(t *testing.T) {
	g := testutil.NewTestDB(t)
	r := newUsageAnalyticsRouter(t, g)
	userA := testutil.CreateUser(t, g, "usage-a@example.com", "usagea", "password123", true)
	userB := testutil.CreateUser(t, g, "usage-b@example.com", "usageb", "password123", true)

	s1, s2, s3 := "session-1", "session-2", "session-3"
	today := statDateOf(0)
	seedAIRequest(t, g, userA.ID, "image", "model-a", "succeeded", 1000, &s1, map[string]string{"size": "1024x1024", "quality": "high"}, "1024x1024", today)
	seedAIRequest(t, g, userA.ID, "image", "model-a", "succeeded", 2000, &s1, map[string]string{"size": "512x512"}, "512x512", today)
	// 失败请求的预扣会被全额退回，消费口径按 0 计。
	seedAIRequest(t, g, userB.ID, "video", "model-b", "failed", 5000, nil, nil, "1080p", today)
	seedAIRequest(t, g, userB.ID, "image", "model-c", "running", 1500, &s2, nil, "", today)
	seedAIRequest(t, g, userA.ID, "text", "model-d", "succeeded", 500, nil, nil, "", statDateOf(2))
	seedAIRequest(t, g, userB.ID, "audio", "model-e", "succeeded", 300, &s3, map[string]string{"voice": "alloy"}, "alloy", statDateOf(10))
	// 30 天窗口之外的行与 stat_date 为空的历史行都不参与统计。
	seedAIRequest(t, g, userA.ID, "image", "model-old", "succeeded", 900, nil, nil, "", statDateOf(40))
	seedAIRequest(t, g, userB.ID, "image", "model-legacy", "succeeded", 700, nil, nil, "", "")

	w := testutil.DoJSON(r, http.MethodGet, "/api/admin/analytics/usage?days=30", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取用量分析失败: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)

	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 summary: %s", w.Body.String())
	}
	if got := numField(t, summary, "requests"); got != 6 {
		t.Fatalf("总请求数应为 6, got %v", summary["requests"])
	}
	if got := numField(t, summary, "succeeded"); got != 4 {
		t.Fatalf("成功数应为 4, got %v", summary["succeeded"])
	}
	if got := numField(t, summary, "failed"); got != 1 {
		t.Fatalf("失败数应为 1, got %v", summary["failed"])
	}
	if got := numField(t, summary, "successRate"); got != 0.8 {
		t.Fatalf("成功率应为 0.8（running 不压低）, got %v", summary["successRate"])
	}
	if got := numField(t, summary, "costMicros"); got != 3800 {
		t.Fatalf("消费应为 3800（失败按 0 计）, got %v", summary["costMicros"])
	}
	if got := numField(t, summary, "activeUsers"); got != 2 {
		t.Fatalf("活跃用户应为 2, got %v", summary["activeUsers"])
	}
	if got := numField(t, summary, "activeSessions"); got != 3 {
		t.Fatalf("活跃会话应为 3（NULL 不计）, got %v", summary["activeSessions"])
	}

	daily := objArray(t, body, "daily")
	if len(daily) != 30 {
		t.Fatalf("daily 应零填充 30 天, got %d", len(daily))
	}
	first := daily[0]
	if first["date"] != statDateOf(29) {
		t.Fatalf("daily 首日应为 %v, got %v", statDateOf(29), first["date"])
	}
	if got := numField(t, first, "requests"); got != 0 {
		t.Fatalf("无数据日期应零填充, got %v", first["requests"])
	}
	emptyDay := daily[26] // 今天往前第 3 天，没有造数
	if got := numField(t, emptyDay, "requests"); got != 0 || numField(t, emptyDay, "costMicros") != 0 {
		t.Fatalf("中间零数据日期应全为 0, got %v", emptyDay)
	}
	todayRow := daily[29]
	if todayRow["date"] != today {
		t.Fatalf("daily 末日应为今天 %s, got %v", today, todayRow["date"])
	}
	if got := numField(t, todayRow, "requests"); got != 4 {
		t.Fatalf("当天请求数应为 4, got %v", todayRow["requests"])
	}
	if got := numField(t, todayRow, "succeeded"); got != 2 || numField(t, todayRow, "failed") != 1 {
		t.Fatalf("当天成功/失败应为 2/1, got %v", todayRow)
	}
	if got := numField(t, todayRow, "costMicros"); got != 3000 {
		t.Fatalf("当天消费应为 3000, got %v", todayRow["costMicros"])
	}
	if got := numField(t, todayRow, "activeUsers"); got != 2 || numField(t, todayRow, "sessions") != 2 {
		t.Fatalf("当天活跃用户/会话应为 2/2, got %v", todayRow)
	}
	day2 := daily[27] // 今天往前第 2 天，只有一条成功文本请求
	if got := numField(t, day2, "requests"); got != 1 || numField(t, day2, "costMicros") != 500 || numField(t, day2, "sessions") != 0 {
		t.Fatalf("第 2 天前应只有 1 条成功请求且无会话, got %v", day2)
	}

	byModel := objArray(t, body, "byModel")
	if len(byModel) != 5 {
		t.Fatalf("byModel 应有 5 个模型, got %d", len(byModel))
	}
	modelA := findRow(t, byModel, "key", "model-a")
	if got := numField(t, modelA, "requests"); got != 2 || numField(t, modelA, "costMicros") != 3000 {
		t.Fatalf("model-a 应为 2 次/3000 微元, got %v", modelA)
	}
	modelB := findRow(t, byModel, "key", "model-b")
	if got := numField(t, modelB, "costMicros"); got != 0 {
		t.Fatalf("失败请求不产生消费, got %v", modelB)
	}

	byCapability := objArray(t, body, "byCapability")
	if len(byCapability) != 4 {
		t.Fatalf("byCapability 应有 4 种能力, got %d", len(byCapability))
	}
	if byCapability[0]["key"] != "image" || numField(t, byCapability[0], "requests") != 3 {
		t.Fatalf("image 应以 3 次排第一, got %v", byCapability[0])
	}

	bySpec := objArray(t, body, "bySpec")
	if len(bySpec) != 4 {
		t.Fatalf("bySpec 应有 4 个规格（空 param_spec 不参与）, got %d", len(bySpec))
	}
	alloy := findRow(t, bySpec, "key", "alloy")
	if alloy["capability"] != "audio" || numField(t, alloy, "costMicros") != 300 {
		t.Fatalf("audio/alloy 应为 300 微元, got %v", alloy)
	}

	topUsers := objArray(t, body, "topUsers")
	if len(topUsers) != 2 {
		t.Fatalf("topUsers 应有 2 个用户, got %d", len(topUsers))
	}
	rowA := findRow(t, topUsers, "email", "usage-a@example.com")
	if rowA["displayName"] != "usagea" {
		t.Fatalf("topUsers 应带昵称, got %v", rowA)
	}
	if got := numField(t, rowA, "requests"); got != 3 || numField(t, rowA, "costMicros") != 3500 {
		t.Fatalf("usage-a 应为 3 次/3500 微元, got %v", rowA)
	}
	rowB := findRow(t, topUsers, "email", "usage-b@example.com")
	if got := numField(t, rowB, "costMicros"); got != 300 {
		t.Fatalf("usage-b 消费应为 300, got %v", rowB)
	}
}

func TestUsageAnalyticsSevenDayWindow(t *testing.T) {
	g := testutil.NewTestDB(t)
	r := newUsageAnalyticsRouter(t, g)
	user := testutil.CreateUser(t, g, "usage-week@example.com", "usageweek", "password123", true)
	seedAIRequest(t, g, user.ID, "image", "model-a", "succeeded", 100, nil, nil, "", statDateOf(0))
	seedAIRequest(t, g, user.ID, "image", "model-b", "succeeded", 200, nil, nil, "", statDateOf(10))

	w := testutil.DoJSON(r, http.MethodGet, "/api/admin/analytics/usage?days=7", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取用量分析失败: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	summary, _ := body["summary"].(map[string]any)
	if got := numField(t, summary, "requests"); got != 1 {
		t.Fatalf("7 天窗口应只含 1 条请求, got %v", summary["requests"])
	}
	if got := numField(t, summary, "costMicros"); got != 100 {
		t.Fatalf("7 天窗口消费应为 100, got %v", summary["costMicros"])
	}
	daily := objArray(t, body, "daily")
	if len(daily) != 7 || daily[0]["date"] != statDateOf(6) {
		t.Fatalf("daily 应为 7 天且首日是 %s, got %d 天/首日 %v", statDateOf(6), len(daily), daily[0]["date"])
	}
}

func TestUsageAnalyticsRejectsInvalidDays(t *testing.T) {
	g := testutil.NewTestDB(t)
	r := newUsageAnalyticsRouter(t, g)
	for _, raw := range []string{"8", "abc", "0", "-7"} {
		w := testutil.DoJSON(r, http.MethodGet, "/api/admin/analytics/usage?days="+raw, nil)
		if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "VALIDATION_FAILED" {
			t.Fatalf("days=%s 应返回 400 VALIDATION_FAILED, got %d %s", raw, w.Code, w.Body.String())
		}
	}
}

func TestUsageAnalyticsByModelTop20(t *testing.T) {
	g := testutil.NewTestDB(t)
	r := newUsageAnalyticsRouter(t, g)
	user := testutil.CreateUser(t, g, "usage-many@example.com", "usagemany", "password123", true)
	for i := 0; i < 25; i++ {
		seedAIRequest(t, g, user.ID, "image", fmt.Sprintf("model-%02d", i), "succeeded", 1, nil, nil, "", statDateOf(0))
	}
	w := testutil.DoJSON(r, http.MethodGet, "/api/admin/analytics/usage?days=30", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读取用量分析失败: %d %s", w.Code, w.Body.String())
	}
	byModel := objArray(t, testutil.DecodeBody(t, w), "byModel")
	if len(byModel) != 20 {
		t.Fatalf("byModel 最多返回 20 条, got %d", len(byModel))
	}
}
