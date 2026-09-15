package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
	"gorm.io/datatypes"
)

// seedPlatformChannel 插入一条加密的平台渠道，指向给定的假上游地址。
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
func newAITestRouter(t *testing.T, g *gorm.DB, cfg *config.Config) (*gin.Engine, *AIHandler) {
	return newAITestRouterWithModeration(t, g, cfg, nil)
}

// newAITestRouterWithModeration 允许注入审核服务，用于第五期链路测试。
func newAITestRouterWithModeration(t *testing.T, g *gorm.DB, cfg *config.Config, moderationService *service.ModerationService) (*gin.Engine, *AIHandler) {
	t.Helper()
	cipher, err := crypto.New(cfg.CredentialKey)
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	upstream := service.NewUpstreamService(g, cipher, service.DefaultUpstreamTimeouts())
	upstream.SetAllowPrivate(true) // 测试用 localhost 假上游
	catalog := service.NewCatalogService(g, func() bool { return cfg.PromotionEnabled })
	quotes := service.NewQuoteService(catalog, service.NewQuotaService(g), cfg.JWTSecret)
	store := newFakeStorage("local")
	aiHandler := NewAIHandler(g, catalog, quotes, upstream, store, "http://localhost:3000", moderationService)

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	api := r.Group("/api")
	ai := api.Group("/ai", middleware.Auth(secret))
	ai.POST("/quote", aiHandler.Quote)
	ai.POST("/images/generations", aiHandler.Images)
	ai.POST("/videos/generations", aiHandler.CreateVideo)
	ai.GET("/videos/tasks/:id", aiHandler.VideoTask)
	ai.POST("/audio/speech", aiHandler.Speech)
	ai.POST("/chat/completions", aiHandler.Chat)

	generationHandler := NewGenerationHandler(g)
	generations := api.Group("/generations", middleware.Auth(secret))
	generations.GET("", generationHandler.List)
	generations.GET("/:id", generationHandler.Get)
	generations.DELETE("/:id", generationHandler.Delete)

	adminHandler := NewAdminHandlerWithUpstream(g, cfg, store, upstream)
	if moderationService != nil {
		adminHandler.SetModeration(moderationService)
	}
	admin := api.Group("/admin", middleware.Auth(secret), middleware.AdminOnly())
	admin.GET("/moderation/records", adminHandler.ListModerationRecords)
	admin.GET("/moderation/records/:id", adminHandler.GetModerationRecord)
	admin.GET("/moderation/records/:id/preview", adminHandler.PreviewModerationArtifact)
	admin.PATCH("/moderation/records/:id", adminHandler.ReviewModerationRecord)
	admin.POST("/moderation/records/:id/compensate", adminHandler.CompensateModeration)
	admin.GET("/moderation/stats", adminHandler.ModerationStats)
	return r, aiHandler
}

// seedImageModel 写入一个绑定平台的图像模型与足够的点数。
func seedImageModel(t *testing.T, g *gorm.DB, channelID uuid.UUID) model.ModelCatalog {
	t.Helper()
	channelIDs, _ := json.Marshal([]uuid.UUID{channelID})
	item := model.ModelCatalog{
		ID:          uuid.New(),
		Name:        "test-image",
		DisplayName: "测试图",
		Capability:  "image",
		Provider:    "openai",
		Constraints: datatypes.JSON([]byte(`{"size":["1024x1024","1024x768"],"quality":["low","high"],"n":{"max":4},"features":["referenceImage"]}`)),
		CreditCost:  datatypes.JSON([]byte(`{"version":1,"dimensions":["size","quality"],"prices":[{"params":{"size":"1024x1024","quality":"low"},"costMicros":100000},{"params":{"size":"1024x1024","quality":"high"},"costMicros":200000},{"params":{"size":"1024x768","quality":"low"},"costMicros":80000},{"params":{"size":"1024x768","quality":"high"},"costMicros":160000}]}`)),
		ChannelIDs:  channelIDs,
		Enabled:     true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入模型失败: %v", err)
	}
	return item
}

func seedCredits(t *testing.T, g *gorm.DB, userID uuid.UUID, purchased int64) {
	t.Helper()
	credits := service.NewCreditService(g)
	if err := credits.EnsureCredit(g, userID); err != nil {
		t.Fatalf("建账本行失败: %v", err)
	}
	if purchased == 0 {
		return
	}
	if err := credits.Purchase(context.Background(), g, &model.Order{
		ID: uuid.New(), UserID: userID, PurchasedMicros: purchased, EntitlementDays: 30,
	}, time.Now()); err != nil {
		t.Fatalf("入账失败: %v", err)
	}
}

