package admin

import (
	"bytes"
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
	r, _, _, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)

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
	// 隔离原件保留供人工复核；上传件以 stage=upload 落记录（评审 E-4 水印归属依据）。
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "upload").First(&record).Error; err != nil {
		t.Fatalf("应写上传审核记录: %v", err)
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
	r, _, _, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store, provider, "reject"))
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
	r2, _, _, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store2, provider2, "allow"))
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
	r, _, _, _ := newAITestRouterWithModeration(t, g, cfg, newModerationForTest(t, g, store, provider, "reject"))
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
	r, _, _, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)

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
	r, _, _, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)
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

// releaseWatermarkStub 是释放水印归属测试用的水印替身：正常时在尾部追加 "-wm" 字节。
type releaseWatermarkStub struct {
	fail bool
}

func (s *releaseWatermarkStub) Image(data []byte, mimeType string) ([]byte, string, error) {
	if s.fail {
		return nil, "", errors.New("水印字体不可用")
	}
	return append(data, []byte("-wm")...), mimeType, nil
}

func (s *releaseWatermarkStub) Video(ctx context.Context, data []byte, mimeType string) ([]byte, error) {
	if s.fail {
		return nil, errors.New("水印字体不可用")
	}
	return append(data, []byte("-wm")...), nil
}

// TestReleaseGenerationArtifactWatermarkAttribution 生成件释放的水印归属（评审 E-4）：
// 免费档物主释放后为水印版并留存干净原件，付费档物主释放为干净版且不写原件。
func TestReleaseGenerationArtifactWatermarkAttribution(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	store := testutil.NewFakeStorage("local")
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _, adminHandler, adminStore := newAITestRouterWithModeration(t, g, cfg, moderationService)
	adminHandler.SetWatermark(&releaseWatermarkStub{}, func() bool { return true })

	admin := testutil.CreateUser(t, g, "adminrel@example.com", "adminrel", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)

	user := testutil.CreateUser(t, g, "releasefree@example.com", "releasefree", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)
	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常提示词", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "release-gen-free",
	})
	if w.Code != http.StatusUnprocessableEntity || testutil.ErrorCode(t, w) != "CONTENT_REJECTED" {
		t.Fatalf("产物应被拒绝: %d %s", w.Code, w.Body.String())
	}
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "artifact").First(&record).Error; err != nil {
		t.Fatalf("应写产物审核记录: %v", err)
	}
	quarantined, err := moderationService.Quarantine().Get(context.Background(), user.ID, record.QuarantineKey)
	if err != nil {
		t.Fatalf("读取隔离原件失败: %v", err)
	}

	// 免费档：人工通过后释放为水印版，干净原件进 orig 供升级后下发。
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "误判放行", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("复核释放应成功: %d %s", w.Code, w.Body.String())
	}
	var file model.MediaFile
	if err := g.Where("user_id = ?", user.ID).First(&file).Error; err != nil {
		t.Fatalf("释放后应有媒体行: %v", err)
	}
	if file.Bytes != int64(len(quarantined)+len("-wm")) {
		t.Fatalf("免费档释放应为水印版字节: got %d want %d", file.Bytes, len(quarantined)+len("-wm"))
	}
	origReader, err := adminStore.Get(context.Background(), storage.OrigPath(user.ID.String(), file.StorageKey))
	if err != nil {
		t.Fatalf("干净原件应写入 orig: %v", err)
	}
	var orig bytes.Buffer
	if _, err := orig.ReadFrom(origReader); err != nil {
		t.Fatalf("读取干净原件失败: %v", err)
	}
	if !bytes.Equal(orig.Bytes(), quarantined) {
		t.Fatalf("orig 应为无水印原件")
	}

	// 付费档：同样被拒再释放，直接落原始字节、不写 orig。
	paid := testutil.CreateUser(t, g, "releasepaid@example.com", "releasepaid", "password123", true)
	seedCredits(t, g, paid.ID, 1_000_000)
	now := time.Now()
	if err := g.Create(&model.MembershipSubscription{ID: uuid.New(), UserID: paid.ID, PlanID: "paid", Status: "active", StartedAt: now, PeriodEnd: now.AddDate(0, 0, 30)}).Error; err != nil {
		t.Fatalf("写入付费订阅失败: %v", err)
	}
	paidToken := testutil.AccessToken(t, cfg, &paid)
	quote2 := mustQuote(t, r, paidToken, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", paidToken, map[string]any{
		"model": "moderation-image", "prompt": "正常提示词", "size": "1024x1024", "quality": "low",
		"quoteToken": quote2["quoteToken"], "idempotencyKey": "release-gen-paid",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("付费档产物应被拒绝: %d", w.Code)
	}
	var paidRecord model.ModerationRecord
	paidQuarantined := []byte(nil)
	if err := g.Where("user_id = ? AND stage = ?", paid.ID, "artifact").First(&paidRecord).Error; err != nil {
		t.Fatalf("应写产物审核记录: %v", err)
	}
	if paidQuarantined, err = moderationService.Quarantine().Get(context.Background(), paid.ID, paidRecord.QuarantineKey); err != nil {
		t.Fatalf("读取付费档隔离原件失败: %v", err)
	}
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/moderation/records/"+paidRecord.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "误判放行", "revision": paidRecord.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("付费档复核释放应成功: %d %s", w.Code, w.Body.String())
	}
	var paidFile model.MediaFile
	if err := g.Where("user_id = ?", paid.ID).First(&paidFile).Error; err != nil {
		t.Fatalf("释放后应有媒体行: %v", err)
	}
	if paidFile.Bytes != int64(len(paidQuarantined)) {
		t.Fatalf("付费档释放应为原始字节: got %d want %d", paidFile.Bytes, len(paidQuarantined))
	}
	if _, err := adminStore.Get(context.Background(), storage.OrigPath(paid.ID.String(), paidFile.StorageKey)); err == nil {
		t.Fatalf("付费档释放不应写干净原件")
	}
}

