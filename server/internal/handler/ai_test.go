package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/provider"
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
	admin := api.Group("/admin", middleware.Auth(secret), middleware.LoadAdminAccess(g))
	admin.GET("/moderation/records", middleware.RequirePermission(authz.PermModerationRead), adminHandler.ListModerationRecords)
	admin.GET("/moderation/records/:id", middleware.RequirePermission(authz.PermModerationRead), adminHandler.GetModerationRecord)
	admin.GET("/moderation/records/:id/preview", middleware.RequirePermission(authz.PermModerationRead), adminHandler.PreviewModerationArtifact)
	admin.PATCH("/moderation/records/:id", middleware.RequirePermission(authz.PermModerationReview), adminHandler.ReviewModerationRecord)
	admin.POST("/moderation/records/:id/compensate", middleware.RequirePermission(authz.PermModerationCompensate), adminHandler.CompensateModeration)
	admin.GET("/moderation/stats", middleware.RequirePermission(authz.PermModerationRead), adminHandler.ModerationStats)
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

// seedTextModel 写入一个绑定若干渠道的文本模型，渠道顺序即故障转移顺序。
func seedTextModel(t *testing.T, g *gorm.DB, channelIDs ...uuid.UUID) model.ModelCatalog {
	t.Helper()
	ids, _ := json.Marshal(channelIDs)
	item := model.ModelCatalog{
		ID: uuid.New(), Name: "test-text", DisplayName: "测试文本", Capability: "text", Provider: "openai",
		Constraints: datatypes.JSON([]byte(`{}`)),
		CreditCost:  datatypes.JSON([]byte(`{"version":1,"dimensions":[],"prices":[{"params":{},"costMicros":20000}]}`)),
		ChannelIDs:  ids,
		Enabled:     true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入文本模型失败: %v", err)
	}
	return item
}

// postChatStream 报价后发起一次流式对话请求。
func postChatStream(t *testing.T, r *gin.Engine, token, modelName, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	quote := mustQuote(t, r, token, modelName, "text", map[string]any{})
	return doAuthJSON(r, http.MethodPost, "/api/ai/chat/completions", token, map[string]any{
		"model":          modelName,
		"messages":       []map[string]any{{"role": "user", "content": "你好"}},
		"stream":         true,
		"quoteToken":     quote["quoteToken"],
		"idempotencyKey": idempotencyKey,
	})
}

// failingUpstream 返回固定 502 的假上游，hits 记录被请求次数（首字节前失败）。
func failingUpstream(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	return server
}

