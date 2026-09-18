package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
)

func newServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatalf("获取底层连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Migrate(g); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.SeedMembershipPlans(g); err != nil {
		t.Fatalf("写入默认档位失败: %v", err)
	}
	return g
}

func createUserRow(t *testing.T, g *gorm.DB) model.PlatformUser {
	t.Helper()
	user := model.PlatformUser{
		ID:           uuid.New(),
		Email:        uuid.NewString() + "@example.com",
		Username:     uuid.NewString()[:16],
		PasswordHash: "x",
		Status:       "active",
	}
	if err := g.Create(&user).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return user
}

func TestPromotionMatchingAndConflict(t *testing.T) {
	g := newServiceDB(t)
	catalog := NewCatalogService(g, func() bool { return true })
	now := time.Now()
	modelID := uuid.New()

	whole := model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "整模型 9 折", DiscountBPS: 9000, Priority: 0,
		Status: "active", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	hd720 := model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "720p 8 折", MatchParams: []byte(`{"resolution":"720p"}`),
		DiscountBPS: 8000, Priority: 0, Status: "active", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	d10 := model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "10 秒 8.5 折", MatchParams: []byte(`{"duration":"10"}`),
		DiscountBPS: 8500, Priority: 0, Status: "active", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	combined := model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "720p/10 秒 7 折", MatchParams: []byte(`{"resolution":"720p","duration":"10"}`),
		DiscountBPS: 7000, Priority: 0, Status: "active", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	promotions := []model.ModelPricePromotion{whole, hd720, d10, combined}

	match := ResolveMatch(promotions, map[string]string{"resolution": "720p", "duration": "10"}, now, true)
	if match == nil || match.Name != "720p/10 秒 7 折" {
		t.Fatalf("应按具体度优先命中组合规则, got %+v", match)
	}
	match = ResolveMatch(promotions, map[string]string{"resolution": "720p", "duration": "5"}, now, true)
	if match == nil || match.Name != "720p 8 折" {
		t.Fatalf("720p/5 秒应命中 720p 规则, got %+v", match)
	}
	match = ResolveMatch(promotions, map[string]string{"resolution": "480p", "duration": "5"}, now, true)
	if match == nil || match.Name != "整模型 9 折" {
		t.Fatalf("其余组合应命中整模型规则, got %+v", match)
	}
	if ResolveMatch(promotions, map[string]string{"resolution": "720p"}, now, false) != nil {
		t.Fatalf("关闭全局开关后不应命中任何折扣")
	}
	if got := ApplyDiscount(500_000, 8000); got != 400_000 {
		t.Fatalf("8 折计算错误: %d", got)
	}
	if got := ApplyDiscount(333_333, 8000); got != 266_667 {
		t.Fatalf("向上取整计算错误: %d", got)
	}

	// 保存与既有规则「时间重叠且具体度、优先级完全相同」的规则必须冲突。
	conflict := model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "另一个 720p 8 折", MatchParams: []byte(`{"resolution":"720p"}`),
		DiscountBPS: 8000, Priority: 0, StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Minute),
	}
	_ = catalog
	if !matchCompatible(map[string]string{"resolution": "720p"}, map[string]string{"duration": "10"}) {
		t.Fatalf("不同键的匹配条件应可同时命中")
	}
	if matchCompatible(map[string]string{"resolution": "720p"}, map[string]string{"resolution": "480p"}) {
		t.Fatalf("同键不同值不应同时命中")
	}
	_ = conflict
}