func mustQuote(t *testing.T, r *gin.Engine, token, modelName, capability string, params map[string]any) map[string]any {
	t.Helper()
	w := doAuthJSON(r, http.MethodPost, "/api/ai/quote", token, map[string]any{
		"model": modelName, "capability": capability, "params": params,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("获取报价失败: code=%d body=%s", w.Code, w.Body.String())
	}
	return decodeBody(t, w)
}

// fakeOpenAIUpstream 模拟 OpenAI 兼容上游：生图返回 b64，视频创建与查询返回状态。
func fakeOpenAIUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		payload := base64.StdEncoding.EncodeToString([]byte("fake-png-data"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": payload}},
		})
	})
	mux.HandleFunc("/v1/videos", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "upstream-video-1"})
	})
	mux.HandleFunc("/v1/videos/upstream-video-1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "url": "http://127.0.0.1:1/result.mp4"})
	})
	mux.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("fake-mp3"))
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{"你好", "，世界"} {
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","delta":"` + chunk + `"}` + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestQuoteRejectsUnsupportedParams(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	channel := seedPlatformChannel(t, g, "http://127.0.0.1:9", "openai")
	seedImageModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "quote@example.com", "quoteuser", "password123", true)
	token := accessToken(t, cfg, &user)

	w := doAuthJSON(r, http.MethodPost, "/api/ai/quote", token, map[string]any{
		"model": "test-image", "capability": "image", "params": map[string]any{"size": "2048x2048"},
	})
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "PARAM_NOT_SUPPORTED" {
		t.Fatalf("非法参数应 400 PARAM_NOT_SUPPORTED, got %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if errObj["param"] != "size" {
		t.Fatalf("错误应带 param 与 allowed: %v", body)
	}

	// 能力不匹配返回 MODEL_NOT_SUPPORTED。
	w = doAuthJSON(r, http.MethodPost, "/api/ai/quote", token, map[string]any{
		"model": "test-image", "capability": "video", "params": map[string]any{},
	})
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "MODEL_NOT_SUPPORTED" {
		t.Fatalf("能力不匹配应 400 MODEL_NOT_SUPPORTED, got %d %s", w.Code, w.Body.String())
	}

	// 目录外的模型名一律拒绝。
	w = doAuthJSON(r, http.MethodPost, "/api/ai/quote", token, map[string]any{
		"model": "not-in-catalog", "capability": "image", "params": map[string]any{},
	})
	if errorCode(t, w) != "MODEL_NOT_SUPPORTED" {
		t.Fatalf("目录外模型应 MODEL_NOT_SUPPORTED, got %s", w.Body.String())
	}
}

func TestImageGenerationReservesCreditsAndWritesGeneration(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "gen@example.com", "genuser", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	if quote["finalCostMicros"] != float64(100000) {
		t.Fatalf("报价金额错误: %v", quote)
	}

	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model":          "test-image",
		"prompt":         "一只猫",
		"n":              1,
		"size":           "1024x1024",
		"quality":        "low",
		"quoteToken":     quote["quoteToken"],
		"idempotencyKey": "idem-image-1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("生图失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	images, _ := body["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("应返回一张图片: %v", body)
	}
	first, _ := images[0].(map[string]any)
	if first["storageKey"] == nil || first["storageKey"] == "" {
		t.Fatalf("产物必须返回 storageKey: %v", body)
	}
	if body["generationId"] == nil {
		t.Fatalf("响应应带 generationId: %v", body)
	}

	// 点数按报价扣减，流水只有一条消费。
	credits := service.NewCreditService(g)
	balance, _ := credits.Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 900_000 {
		t.Fatalf("余额应为 900000, got %d", balance.PurchasedMicros)
	}
	var generation model.Generation
	if err := g.Where("user_id = ? AND kind = ?", user.ID, "image").First(&generation).Error; err != nil {
		t.Fatalf("应写入一条生成记录: %v", err)
	}
	if generation.Status != "success" {
		t.Fatalf("生成记录状态应为 success: %s", generation.Status)
	}
}