// sseTextUpstream 返回按 chunks 逐段吐 delta 的假上游，hits 记录被请求次数。
func sseTextUpstream(t *testing.T, hits *int32, chunks ...string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","delta":"` + chunk + `"}` + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// creditTxCount 统计用户指定类型的流水条数。
func creditTxCount(t *testing.T, g *gorm.DB, userID uuid.UUID, txType string) int64 {
	t.Helper()
	var count int64
	if err := g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", userID, txType).Count(&count).Error; err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	return count
}

// TestChatStreamStopsFailoverAfterProducedOutput 决策 1 的闸门：渠道 A 已吐出
// delta 后失败，必须立即终止——响应体恰好是已发出的 2 个 delta 加 1 个 done，
// 不重试、不切渠道、无重复正文；已产出按成功记账，不产生退款流水。
func TestChatStreamStopsFailoverAfterProducedOutput(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"

	var aHits, bHits int32
	// 渠道 A：吐 2 个 delta 后以 response.failed（上游 502 语义）收场。
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&aHits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{"第一段", "第二段"} {
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","delta":"` + chunk + `"}` + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstreamA.Close()
	// 渠道 B：它的正文一旦出现即说明违反「已产出即终止」。
	upstreamB := sseTextUpstream(t, &bHits, "渠道B正文")

	channelA := seedPlatformChannel(t, g, upstreamA.URL, "openai")
	channelB := seedPlatformChannel(t, g, upstreamB.URL, "openai")
	seedTextModel(t, g, channelA.ID, channelB.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := createUser(t, g, "produced@example.com", "produced", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	w := postChatStream(t, r, token, "test-text", "produced-1")
	if w.Code != http.StatusOK {
		t.Fatalf("流式对话失败: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if got := strings.Count(body, "event: delta"); got != 2 {
		t.Fatalf("响应体应恰好 2 个 delta 事件, got %d: %s", got, body)
	}
	if got := strings.Count(body, "event: done"); got != 1 {
		t.Fatalf("响应体应恰好 1 个 done 事件, got %d: %s", got, body)
	}
	if strings.Contains(body, "event: error") {
		t.Fatalf("已产出后不允许出现 error 事件: %s", body)
	}
	if !contains(body, "第一段") || !contains(body, "第二段") {
		t.Fatalf("已发出的 delta 正文必须保留: %s", body)
	}
	if strings.Contains(body, "渠道B正文") {
		t.Fatalf("不允许出现渠道 B 的重复正文: %s", body)
	}
	if atomic.LoadInt32(&aHits) != 1 || atomic.LoadInt32(&bHits) != 0 {
		t.Fatalf("已产出后不重试也不切渠道: aHits=%d bHits=%d", aHits, bHits)
	}
	var req model.AIRequest
	if err := g.Where("user_id = ?", user.ID).First(&req).Error; err != nil {
		t.Fatalf("读取请求失败: %v", err)
	}
	if req.Status != "succeeded" {
		t.Fatalf("已产出后应按成功记账, got %s", req.Status)
	}
	if refund := creditTxCount(t, g, user.ID, service.TxTypeRefund); refund != 0 {
		t.Fatalf("不应产生退款流水, got %d", refund)
	}
}

// TestChatStreamRetryAndFailoverBilling 决策 1 的记账与重试表行为：
// 首字节前失败才允许原渠道重试一次再切渠道；重试与切渠道都不重复扣点，
// 全部失败时全额退款并给出 error 事件。
func TestChatStreamRetryAndFailoverBilling(t *testing.T) {
	newFixture := func(t *testing.T) (*gorm.DB, *gin.Engine, model.User, string) {
		g := newTestDB(t)
		cfg := testConfig()
		cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
		r, _ := newAITestRouter(t, g, cfg)
		user := createUser(t, g, "failover@example.com", "failover", "password123", true)
		seedCredits(t, g, user.ID, 1_000_000)
		return g, r, user, accessToken(t, cfg, &user)
	}

	t.Run("首渠道 502 两次后切渠道成功：一条消费流水、无退款", func(t *testing.T) {
		g, r, user, token := newFixture(t)
		var aHits, bHits int32
		upstreamA := failingUpstream(t, &aHits)
		upstreamB := sseTextUpstream(t, &bHits, "你好", "，世界")
		channelA := seedPlatformChannel(t, g, upstreamA.URL, "openai")
		channelB := seedPlatformChannel(t, g, upstreamB.URL, "openai")
		seedTextModel(t, g, channelA.ID, channelB.ID)

		w := postChatStream(t, r, token, "test-text", "failover-ok-1")
		if w.Code != http.StatusOK {
			t.Fatalf("流式对话失败: %d %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if atomic.LoadInt32(&aHits) != 2 {
			t.Fatalf("首渠道应原渠道重试一次, aHits=%d", aHits)
		}
		if atomic.LoadInt32(&bHits) != 1 {
			t.Fatalf("重试失败后应切到下一个渠道, bHits=%d", bHits)
		}
		if !contains(body, "你好") || !contains(body, "event: done") || strings.Contains(body, "event: error") {
			t.Fatalf("应由渠道 B 正常收场: %s", body)
		}
		var req model.AIRequest
		if err := g.Where("user_id = ?", user.ID).First(&req).Error; err != nil {
			t.Fatalf("读取请求失败: %v", err)
		}
		if req.Status != "succeeded" {
			t.Fatalf("切换后成功应记 succeeded, got %s", req.Status)
		}
		if consume := creditTxCount(t, g, user.ID, service.TxTypeConsume); consume != 1 {
			t.Fatalf("应恰好一条消费流水, got %d", consume)
		}
		if refund := creditTxCount(t, g, user.ID, service.TxTypeRefund); refund != 0 {
			t.Fatalf("成功不应退款, got %d 条退款流水", refund)
		}
	})

	t.Run("两个渠道全失败：一条退款流水加 error 事件", func(t *testing.T) {
		g, r, user, token := newFixture(t)
		var aHits, bHits int32
		upstreamA := failingUpstream(t, &aHits)
		upstreamB := failingUpstream(t, &bHits)
		channelA := seedPlatformChannel(t, g, upstreamA.URL, "openai")
		channelB := seedPlatformChannel(t, g, upstreamB.URL, "openai")
		seedTextModel(t, g, channelA.ID, channelB.ID)

		w := postChatStream(t, r, token, "test-text", "failover-bad-1")
		if w.Code != http.StatusOK {
			t.Fatalf("SSE 响应头应先于上游调用写出: %d %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if atomic.LoadInt32(&aHits) != 2 || atomic.LoadInt32(&bHits) != 2 {
			t.Fatalf("每个渠道都应重试一次: aHits=%d bHits=%d", aHits, bHits)
		}
		if !contains(body, "event: error") {
			t.Fatalf("全失败应以 error 事件收场: %s", body)
		}
		if strings.Contains(body, "event: done") || strings.Contains(body, "event: delta") {
			t.Fatalf("全失败不应有 done 或 delta 事件: %s", body)
		}
		var req model.AIRequest
		if err := g.Where("user_id = ?", user.ID).First(&req).Error; err != nil {
			t.Fatalf("读取请求失败: %v", err)
		}
		if req.Status != "failed" {
			t.Fatalf("全失败应记 failed, got %s", req.Status)
		}
		if refund := creditTxCount(t, g, user.ID, service.TxTypeRefund); refund != 1 {
			t.Fatalf("应恰好一条退款流水, got %d", refund)
		}
		credits := service.NewCreditService(g)
		balance, _ := credits.Balance(context.Background(), user.ID)
		if balance.PurchasedMicros != 1_000_000 {
			t.Fatalf("失败应全额退还, 余额=%d", balance.PurchasedMicros)
		}
	})
}

// TestCallChatCanceledContextMakesNoSecondUpstreamRequest 决策 2 的取消语义：
// context.Canceled 原样上抛不打标，两谓词均为 false，因此不产生第二次上游请求。
func TestCallChatCanceledContextMakesNoSecondUpstreamRequest(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"

	var hits int32
	started := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		started <- struct{}{}
		// 挂住直到客户端取消，确保取消发生在第一次请求进行中。
		<-r.Context().Done()
	}))
	defer upstream.Close()
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	item := seedTextModel(t, g, channel.ID)
	r, aiHandler := newAITestRouter(t, g, cfg)
	_ = r

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := aiHandler.callChat(ctx, item, provider.ChatRequest{
			Model:    item.Name,
			Messages: []provider.ChatMessage{{Role: "user", Content: "你好"}},
			Stream:   true,
		}, func() provider.StreamSink { return &collectSink{} })
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("上游未收到第一次请求")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消的错误应原样上抛, got %v", err)
		}
		if service.IsRetryableUpstream(err) || service.ShouldFailover(err) {
			t.Fatalf("context.Canceled 两谓词都必须为 false")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("callChat 未在取消后返回")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("取消后不允许发起第二次上游请求, hits=%d", hits)
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