// TestReleaseUploadArtifactNeverWatermarked 上传件（stage=upload）即使免费档且水印开关
// 开启，释放也不烧水印、不写干净原件（评审 E-4）。
func TestReleaseUploadArtifactNeverWatermarked(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	store := testutil.NewFakeStorage("local")
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _, adminHandler, adminStore := newAITestRouterWithModeration(t, g, cfg, moderationService)
	adminHandler.SetWatermark(&releaseWatermarkStub{}, func() bool { return true })

	admin := testutil.CreateUser(t, g, "adminup@example.com", "adminup", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)
	user := testutil.CreateUser(t, g, "uprelease@example.com", "uprelease", "password123", true)

	// 直接走上传链路的同一口径：落隔离区并以 stage=upload 送审被拒。
	raw := testutil.TestPNG
	quarantine, err := moderationService.Quarantine().Put(context.Background(), user.ID, raw, "image/png")
	if err != nil {
		t.Fatalf("写入隔离区失败: %v", err)
	}
	if _, err := moderationService.CheckArtifact(context.Background(), user.ID, moderation.StageUpload, moderation.ContentType("image"), quarantine.Key, raw, "image/png"); err == nil {
		t.Fatalf("被拒上传应返回错误")
	}
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "upload").First(&record).Error; err != nil {
		t.Fatalf("应写上传审核记录: %v", err)
	}

	// 人工通过释放：上传件直接落原始字节。
	w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "误判放行", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("复核释放应成功: %d %s", w.Code, w.Body.String())
	}
	var file model.MediaFile
	if err := g.Where("user_id = ?", user.ID).First(&file).Error; err != nil {
		t.Fatalf("释放后应有媒体行: %v", err)
	}
	if file.Bytes != int64(len(raw)) {
		t.Fatalf("上传件释放不应烧水印: got %d want %d", file.Bytes, len(raw))
	}
	if _, err := adminStore.Get(context.Background(), storage.OrigPath(user.ID.String(), file.StorageKey)); err == nil {
		t.Fatalf("上传件释放不应写干净原件")
	}
}

// TestReleaseGenerationArtifactFailsClosedOnWatermarkError 生成件释放的水印烧录失败时
// fail-closed：不落正式存储、隔离原件保留待重试（评审 E-4）。
func TestReleaseGenerationArtifactFailsClosedOnWatermarkError(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedModerationModel(t, g, channel.ID)
	provider := &moderation.FakeProvider{RejectLabels: []string{"adult"}}
	store := testutil.NewFakeStorage("local")
	moderationService := newModerationForTest(t, g, store, provider, "reject")
	r, _, adminHandler, _ := newAITestRouterWithModeration(t, g, cfg, moderationService)
	wm := &releaseWatermarkStub{fail: true}
	adminHandler.SetWatermark(wm, func() bool { return true })

	admin := testutil.CreateUser(t, g, "adminfail@example.com", "adminfail", "password123", true)
	testutil.PromoteAdmin(t, g, &admin)
	adminToken := testutil.AccessToken(t, cfg, &admin)
	user := testutil.CreateUser(t, g, "relfail@example.com", "relfail", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)
	quote := mustQuote(t, r, token, "moderation-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model": "moderation-image", "prompt": "正常提示词", "size": "1024x1024", "quality": "low",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "release-fail-1",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("产物应被拒绝: %d", w.Code)
	}
	var record model.ModerationRecord
	if err := g.Where("user_id = ? AND stage = ?", user.ID, "artifact").First(&record).Error; err != nil {
		t.Fatalf("应写产物审核记录: %v", err)
	}

	// 水印失败：释放失败、隔离原件保留、无媒体行。
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "误判放行", "revision": record.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("复核请求本身应成功: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["released"] != false || body["releaseError"] == "" {
		t.Fatalf("水印失败应返回 released=false 与释放错误: %v", body)
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("水印失败不应释放进正式存储")
	}
	var afterFail model.ModerationRecord
	if err := g.First(&afterFail, "id = ?", record.ID).Error; err != nil {
		t.Fatalf("读取审核记录失败: %v", err)
	}
	if afterFail.QuarantineKey == "" {
		t.Fatalf("水印失败应保留隔离原件待重试")
	}

	// 水印恢复后重试释放成功。
	wm.fail = false
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/admin/moderation/records/"+record.ID.String(), adminToken, map[string]any{
		"decision": "approved", "note": "复核重试释放", "revision": afterFail.ReviewRevision,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("重试释放应成功: %d %s", w.Code, w.Body.String())
	}
	var mediaAfterRetry int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaAfterRetry)
	if mediaAfterRetry != 1 {
		t.Fatalf("重试释放后应有一条媒体行, got %d", mediaAfterRetry)
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
// 第三个返回值是挂了审核管理路由的 AdminHandler（可注入水印替身），第四个是夹具内自建的
// 假存储（审核隔离区与 admin 释放落盘共用它，断言对象内容时使用）。
func newAITestRouterWithModeration(t *testing.T, g *gorm.DB, cfg *config.Config, moderationService *service.ModerationService) (*gin.Engine, *ai.AIHandler, *AdminHandler, storage.Storage) {
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
	return r, aiHandler, adminHandler, store
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