func TestImageGenerationRefundsOnUpstreamFailure(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	// 上游固定返回 500。
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failing.Close()
	channel := seedPlatformChannel(t, g, failing.URL, "openai")
	seedImageModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "refund@example.com", "refunduser", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model":          "test-image",
		"prompt":         "会失败",
		"size":           "1024x1024",
		"quality":        "low",
		"quoteToken":     quote["quoteToken"],
		"idempotencyKey": "idem-refund-1",
	})
	if w.Code != http.StatusBadGateway || errorCode(t, w) != "UPSTREAM_ERROR" {
		t.Fatalf("上游 500 应映射为 502 UPSTREAM_ERROR, got %d %s", w.Code, w.Body.String())
	}

	credits := service.NewCreditService(g)
	balance, _ := credits.Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("失败应全额退还, 余额=%d", balance.PurchasedMicros)
	}
	// 逐桶对账：消费与退款各一条，净额为零。
	totals, _ := credits.SumByBucket(context.Background(), user.ID)
	if totals[service.BucketPurchased] != 1_000_000 {
		t.Fatalf("流水合计应回到原始余额: %v", totals)
	}
}

func TestImageGenerationIdempotencyAndMissingKey(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "idem@example.com", "idemuser", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	// 缺少 idempotencyKey 直接 400。
	quote := mustQuote(t, r, token, "test-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model": "test-image", "prompt": "缺少幂等键", "size": "1024x1024", "quality": "low", "quoteToken": quote["quoteToken"],
	})
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "VALIDATION_FAILED" {
		t.Fatalf("缺少幂等键应 400, got %d %s", w.Code, w.Body.String())
	}

	payload := map[string]any{
		"model": "test-image", "prompt": "重复提交", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "same-key",
	}
	first := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, payload)
	if first.Code != http.StatusOK {
		t.Fatalf("首次生成失败: %d %s", first.Code, first.Body.String())
	}
	second := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, payload)
	if second.Code == http.StatusOK {
		t.Fatalf("重复提交不应再次生成成功")
	}
	credits := service.NewCreditService(g)
	balance, _ := credits.Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 900_000 {
		t.Fatalf("重复提交只应扣一次, 余额=%d", balance.PurchasedMicros)
	}
}

func TestImageGenerationInsufficientCredits(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "poor@example.com", "pooruser", "password123", true)
	token := accessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model": "test-image", "prompt": "没有点数", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "poor-1",
	})
	if w.Code != http.StatusPaymentRequired || errorCode(t, w) != "INSUFFICIENT_CREDITS" {
		t.Fatalf("点数不足应 402, got %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if errObj["shortfallMicros"] != float64(100000) {
		t.Fatalf("错误应带差额: %v", body)
	}
}

func TestChatStreamEventsAndSlotRelease(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")

	channelIDs, _ := json.Marshal([]uuid.UUID{channel.ID})
	textModel := model.ModelCatalog{
		ID: uuid.New(), Name: "test-text", DisplayName: "测试文本", Capability: "text", Provider: "openai",
		Constraints: datatypes.JSON([]byte(`{}`)),
		CreditCost:  datatypes.JSON([]byte(`{"version":1,"dimensions":[],"prices":[{"params":{},"costMicros":20000}]}`)),
		ChannelIDs:  channelIDs,
		Enabled:     true,
	}
	if err := g.Create(&textModel).Error; err != nil {
		t.Fatalf("写入文本模型失败: %v", err)
	}
	r, aiHandler := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "chat@example.com", "chatuser", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	// 连续发起多轮 SSE，验证槽位在断开时被正确释放。
	for i := 0; i < 5; i++ {
		quote := mustQuote(t, r, token, "test-text", "text", map[string]any{})
		w := doAuthJSON(r, http.MethodPost, "/api/ai/chat/completions", token, map[string]any{
			"model":          "test-text",
			"messages":       []map[string]any{{"role": "user", "content": "你好"}},
			"stream":         true,
			"quoteToken":     quote["quoteToken"],
			"idempotencyKey": "chat-" + uuid.NewString(),
		})
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 轮流式对话失败: %d %s", i, w.Code, w.Body.String())
		}
		bodyText := w.Body.String()
		if !contains(bodyText, "event: delta") || !contains(bodyText, "你好") || !contains(bodyText, "event: done") {
			t.Fatalf("SSE 事件不完整: %s", bodyText)
		}
	}
	if count := aiHandler.Slots().count("text", user.ID.String()); count != 0 {
		t.Fatalf("流式结束后槽位应释放, got %d", count)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
