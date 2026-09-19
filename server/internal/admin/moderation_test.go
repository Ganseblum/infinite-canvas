package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/ai"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/canvas"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/testutil"
)

// newModerationForTest 组装使用 fake provider 的审核服务。
func newModerationForTest(t *testing.T, g *gorm.DB, stor storage.Storage, provider *moderation.FakeProvider, failMode string) *service.ModerationService {
	t.Helper()
	cipher, err := crypto.New("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	quarantine := service.NewQuarantineService(g, stor, cipher, time.Hour)
	return service.NewModerationService(g, provider, quarantine, service.ModerationConfig{
		Enabled:  true,
		FailMode: failMode,
		Policy:   "test-v1",
		Timeout:  2 * time.Second,
		CacheTTL: time.Minute,
	})
}

func seedModerationModel(t *testing.T, g *gorm.DB, channelID uuid.UUID) model.ModelCatalog {
	t.Helper()
	channelIDs, _ := json.Marshal([]uuid.UUID{channelID})
	item := model.ModelCatalog{
		ID:          uuid.New(),
		Name:        "moderation-image",
		DisplayName: "审核图",
		Capability:  "image",
		Provider:    "openai",
		Constraints: []byte(`{"size":["1024x1024"],"quality":["low"]}`),
		CreditCost:  []byte(`{"version":1,"dimensions":["size","quality"],"prices":[{"params":{"size":"1024x1024","quality":"low"},"costMicros":100000}]}`),
		ChannelIDs:  channelIDs,
		Enabled:     true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入模型失败: %v", err)
	}
	return item
}

func TestModerationRejectsPromptBeforeReserve(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectTexts: []string{"违禁词"}}
	store := testutil.NewFakeStorage("local")
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)

	user := testutil.CreateUser(t, g, "modprompt@example.com", "modprompt", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "包含违禁词的提示", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-prompt-1",
	})
	if w.Code != http.StatusUnprocessableEntity || testutil.ErrorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("违规提示词应 422 CONTENT_REJECTED, got %d %s", w.Code, w.Body.String())
	}
	// 不扣点、无消费流水、没有生成记录。
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("被拒输入不应扣点, 余额=%d", balance.PurchasedMicros)
	}
	var consumes int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, billing.TxTypeConsume).Count(&consumes)
	if consumes != 0 {
		t.Fatalf("被拒输入不应产生消费流水")
	}
	var generations int64
	g.Model(&model.Generation{}).Where("user_id = ?", user.ID).Count(&generations)
	if generations != 0 {
		t.Fatalf("被拒输入不应写生成记录")
	}
	// 审核记录保存哈希与标签，不保存原文。
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "prompt").First(&record).Error; err != nil {
		t.Fatalf("应写入审核记录: %v", err)
	}
	if record.ContentHash == "" || record.Decision != "rejected" || record.ReviewStatus != "pending" {
		t.Fatalf("审核记录字段错误: %+v", record)
	}
	if string(record.ProviderResult) == "包含违禁词的提示" {
		t.Fatalf("审核记录不应保存原始提示词")
	}
}

func TestModerationRejectsUploadWithoutQuota(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	store := testutil.NewFakeStorage("local")
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r := newResourceRouterWithModeration(t, g, cfg, store, moderationService)
	user := testutil.CreateUser(t, g, "modupload@example.com", "modupload", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	w := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:ModUp1", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"), "image/png", token, nil)
	if w.Code != http.StatusUnprocessableEntity || testutil.ErrorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("违规上传应 422 CONTENT_REJECTED, got %d %s", w.Code, w.Body.String())
	}
	// 不写正式记录、不增加用量。
	var count int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 0 {
		t.Fatalf("被拒上传不应写媒体记录")
	}
	used, _, err := platformstorage.NewService(g, model.ProductCanvas).Snapshot(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取存储用量失败: %v", err)
	}
	if used != 0 {
		t.Fatalf("被拒上传不应占用配额, used=%d", used)
	}
	// 隔离原件保留供人工复核。
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "artifact").First(&record).Error; err != nil {
		t.Fatalf("应写入审核记录: %v", err)
	}
	if record.QuarantineKey == "" {
		t.Fatalf("拒绝记录应保留隔离原件")
	}

	// 审核通过后正常上传，同 storageKey 重试幂等。
	provider.RejectLabels = nil
	ok := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:ModUp1", testutil.TestPNG, "image/png", token, nil)
	if ok.Code != http.StatusCreated {
		t.Fatalf("通过审核的上传应 201, got %d %s", ok.Code, ok.Body.String())
	}
	okAgain := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:ModUp1", testutil.TestPNG, "image/png", token, nil)
	if okAgain.Code != http.StatusCreated {
		t.Fatalf("重复上传应 201, got %d", okAgain.Code)
	}
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 1 {
		t.Fatalf("同一 storageKey 只应有一条正式记录, got %d", count)
	}
	used, _, err = platformstorage.NewService(g, model.ProductCanvas).Snapshot(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取存储用量失败: %v", err)
	}
	if used != int64(len(testutil.TestPNG)) {
		t.Fatalf("用量应只计正式对象, got %d", used)
	}
}