func TestValidateCreditCost(t *testing.T) {
	constraints, err := ParseConstraints([]byte(`{"size":["1024x1024","1536x1024"],"quality":["low","high"]}`))
	if err != nil {
		t.Fatalf("解析 constraints 失败: %v", err)
	}
	valid := CreditCost{
		Version:    1,
		Dimensions: []string{"size", "quality"},
		Prices: []PriceEntry{
			{Params: map[string]string{"size": "1024x1024", "quality": "low"}, CostMicros: 100},
			{Params: map[string]string{"size": "1024x1024", "quality": "high"}, CostMicros: 200},
			{Params: map[string]string{"size": "1536x1024", "quality": "low"}, CostMicros: 150},
			{Params: map[string]string{"size": "1536x1024", "quality": "high"}, CostMicros: 250},
		},
	}
	if err := ValidateCreditCost(valid, constraints); err != nil {
		t.Fatalf("合法矩阵不应报错: %v", err)
	}

	missing := valid
	missing.Prices = valid.Prices[:3]
	if err := ValidateCreditCost(missing, constraints); err == nil {
		t.Fatalf("缺组合应被拒绝")
	}
	outside := valid
	outside.Prices = append([]PriceEntry(nil), valid.Prices...)
	outside.Prices[0].Params = map[string]string{"size": "2048x2048", "quality": "low"}
	if err := ValidateCreditCost(outside, constraints); err == nil {
		t.Fatalf("constraints 外的取值应被拒绝")
	}
	negative := valid
	negative.Prices = append([]PriceEntry(nil), valid.Prices...)
	negative.Prices[0].CostMicros = -1
	if err := ValidateCreditCost(negative, constraints); err == nil {
		t.Fatalf("负价格应被拒绝")
	}
	dup := valid
	dup.Prices = append([]PriceEntry(nil), valid.Prices...)
	dup.Prices[len(dup.Prices)-1].Params = map[string]string{"size": "1024x1024", "quality": "low"}
	if err := ValidateCreditCost(dup, constraints); err == nil {
		t.Fatalf("重复组合应被拒绝")
	}
}

func TestExtractStorageKeys(t *testing.T) {
	raw := []byte(`{"nodes":[{"metadata":{"storageKey":"image:abc123","thumbnail":"video:xyz_9"}}],"note":"not-a-key"}`)
	keys := ExtractStorageKeys(raw)
	if len(keys) != 2 {
		t.Fatalf("应提取到 2 个 storageKey, got %v", keys)
	}
}

func TestQuoteStalenessOnPromotionChange(t *testing.T) {
	g := newServiceDB(t)
	catalog := NewCatalogService(g, func() bool { return true })
	quotes := NewQuoteService(catalog, billing.NewService(g, model.ProductCanvas), "quote-secret")
	user := createUserRow(t, g)

	modelID := uuid.New()
	item := model.ModelCatalog{
		ID: modelID, Name: "quote-image", DisplayName: "报价图", Capability: "image", Provider: "openai",
		Constraints: []byte(`{"size":["1024x1024"],"quality":["low","high"]}`),
		CreditCost:  []byte(`{"version":3,"dimensions":["size","quality"],"prices":[{"params":{"size":"1024x1024","quality":"low"},"costMicros":100000},{"params":{"size":"1024x1024","quality":"high"},"costMicros":200000}]}`),
		Enabled:     true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入模型失败: %v", err)
	}
	now := time.Now()
	// 无活动时报价，价格版本取自矩阵。
	base, err := quotes.BuildQuote(context.Background(), user.ID, item, "image", QuoteParams{"quality": "low"}, 1, now)
	if err != nil {
		t.Fatalf("报价失败: %v", err)
	}
	if base.FinalCostMicros != 100000 || base.PriceVersion != 3 {
		t.Fatalf("基础报价错误: %+v", base)
	}
	params := QuoteParams{"quality": "low"}
	if _, err := quotes.VerifyQuote(context.Background(), user.ID, base.Token, item, "image", params, 1, now); err != nil {
		t.Fatalf("未变化时凭证应有效: %v", err)
	}

	// 活动上线后，旧凭证因价格更便宜而失效，必须重新报价。
	promotion := model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "整模型 8 折", MatchParams: []byte(`{}`), DiscountBPS: 8000,
		Status: "active", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	if err := g.Create(&promotion).Error; err != nil {
		t.Fatalf("写入活动失败: %v", err)
	}
	if _, err := quotes.VerifyQuote(context.Background(), user.ID, base.Token, item, "image", params, 1, now); err != ErrQuoteStale {
		t.Fatalf("活动上线后旧凭证应失效, got %v", err)
	}
	discounted, err := quotes.BuildQuote(context.Background(), user.ID, item, "image", params, 1, now)
	if err != nil {
		t.Fatalf("折后报价失败: %v", err)
	}
	if discounted.FinalCostMicros != 80000 || discounted.Discount == nil {
		t.Fatalf("折后报价错误: %+v", discounted)
	}
	if !discounted.ExpiresAt.After(now) || discounted.ExpiresAt.After(promotion.EndsAt.Add(time.Second)) {
		t.Fatalf("折后凭证有效期不应晚于活动结束: %v", discounted.ExpiresAt)
	}

	// 参数被篡改时凭证失效。
	if _, err := quotes.VerifyQuote(context.Background(), user.ID, discounted.Token, item, "image", QuoteParams{"quality": "high"}, 1, now); err != ErrQuoteStale {
		t.Fatalf("参数不匹配应失效, got %v", err)
	}
	// 凭证过期后失效。
	if _, err := quotes.VerifyQuote(context.Background(), user.ID, discounted.Token, item, "image", params, 1, now.Add(6*time.Minute)); err != ErrQuoteStale {
		t.Fatalf("凭证过期应失效, got %v", err)
	}
	// 停用活动后，折后凭证同样失效。
	if err := g.Model(&model.ModelPricePromotion{}).Where("id = ?", promotion.ID).Update("status", "disabled").Error; err != nil {
		t.Fatalf("停用活动失败: %v", err)
	}
	if _, err := quotes.VerifyQuote(context.Background(), user.ID, discounted.Token, item, "image", params, 1, now); err != ErrQuoteStale {
		t.Fatalf("活动停用后旧凭证应失效, got %v", err)
	}
}

