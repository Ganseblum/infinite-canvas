package ai

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/testutil"
)

// stubWatermarker 是生成落盘水印挂钩的测试替身：可注入错误、可记录调用次数。
// 成功时输出「原始字节 + -watermarked 后缀」与 image/png，保证水印字节必不等于原始字节。
type stubWatermarker struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubWatermarker) Image(src []byte, srcMime string) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, "", s.err
	}
	out := append(append([]byte(nil), src...), []byte("-watermarked")...)
	return out, "image/png", nil
}

// newWatermarkRouter 组装带水印挂钩的生图路由，storage 与水印替身均可注入。
func newWatermarkRouter(t *testing.T, g *gorm.DB, cfg *config.Config, stor storage.Storage, wm imageWatermarker, enabled func() bool) (*gin.Engine, *AIHandler) {
	t.Helper()
	cipher, err := crypto.New(cfg.CredentialKey)
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	upstream := service.NewUpstreamService(g, cipher, service.DefaultUpstreamTimeouts())
	upstream.SetAllowPrivate(true) // 测试用 localhost 假上游
	catalog := service.NewCatalogService(g, func() bool { return false })
	quotes := service.NewQuoteService(catalog, billing.NewService(g, model.ProductCanvas), cfg.JWTSecret)
	aiHandler := NewAIHandler(g, catalog, quotes, upstream, stor, "http://localhost:3000", nil)
	aiHandler.SetWatermark(wm, enabled)

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	api := r.Group("/api/v1", middleware.Auth([]byte(cfg.JWTSecret)))
	api.POST("/ai/quote", aiHandler.Quote)
	api.POST("/ai/images/generations", aiHandler.Images)
	return r, aiHandler
}

// seedGrantedCredits 只给赠送桶入账：档位派生不看赠送桶（D6），用户保持 free 档但有余额可预扣。
func seedGrantedCredits(t *testing.T, g *gorm.DB, userID uuid.UUID, micros int64) {
	t.Helper()
	points := billing.NewService(g, model.ProductCanvas)
	if err := points.EnsureAccount(g, userID); err != nil {
		t.Fatalf("建账本行失败: %v", err)
	}
	if _, err := points.Adjust(g, userID, billing.BucketGranted, micros, "水印挂钩测试入账", "test"); err != nil {
		t.Fatalf("赠送桶入账失败: %v", err)
	}
}

// generateImage 走一遍报价 + 生图，返回响应与生成的 storageKey（失败时为空串）。
func generateImage(t *testing.T, r *gin.Engine, token, idempotencyKey string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	quote := mustQuote(t, r, token, "test-image", "image", map[string]any{"size": "1024x1024", "quality": "low"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/images/generations", token, map[string]any{
		"model":          "test-image",
		"prompt":         "水印挂钩测试",
		"n":              1,
		"size":           "1024x1024",
		"quality":        "low",
		"quoteToken":     quote["quoteToken"],
		"idempotencyKey": idempotencyKey,
	})
	key := ""
	if w.Code == http.StatusOK {
		body := testutil.DecodeBody(t, w)
		images, _ := body["images"].([]any)
		if len(images) != 1 {
			t.Fatalf("应返回一张图片: %v", body)
		}
		first, _ := images[0].(map[string]any)
		key, _ = first["storageKey"].(string)
	}
	return w, key
}