func TestModerationFailModeRejectAndAllow(t *testing.T) {
	// reject：审核服务故障返回 503，不预扣。
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{FailWith: errors.New("dial timeout")}
	store := testutil.NewFakeStorage("local")
	r, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store, provider, "reject"))
	user := testutil.CreateUser(t, g, "modfail@example.com", "modfail", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)
	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常内容", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-fail-1",
	})
	if w.Code != http.StatusServiceUnavailable || testutil.ErrorCode(t, w) != "MODERATION_UNAVAILABLE" {
		t.Fatalf("reject 模式下审核故障应 503, got %d %s", w.Code, w.Body.String())
	}
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("审核故障不应预扣, 余额=%d", balance.PurchasedMicros)
	}

	// allow：明确配置后放行，记录为 error 且继续生成。
	provider2 := &moderation.FakeProvider{FailWith: errors.New("dial timeout")}
	store2 := testutil.NewFakeStorage("local")
	provider2.RejectTexts = nil
	r2, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store2, provider2, "allow"))
	quote2 := mustQuote(t, r2, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w2 := testutil.DoAuthJSON(r2, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常内容", "size": "1024x1024", "quality": "low",
		"quoteToken": quote2["quoteToken"], "idempotencyKey": "mod-fail-2",
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("allow 模式下审核故障应放行, got %d %s", w2.Code, w2.Body.String())
	}
	var errorRecords int64
	g.Model(&model.ModerationRecord{}).Where("decision = ?", "error").Count(&errorRecords)
	if errorRecords == 0 {
		t.Fatalf("allow 模式应记录 error 决策")
	}
}

func TestModerationArtifactRejectedKeepsCredits(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	store := testutil.NewFakeStorage("local")
	r, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store, provider, "reject"))
	user := testutil.CreateUser(t, g, "modartifact@example.com", "modartifact", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)
	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常提示词", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-artifact-1",
	})
	if w.Code != http.StatusUnprocessableEntity || testutil.ErrorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("违规产物应 422, got %d %s", w.Code, w.Body.String())
	}
	// 产物拒绝不退点：上游成本已经发生。
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 900_000 {
		t.Fatalf("产物拒绝不应退点, 余额=%d", balance.PurchasedMicros)
	}
	// 产物没有进入正式存储。
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("被拒产物不应进入正式存储")
	}
	// 生成记录标记为拒绝。
	var generation model.Generation
	if err := g.Where("user_id = ? AND kind = ?", user.ID, "image").First(&generation).Error; err != nil {
		t.Fatalf("应写生成记录: %v", err)
	}
	if generation.ModerationStatus != "rejected" {
		t.Fatalf("生成记录审核状态应为 rejected, got %s", generation.ModerationStatus)
	}
}

func TestModerationReviewConflictAndCompensation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectTexts: []string{"违禁"}}
	store := testutil.NewFakeStorage("local")
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, aiHandler := newAITestRouterWithModeration(t, g, cfg, moderationService)

	admin := testutil.CreateUser(t, g, "adminmod@example.com", "adminmod", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)
	user := testutil.CreateUser(t, g, "modreview@example.com", "modreview", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "包含违禁内容", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-review-1",
	})
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "prompt").First(&record).Error; err != nil {
		t.Fatalf("应写审核记录: %v", err)
	}

	// 第一次复核成功。
	adminRouter := r
	_ = aiHandler
	w := testutil.DoAuthJSON(adminRouter, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "误判，人工通过", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("复核应成功: %d %s", w.Code, w.Body.String())
	}
	// 第二个管理员用旧 revision 提交会冲突。
	w = testutil.DoAuthJSON(adminRouter, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "rejected", "note": "覆盖前一结论", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "MODERATION_ALREADY_REVIEWED" {
		t.Fatalf("旧 revision 复核应 409, got %d %s", w.Code, w.Body.String())
	}

	// 补偿进入 granted 桶且幂等。
	w = testutil.DoAuthJSON(adminRouter, http.MethodPost, "/api/admin/moderation/records/"+record.ID.String()+"/compensate", adminToken, map[string]any{
		"amountMicros": 100000, "note": "误判补偿",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("补偿应成功: %d %s", w.Code, w.Body.String())
	}
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 100000 || balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("补偿应进入赠送桶且不改变付费身份: %+v", balance)
	}
	w = testutil.DoAuthJSON(adminRouter, http.MethodPost, "/api/admin/moderation/records/"+record.ID.String()+"/compensate", adminToken, map[string]any{
		"amountMicros": 100000, "note": "重复补偿",
	})
	if w.Code == http.StatusOK {
		t.Fatalf("同一记录重复补偿应被拒绝")
	}
	balanceAfter, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balanceAfter.GrantedMicros != 100000 {
		t.Fatalf("重复补偿不应重复到账, granted=%d", balanceAfter.GrantedMicros)
	}
}