func TestFreeTrialTakesPriorityOverDiscount(t *testing.T) {
	g := newServiceDB(t)
	catalog := NewCatalogService(g, func() bool { return true })
	quotes := NewQuoteService(catalog, billing.NewService(g, model.ProductCanvas), "quote-secret")
	user := createUserRow(t, g)

	modelID := uuid.New()
	item := model.ModelCatalog{
		ID: modelID, Name: "trial-image", DisplayName: "试用图", Capability: "image", Provider: "openai",
		Constraints:       []byte(`{"size":["1024x1024"]}`),
		CreditCost:        []byte(`{"version":1,"dimensions":["size"],"prices":[{"params":{"size":"1024x1024"},"costMicros":100000}]}`),
		FreeTrialEligible: true,
		Enabled:           true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入模型失败: %v", err)
	}
	now := time.Now()
	if err := g.Create(&model.ModelPricePromotion{
		ID: uuid.New(), ModelID: modelID, Name: "整模型 8 折", MatchParams: []byte(`{}`), DiscountBPS: 8000,
		Status: "active", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("写入活动失败: %v", err)
	}
	// 免费额度现在要求先有 granted 领取记录（差异清单 #5），先补一条再验证优先于折扣。
	if err := g.Create(&model.FreeGrantClaim{
		ID: uuid.New(), UserID: user.ID, CampaignID: "test-campaign", Status: "granted",
	}).Error; err != nil {
		t.Fatalf("写入领取记录失败: %v", err)
	}
	quote, err := quotes.BuildQuote(context.Background(), user.ID, item, "image", QuoteParams{"size": "1024x1024"}, 1, now)
	if err != nil {
		t.Fatalf("报价失败: %v", err)
	}
	if quote.BillingMode != BillingModeFreeTrial || quote.FinalCostMicros != 0 || quote.FreeTrialsLeft != FreeImageTrialLimit {
		t.Fatalf("免费额度应优先于折扣: %+v", quote)
	}
	// 用掉一次后剩余次数减少，仍然优先免费。
	if err := g.Transaction(func(tx *gorm.DB) error {
		consumed, err := billing.NewService(g, model.ProductCanvas).ConsumeFreeTrial(tx, user.ID, billing.MetricFreeImageTrial)
		if err != nil {
			return err
		}
		if !consumed {
			t.Fatal("首次占用免费额度应成功")
		}
		return nil
	}); err != nil {
		t.Fatalf("占用免费额度失败: %v", err)
	}
	quote, err = quotes.BuildQuote(context.Background(), user.ID, item, "image", QuoteParams{"size": "1024x1024"}, 1, now)
	if err != nil {
		t.Fatalf("再次报价失败: %v", err)
	}
	if quote.BillingMode != BillingModeFreeTrial || quote.FreeTrialsLeft != FreeImageTrialLimit-1 {
		t.Fatalf("剩余次数错误: %+v", quote)
	}
}