// TestImageGenerationWatermarkFailClosed 锁死 S1 红线：free 档水印烧录失败 →
// 整单失败并退款，存储中无主对象也无 orig 对象，无任何媒体行（绝不漏出干净字节）。
func TestImageGenerationWatermarkFailClosed(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	stor := testutil.NewFakeStorage("local")
	wm := &stubWatermarker{err: errors.New("注入的水印故障")}
	r, _ := newWatermarkRouter(t, g, cfg, stor, wm, func() bool { return true })
	user := testutil.CreateUser(t, g, "wmfail@example.com", "wmfail", "password123", true)
	seedGrantedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	w, _ := generateImage(t, r, token, "wm-fail-1")
	if w.Code != http.StatusBadGateway || testutil.ErrorCode(t, w) != "UPSTREAM_ERROR" {
		t.Fatalf("水印失败应走既有失败路径 502 UPSTREAM_ERROR, got %d %s", w.Code, w.Body.String())
	}

	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("fail-closed 不应产生任何媒体行, got %d", mediaCount)
	}
	if len(stor.Objects) != 0 {
		t.Fatalf("存储中不应有任何对象（主对象与 orig 均不可落盘）: %v", stor.Objects)
	}
	var request model.AIRequest
	if err := g.Where("user_id = ?", user.ID).First(&request).Error; err != nil {
		t.Fatalf("应写入生成请求行: %v", err)
	}
	if request.Status != "failed" {
		t.Fatalf("请求应收敛为 failed: %s", request.Status)
	}
	var generationCount int64
	g.Model(&model.Generation{}).Where("user_id = ?", user.ID).Count(&generationCount)
	if generationCount != 0 {
		t.Fatalf("非拒绝失败不写生成记录（既有语义）, got %d", generationCount)
	}
	// 预扣冲销：余额回到原值，退款流水恰好一条。
	balance, err := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取余额失败: %v", err)
	}
	if balance.GrantedMicros != 1_000_000 {
		t.Fatalf("失败应全额退还, granted=%d", balance.GrantedMicros)
	}
	var refunds int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, billing.TxTypeRefund).Count(&refunds)
	if refunds != 1 {
		t.Fatalf("退款流水应只有一条, got %d", refunds)
	}
}

// TestImageGenerationOrigPutFailureFailsClosed 验证落盘序第②步（评审 E-3）：
// orig Put 失败 → 整单失败退款。水印版主对象尚未落盘，存储中主对象与 orig 均不存在，
// 无任何媒体行。putErr 注入命中首个 Put 即 orig（水印烧录不经过存储）。
func TestImageGenerationOrigPutFailureFailsClosed(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	stor := testutil.NewFakeStorage("local")
	stor.PutErr = errors.New("注入的 orig 写入故障")
	wm := &stubWatermarker{}
	r, _ := newWatermarkRouter(t, g, cfg, stor, wm, func() bool { return true })
	user := testutil.CreateUser(t, g, "wmorigfail@example.com", "wmorigfail", "password123", true)
	seedGrantedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	w, _ := generateImage(t, r, token, "wm-origfail-1")
	if w.Code != http.StatusBadGateway || testutil.ErrorCode(t, w) != "UPSTREAM_ERROR" {
		t.Fatalf("orig 写入失败应走既有失败路径 502 UPSTREAM_ERROR, got %d %s", w.Code, w.Body.String())
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("orig 写入失败不应产生任何媒体行, got %d", mediaCount)
	}
	if len(stor.Objects) != 0 {
		t.Fatalf("orig 写入失败时主对象与 orig 均不应落盘: %v", stor.Objects)
	}
	var request model.AIRequest
	if err := g.Where("user_id = ?", user.ID).First(&request).Error; err != nil {
		t.Fatalf("应写入生成请求行: %v", err)
	}
	if request.Status != "failed" {
		t.Fatalf("请求应收敛为 failed: %s", request.Status)
	}
	balance, err := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取余额失败: %v", err)
	}
	if balance.GrantedMicros != 1_000_000 {
		t.Fatalf("失败应全额退还, granted=%d", balance.GrantedMicros)
	}
	var refunds int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, billing.TxTypeRefund).Count(&refunds)
	if refunds != 1 {
		t.Fatalf("退款流水应只有一条, got %d", refunds)
	}
}

