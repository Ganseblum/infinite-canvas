package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
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
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectTexts: []string{"违禁词"}}
	store := newFakeStorage("local")
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)

	user := createUser(t, g, "modprompt@example.com", "modprompt", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "包含违禁词的提示", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-prompt-1",
	})
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("违规提示词应 422 CONTENT_REJECTED, got %d %s", w.Code, w.Body.String())
	}
	// 不扣点、无消费流水、没有生成记录。
	balance, _ := service.NewCreditService(g).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("被拒输入不应扣点, 余额=%d", balance.PurchasedMicros)
	}
	var consumes int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, service.TxTypeConsume).Count(&consumes)
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
	g := newTestDB(t)
	cfg := testConfig()
	store := newFakeStorage("local")
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r := newResourceRouterWithModeration(t, g, cfg, store, moderationService)
	user := createUser(t, g, "modupload@example.com", "modupload", "password123", true)
	token := accessToken(t, cfg, &user)

	w := doRaw(r, http.MethodPut, "/api/media/image:ModUp1", []byte("bad-image"), "image/png", token, nil)
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("违规上传应 422 CONTENT_REJECTED, got %d %s", w.Code, w.Body.String())
	}
	// 不写正式记录、不增加用量。
	var count int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 0 {
		t.Fatalf("被拒上传不应写媒体记录")
	}
	used, _ := service.NewQuotaService(g).StorageBytes(context.Background(), user.ID)
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
	ok := doRaw(r, http.MethodPut, "/api/media/image:ModUp1", []byte("good-image"), "image/png", token, nil)
	if ok.Code != http.StatusCreated {
		t.Fatalf("通过审核的上传应 201, got %d %s", ok.Code, ok.Body.String())
	}
	okAgain := doRaw(r, http.MethodPut, "/api/media/image:ModUp1", []byte("good-image"), "image/png", token, nil)
	if okAgain.Code != http.StatusCreated {
		t.Fatalf("重复上传应 201, got %d", okAgain.Code)
	}
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 1 {
		t.Fatalf("同一 storageKey 只应有一条正式记录, got %d", count)
	}
	used, _ = service.NewQuotaService(g).StorageBytes(context.Background(), user.ID)
	if used != int64(len("good-image")) {
		t.Fatalf("用量应只计正式对象, got %d", used)
	}
}

func TestModerationFailModeRejectAndAllow(t *testing.T) {
	// reject：审核服务故障返回 503，不预扣。
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{FailWith: errors.New("dial timeout")}
	store := newFakeStorage("local")
	r, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store, provider, "reject"))
	user := createUser(t, g, "modfail@example.com", "modfail", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)
	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})

	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常内容", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-fail-1",
	})
	if w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "MODERATION_UNAVAILABLE" {
		t.Fatalf("reject 模式下审核故障应 503, got %d %s", w.Code, w.Body.String())
	}
	balance, _ := service.NewCreditService(g).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("审核故障不应预扣, 余额=%d", balance.PurchasedMicros)
	}

	// allow：明确配置后放行，记录为 error 且继续生成。
	provider2 := &moderation.FakeProvider{FailWith: errors.New("dial timeout")}
	store2 := newFakeStorage("local")
	provider2.RejectTexts = nil
	r2, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store2, provider2, "allow"))
	quote2 := mustQuote(t, r2, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w2 := doAuthJSON(r2, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
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
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	store := newFakeStorage("local")
	r, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store, provider, "reject"))
	user := createUser(t, g, "modartifact@example.com", "modartifact", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)
	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})

	w := doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常提示词", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "mod-artifact-1",
	})
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("违规产物应 422, got %d %s", w.Code, w.Body.String())
	}
	// 产物拒绝不退点：上游成本已经发生。
	balance, _ := service.NewCreditService(g).Balance(context.Background(), user.ID)
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
	g := newTestDB(t)
	cfg := testConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectTexts: []string{"违禁"}}
	store := newFakeStorage("local")
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, aiHandler := newAITestRouterWithModeration(t, g, cfg, moderationService)

	admin := createUser(t, g, "adminmod@example.com", "adminmod", "password123", true)
	promoteAdmin(t, g, &admin)
	adminToken := accessToken(t, cfg, &admin)
	user := createUser(t, g, "modreview@example.com", "modreview", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := accessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	doAuthJSON(r, http.MethodPost, "/api/ai/images/generations", token, map[string]any{
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
	w := doAuthJSON(adminRouter, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "误判，人工通过", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("复核应成功: %d %s", w.Code, w.Body.String())
	}
	// 第二个管理员用旧 revision 提交会冲突。
	w = doAuthJSON(adminRouter, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "rejected", "note": "覆盖前一结论", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusConflict || errorCode(t, w) != "MODERATION_ALREADY_REVIEWED" {
		t.Fatalf("旧 revision 复核应 409, got %d %s", w.Code, w.Body.String())
	}

	// 补偿进入 granted 桶且幂等。
	w = doAuthJSON(adminRouter, http.MethodPost, "/api/admin/moderation/records/"+record.ID.String()+"/compensate", adminToken, map[string]any{
		"amountMicros": 100000, "note": "误判补偿",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("补偿应成功: %d %s", w.Code, w.Body.String())
	}
	balance, _ := service.NewCreditService(g).Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 100000 || balance.PurchasedMicros != 1_000_000 {
		t.Fatalf("补偿应进入赠送桶且不改变付费身份: %+v", balance)
	}
	w = doAuthJSON(adminRouter, http.MethodPost, "/api/admin/moderation/records/"+record.ID.String()+"/compensate", adminToken, map[string]any{
		"amountMicros": 100000, "note": "重复补偿",
	})
	if w.Code == http.StatusOK {
		t.Fatalf("同一记录重复补偿应被拒绝")
	}
	balanceAfter, _ := service.NewCreditService(g).Balance(context.Background(), user.ID)
	if balanceAfter.GrantedMicros != 100000 {
		t.Fatalf("重复补偿不应重复到账, granted=%d", balanceAfter.GrantedMicros)
	}
}

func TestModerationStatsAndNonAdminDenied(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	store := newFakeStorage("local")
	provider := &moderation.FakeProvider{}
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)
	user := createUser(t, g, "modstats@example.com", "modstats", "password123", true)
	token := accessToken(t, cfg, &user)

	// 非管理员访问审核接口一律 403。
	for _, path := range []string{"/api/admin/moderation/records", "/api/admin/moderation/stats"} {
		w := doAuthJSON(r, http.MethodGet, path, token, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("非管理员访问 %s 应 403, got %d", path, w.Code)
		}
	}
}
