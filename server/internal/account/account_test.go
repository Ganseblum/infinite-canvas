package account

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/testutil"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// seedFreeTrialCatalog 播种一个可领取的免费试用目录（1 图 + 1 视频，单价 20 微元）。
// 新版每日预算按「当日已发放领取数 × 单次估算成本」闸门，估算依赖目录里存在
// 免费试用模型；目录为空时估算为 0，闸门按拒绝处理（FREE_GRANT_UNAVAILABLE）。
// 预算用例预算=100：3 图 + 1 视频 × 20 = 80，首个可领、第二个超预算被拒。
func seedFreeTrialCatalog(t *testing.T, g *gorm.DB) {
	t.Helper()
	prices := `{"version":1,"dimensions":[],"prices":[{"params":{},"costMicros":20}]}`
	for _, item := range []model.ModelCatalog{
		{ID: uuid.New(), Name: "trial-image", DisplayName: "试用图片", Capability: "image", Provider: "openai", CreditCost: datatypes.JSON([]byte(prices)), FreeTrialEligible: true, Enabled: true},
		{ID: uuid.New(), Name: "trial-video", DisplayName: "试用视频", Capability: "video", Provider: "openai", CreditCost: datatypes.JSON([]byte(prices)), FreeTrialEligible: true, Enabled: true},
	} {
		if err := g.Create(&item).Error; err != nil {
			t.Fatalf("播种免费试用目录失败: %v", err)
		}
	}
}

func TestFreeGrantClaimIsIdempotent(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAccountRouter(t, g, cfg)
	seedFreeTrialCatalog(t, g)
	user := testutil.CreateUser(t, g, "grant@example.com", "grantuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("首次领取失败: code=%d body=%s", w.Code, w.Body.String())
	}
	var first map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatalf("解析领取响应失败: %v", err)
	}
	if first["status"] != "granted" {
		t.Fatalf("首次领取应返回 granted, got %v", first["status"])
	}

	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("重复领取应返回既有结论, code=%d body=%s", w.Code, w.Body.String())
	}
	var second map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatalf("解析重复领取响应失败: %v", err)
	}
	if second["campaignId"] != first["campaignId"] || second["status"] != first["status"] {
		t.Fatalf("重复领取应返回同一结论: first=%v second=%v", first, second)
	}

	var count int64
	if err := g.Model(&model.FreeGrantClaim{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计领取记录失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("同一活动应只有一条领取记录, got %d", count)
	}
}

func TestFreeGrantRiskThresholdDenies(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAccountRouter(t, g, cfg)
	seedFreeTrialCatalog(t, g)

	first := testutil.CreateUser(t, g, "risk-first@example.com", "riskfirst", "password123", true)
	second := testutil.CreateUser(t, g, "risk-second@example.com", "risksecond", "password123", true)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", testutil.AccessToken(t, cfg, &first), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("首个账号领取应成功: code=%d body=%s", w.Code, w.Body.String())
	}

	// 同一 UA/IP 的第二个账号风险分提高，用可配置阈值触发拒绝
	cfg.FreeGrantRiskThreshold = 30
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", testutil.AccessToken(t, cfg, &second), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("风险评分达到阈值应拒绝, got %d body=%s", w.Code, w.Body.String())
	}
	if code := testutil.ErrorCode(t, w); code != "FREE_GRANT_UNAVAILABLE" {
		t.Fatalf("风控拒绝应返回 FREE_GRANT_UNAVAILABLE, got %s", code)
	}
	var denied model.FreeGrantClaim
	if err := g.Where("user_id = ?", second.ID).First(&denied).Error; err != nil {
		t.Fatalf("查询风控拒绝记录失败: %v", err)
	}
	if denied.Status != "denied" || denied.Reason != "risk_threshold" {
		t.Fatalf("风控拒绝记录不符: status=%s reason=%s", denied.Status, denied.Reason)
	}
}

func TestFreeGrantDailyBudgetDenies(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.FreeGrantDailyBudgetMicros = 100
	r := newAccountRouter(t, g, cfg)
	seedFreeTrialCatalog(t, g)

	first := testutil.CreateUser(t, g, "budget-first@example.com", "budgetfirst", "password123", true)
	second := testutil.CreateUser(t, g, "budget-second@example.com", "budgetsecond", "password123", true)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", testutil.AccessToken(t, cfg, &first), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("预算内首个账号领取应成功: code=%d body=%s", w.Code, w.Body.String())
	}

	// 单次领取消耗固定成本，第二个账号已无当日预算
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", testutil.AccessToken(t, cfg, &second), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("预算耗尽应拒绝, got %d body=%s", w.Code, w.Body.String())
	}
	if code := testutil.ErrorCode(t, w); code != "FREE_GRANT_UNAVAILABLE" {
		t.Fatalf("预算拒绝应返回 FREE_GRANT_UNAVAILABLE, got %s", code)
	}
	var denied model.FreeGrantClaim
	if err := g.Where("user_id = ?", second.ID).First(&denied).Error; err != nil {
		t.Fatalf("查询预算拒绝记录失败: %v", err)
	}
	if denied.Status != "denied" || denied.Reason != "daily_budget" {
		t.Fatalf("预算拒绝记录不符: status=%s reason=%s", denied.Status, denied.Reason)
	}
}

func TestFreeGrantClaimConcurrentUniqueConstraint(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAccountRouter(t, g, cfg)
	seedFreeTrialCatalog(t, g)
	user := testutil.CreateUser(t, g, "grant-race@example.com", "grantrace", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/me/free-grant/claim", token, nil).Code
		}(i)
	}
	wg.Wait()

	for _, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("并发领取应被唯一约束兜底为 200, codes=%v", codes)
		}
	}
	var count int64
	if err := g.Model(&model.FreeGrantClaim{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计领取记录失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("并发领取只应产生一条记录, got %d", count)
	}
}