// TestImageGenerationWatermarkFreeStoresOrig 验证 free 档成功序：
// orig 保留原始字节、主对象为水印后字节、媒体行记录水印版 mime 与大小。
func TestImageGenerationWatermarkFreeStoresOrig(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	stor := testutil.NewFakeStorage("local")
	wm := &stubWatermarker{}
	r, _ := newWatermarkRouter(t, g, cfg, stor, wm, func() bool { return true })
	user := testutil.CreateUser(t, g, "wmfree@example.com", "wmfree", "password123", true)
	seedGrantedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	w, key := generateImage(t, r, token, "wm-free-1")
	if w.Code != http.StatusOK || key == "" {
		t.Fatalf("免费档生成应成功: %d %s", w.Code, w.Body.String())
	}
	if wm.calls != 1 {
		t.Fatalf("免费档应烧录一次水印: calls=%d", wm.calls)
	}
	original := []byte("fake-png-data")
	wantWM := append(append([]byte(nil), original...), []byte("-watermarked")...)
	mainPath := storage.ObjectPath(user.ID.String(), key)
	origPath := storage.OrigPath(user.ID.String(), key)
	if origPath == "" {
		t.Fatalf("生成产物 storageKey 必含冒号，orig 路径不应为空")
	}
	if got, ok := stor.Objects[origPath]; !ok || !bytes.Equal(got, original) {
		t.Fatalf("orig 应存在且内容为原始字节: %q", got)
	}
	if got, ok := stor.Objects[mainPath]; !ok || !bytes.Equal(got, wantWM) {
		t.Fatalf("主对象应为水印后字节: %q", got)
	}
	var file model.MediaFile
	if err := g.Where("user_id = ? AND storage_key = ?", user.ID, key).First(&file).Error; err != nil {
		t.Fatalf("应写入媒体行: %v", err)
	}
	if file.MimeType != "image/png" {
		t.Fatalf("媒体行 mime 应为水印返回的 wmMime, got %s", file.MimeType)
	}
	if file.Bytes != int64(len(wantWM)) {
		t.Fatalf("媒体行字节数应为水印版, got %d", file.Bytes)
	}
	var generation model.Generation
	if err := g.Where("user_id = ? AND kind = ?", user.ID, "image").First(&generation).Error; err != nil {
		t.Fatalf("应写入生成记录: %v", err)
	}
	if generation.Status != "success" {
		t.Fatalf("生成记录应为 success: %s", generation.Status)
	}
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 900_000 {
		t.Fatalf("成功应正常扣点, granted=%d", balance.GrantedMicros)
	}
}

// TestImageGenerationPaidSkipsWatermark 验证付费档现状不变：不烧水印、不写 orig、
// 主对象即原始字节。
func TestImageGenerationPaidSkipsWatermark(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	stor := testutil.NewFakeStorage("local")
	wm := &stubWatermarker{}
	r, _ := newWatermarkRouter(t, g, cfg, stor, wm, func() bool { return true })
	user := testutil.CreateUser(t, g, "wmpaid@example.com", "wmpaid", "password123", true)
	makePaid(t, g, user)                  // 有效订阅 → paid 档（D6）
	seedCredits(t, g, user.ID, 1_000_000) // 余额可预扣
	token := testutil.AccessToken(t, cfg, &user)

	w, key := generateImage(t, r, token, "wm-paid-1")
	if w.Code != http.StatusOK || key == "" {
		t.Fatalf("付费档生成应成功: %d %s", w.Code, w.Body.String())
	}
	if wm.calls != 0 {
		t.Fatalf("付费档不应调用水印: calls=%d", wm.calls)
	}
	if len(stor.Objects) != 1 {
		t.Fatalf("付费档不应写 orig, objects=%v", stor.Objects)
	}
	mainPath := storage.ObjectPath(user.ID.String(), key)
	if got, ok := stor.Objects[mainPath]; !ok || !bytes.Equal(got, []byte("fake-png-data")) {
		t.Fatalf("付费档主对象应为原始字节: %q", got)
	}
	var file model.MediaFile
	if err := g.Where("user_id = ? AND storage_key = ?", user.ID, key).First(&file).Error; err != nil {
		t.Fatalf("应写入媒体行: %v", err)
	}
	if file.Bytes != int64(len("fake-png-data")) {
		t.Fatalf("付费档媒体行应为原始字节大小, got %d", file.Bytes)
	}
}