func TestModerationStatsAndNonAdminDenied(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	store := testutil.NewFakeStorage("local")
	provider := &moderation.FakeProvider{}
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)
	user := testutil.CreateUser(t, g, "modstats@example.com", "modstats", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	// 非管理员访问审核接口一律 403。
	for _, path := range []string{"/api/admin/moderation/records", "/api/admin/moderation/stats"} {
		w := testutil.DoAuthJSON(r, http.MethodGet, path, token, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("非管理员访问 %s 应 403, got %d", path, w.Code)
		}
	}
}

func seedPlatformChannel(t *testing.T, g *gorm.DB, baseURL, format string) model.PlatformChannel {
	t.Helper()
	cipher, err := crypto.New("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	nonce, payload, err := cipher.Encrypt([]byte("test-api-key"))
	if err != nil {
		t.Fatalf("加密渠道密钥失败: %v", err)
	}
	channel := model.PlatformChannel{
		ID:        uuid.New(),
		Name:      "测试渠道",
		BaseURL:   baseURL,
		APIFormat: format,
		Nonce:     nonce,
		Payload:   payload,
		Enabled:   true,
	}
	if err := g.Create(&channel).Error; err != nil {
		t.Fatalf("写入渠道失败: %v", err)
	}
	return channel
}

// newAITestRouter 组装第四期路由与真实依赖，上游用注入的假服务器地址。

// newAITestRouterWithModeration 组装 AI 生成与审核链路的测试引擎，与 ai 包同名夹具各自独立。
func newAITestRouterWithModeration(t *testing.T, g *gorm.DB, cfg *config.Config, moderationService *service.ModerationService) (*gin.Engine, *ai.AIHandler) {
	t.Helper()
	cipher, err := crypto.New(cfg.CredentialKey)
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	upstream := service.NewUpstreamService(g, cipher, service.DefaultUpstreamTimeouts())
	upstream.SetAllowPrivate(true) // 测试用 localhost 假上游
	catalog := service.NewCatalogService(g, func() bool { return cfg.PromotionEnabled })
	quotes := service.NewQuoteService(catalog, billing.NewService(g, model.ProductCanvas), cfg.JWTSecret)
	store := testutil.NewFakeStorage("local")
	aiHandler := ai.NewAIHandler(g, catalog, quotes, upstream, store, "http://localhost:3000", moderationService)

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	api := r.Group("/api/v1")
	aiRoutes := api.Group("/ai", middleware.Auth(secret))
	aiRoutes.POST("/quote", aiHandler.Quote)
	aiRoutes.POST("/images/generations", aiHandler.Images)
	aiRoutes.POST("/videos/generations", aiHandler.CreateVideo)
	aiRoutes.GET("/videos/tasks/:id", aiHandler.VideoTask)
	aiRoutes.POST("/audio/speech", aiHandler.Speech)
	aiRoutes.POST("/chat/completions", aiHandler.Chat)

	generationHandler := canvas.NewGenerationHandler(g)
	generations := api.Group("/generations", middleware.Auth(secret))
	generations.GET("", generationHandler.List)
	generations.GET("/:id", generationHandler.Get)
	generations.DELETE("/:id", generationHandler.Delete)

	adminHandler := NewAdminHandlerWithUpstream(g, cfg, store, upstream)
	if moderationService != nil {
		adminHandler.SetModeration(moderationService)
	}
	adminRoutes := r.Group("/api/admin", middleware.Auth(secret), middleware.LoadAdminAccess(identity.NewService(g), g))
	adminRoutes.GET("/moderation/records", middleware.RequirePermission(authz.PermModerationRead), adminHandler.ListModerationRecords)
	adminRoutes.GET("/moderation/records/:id", middleware.RequirePermission(authz.PermModerationRead), adminHandler.GetModerationRecord)
	adminRoutes.GET("/moderation/records/:id/preview", middleware.RequirePermission(authz.PermModerationRead), adminHandler.PreviewModerationArtifact)
	adminRoutes.PATCH("/moderation/records/:id", middleware.RequirePermission(authz.PermModerationReview), adminHandler.ReviewModerationRecord)
	adminRoutes.POST("/moderation/records/:id/compensate", middleware.RequirePermission(authz.PermModerationCompensate), adminHandler.CompensateModeration)
	adminRoutes.GET("/moderation/stats", middleware.RequirePermission(authz.PermModerationRead), adminHandler.ModerationStats)
	return r, aiHandler
}

func seedCredits(t *testing.T, g *gorm.DB, userID uuid.UUID, purchased int64) {
	t.Helper()
	points := billing.NewService(g, model.ProductCanvas)
	if err := points.EnsureAccount(g, userID); err != nil {
		t.Fatalf("建账本行失败: %v", err)
	}
	if purchased == 0 {
		return
	}
	if err := points.Purchase(g, &model.Order{
		ID: uuid.New(), UserID: userID, PurchasedMicros: purchased,
	}, time.Now()); err != nil {
		t.Fatalf("入账失败: %v", err)
	}
}

func mustQuote(t *testing.T, r *gin.Engine, token, modelName, capability string, params map[string]any) map[string]any {
	t.Helper()
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/quote", token, map[string]any{
		"model": modelName, "capability": capability, "params": params,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("获取报价失败: code=%d body=%s", w.Code, w.Body.String())
	}
	return testutil.DecodeBody(t, w)
}