// TestImageGenerationWatermarkDisabled 验证开关关闭时整段跳过：free 档也不判档、
// 不写 orig、不烧水印，行为与现状完全一致。
func TestImageGenerationWatermarkDisabled(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	stor := testutil.NewFakeStorage("local")
	wm := &stubWatermarker{}
	r, _ := newWatermarkRouter(t, g, cfg, stor, wm, func() bool { return false })
	user := testutil.CreateUser(t, g, "wmoff@example.com", "wmoff", "password123", true)
	seedGrantedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	w, key := generateImage(t, r, token, "wm-off-1")
	if w.Code != http.StatusOK || key == "" {
		t.Fatalf("开关关闭时生成应成功: %d %s", w.Code, w.Body.String())
	}
	if wm.calls != 0 {
		t.Fatalf("开关关闭时不应调用水印: calls=%d", wm.calls)
	}
	if len(stor.Objects) != 1 {
		t.Fatalf("开关关闭时不应写 orig, objects=%v", stor.Objects)
	}
	mainPath := storage.ObjectPath(user.ID.String(), key)
	if got, ok := stor.Objects[mainPath]; !ok || !bytes.Equal(got, []byte("fake-png-data")) {
		t.Fatalf("开关关闭时主对象应为原始字节: %q", got)
	}
}

// TestImageGenerationSaveFailureCompensatesOrig 验证落盘序第③步：
// 正式存储失败时补偿删除已写的 orig（best-effort），仍然整单失败退款。
func TestImageGenerationSaveFailureCompensatesOrig(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedImageModel(t, g, channel.ID)
	stor := testutil.NewFakeStorage("local")
	wm := &stubWatermarker{}
	r, _ := newWatermarkRouter(t, g, cfg, stor, wm, func() bool { return true })
	user := testutil.CreateUser(t, g, "wmcomp@example.com", "wmcomp", "password123", true)
	seedGrantedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	// 把 free 档单文件上限压到与原始产物等大：水印字节必然超限，Save 必失败。
	if err := g.Model(&model.MembershipPlan{}).Where("id = ?", "free").Update("max_file_bytes", len("fake-png-data")).Error; err != nil {
		t.Fatalf("调整档位上限失败: %v", err)
	}

	w, _ := generateImage(t, r, token, "wm-comp-1")
	if w.Code != http.StatusInsufficientStorage || testutil.ErrorCode(t, w) != "STORAGE_QUOTA_EXCEEDED" {
		t.Fatalf("落盘失败应返回 507, got %d %s", w.Code, w.Body.String())
	}
	if len(stor.Objects) != 0 {
		t.Fatalf("补偿后不应残留任何对象: %v", stor.Objects)
	}
	wantOrig := user.ID.String() + "/orig/"
	found := false
	for _, deleted := range stor.Deleted {
		if len(deleted) > len(wantOrig) && deleted[:len(wantOrig)] == wantOrig {
			found = true
		}
	}
	if !found {
		t.Fatalf("应补偿删除 orig, deleted=%v", stor.Deleted)
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("落盘失败不应产生媒体行, got %d", mediaCount)
	}
	var request model.AIRequest
	if err := g.Where("user_id = ?", user.ID).First(&request).Error; err != nil {
		t.Fatalf("应写入生成请求行: %v", err)
	}
	if request.Status != "failed" {
		t.Fatalf("请求应收敛为 failed: %s", request.Status)
	}
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 1_000_000 {
		t.Fatalf("落盘失败应全额退还, granted=%d", balance.GrantedMicros)
	}
}
