package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/testutil"
)

// fakeVideoUpstream 模拟视频任务：创建返回任务 id，查询返回可下载的结果地址。
func fakeVideoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/videos", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "video-task-1"})
	})
	mux.HandleFunc("/v1/videos/video-task-1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "url": ""})
	})
	mux.HandleFunc("/v1/videos/video-task-1/content", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("fake-mp4-data"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func seedVideoModel(t *testing.T, g *gorm.DB, channelID uuid.UUID) model.ModelCatalog {
	t.Helper()
	channelIDs, _ := json.Marshal([]uuid.UUID{channelID})
	item := model.ModelCatalog{
		ID:          uuid.New(),
		Name:        "test-video",
		DisplayName: "测试视频",
		Capability:  "video",
		Provider:    "openai",
		Constraints: datatypes.JSON([]byte(`{"resolution":["480p","720p"],"duration":[4,6],"ratio":["16:9","9:16"],"features":["referenceImage"]}`)),
		CreditCost:  datatypes.JSON([]byte(`{"version":1,"dimensions":["resolution","duration"],"prices":[{"params":{"resolution":"480p","duration":"4"},"costMicros":400000},{"params":{"resolution":"480p","duration":"6"},"costMicros":600000},{"params":{"resolution":"720p","duration":"4"},"costMicros":800000},{"params":{"resolution":"720p","duration":"6"},"costMicros":1200000}]}`)),
		ChannelIDs:  channelIDs,
		Enabled:     true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入视频模型失败: %v", err)
	}
	return item
}

func newVideoTaskService(t *testing.T, g *gorm.DB, cfg *config.Config, router *gin.Engine) (*service.AITaskService, string) {
	t.Helper()
	cipher, err := cryptoForTest(t)
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	upstream := service.NewUpstreamService(g, cipher, service.DefaultUpstreamTimeouts())
	upstream.SetAllowPrivate(true)
	media := service.NewMediaWriteService(g, testutil.NewFakeStorage("local"))
	return service.NewAITaskService(g, upstream, media), ""
}

func TestVideoTaskLifecycleAndRefund(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeVideoUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedVideoModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "video@example.com", "videouser", "password123", true)
	seedCredits(t, g, user.ID, 5_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-video", "video", map[string]any{"resolution": "480p", "duration": "4", "ratio": "16:9"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/videos/generations", token, map[string]any{
		"model": "test-video", "prompt": "镜头推进", "resolution": "480p", "duration": 4, "ratio": "16:9",
		"quoteToken": quote["quoteToken"], "idempotencyKey": "video-1",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("创建视频任务失败: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	taskID, _ := body["taskId"].(string)
	if taskID == "" || body["status"] != "pending" {
		t.Fatalf("任务创建响应不完整: %v", body)
	}
	// 创建时就扣点，并写入一条 pending 的生成记录。
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 4_600_000 {
		t.Fatalf("创建任务时应扣点, 余额=%d", balance.PurchasedMicros)
	}
	var pending model.Generation
	if err := g.Where("user_id = ? AND kind = ? AND status = ?", user.ID, "video", "pending").First(&pending).Error; err != nil {
		t.Fatalf("应写入 pending 生成记录: %v", err)
	}

	// 后台轮询一次，任务收敛为成功并落盘。
	taskService, _ := newVideoTaskService(t, g, cfg, r)
	taskUUID, _ := uuid.Parse(taskID)
	var task model.AITask
	if err := g.First(&task, "id = ?", taskUUID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if err := taskService.PollTask(context.Background(), &task, time.Now()); err != nil {
		t.Fatalf("轮询任务失败: %v", err)
	}
	if task.Status != "succeeded" || task.StorageKey == "" {
		t.Fatalf("任务应收敛为 succeeded 并带 storageKey: %+v", task)
	}
	var generation model.Generation
	if err := g.First(&generation, "id = ?", task.GenerationID).Error; err != nil {
		t.Fatalf("读取生成记录失败: %v", err)
	}
	if generation.Status != "success" {
		t.Fatalf("生成记录应收敛为 success: %s", generation.Status)
	}

	// 查询接口返回任务与产物。
	query := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/ai/videos/tasks/"+taskID, token, nil)
	if query.Code != http.StatusOK {
		t.Fatalf("查询任务失败: %d %s", query.Code, query.Body.String())
	}
	view := testutil.DecodeBody(t, query)
	if view["status"] != "succeeded" || view["video"] == nil {
		t.Fatalf("查询应返回产物: %v", view)
	}
}

func TestVideoTaskFailureRefundsOnce(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	// 上游任务固定失败。
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "video-fail-1"})
	}))
	defer failing.Close()
	channel := seedPlatformChannel(t, g, failing.URL, "openai")
	seedVideoModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "videofail@example.com", "videofailuser", "password123", true)
	seedCredits(t, g, user.ID, 5_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-video", "video", map[string]any{"resolution": "480p", "duration": "4"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/videos/generations", token, map[string]any{
		"model": "test-video", "prompt": "会失败", "resolution": "480p", "duration": 4,
		"quoteToken": quote["quoteToken"], "idempotencyKey": "video-fail-1",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("创建任务失败: %d %s", w.Code, w.Body.String())
	}
	taskID := testutil.DecodeBody(t, w)["taskId"].(string)
	taskUUID, _ := uuid.Parse(taskID)
	var task model.AITask
	if err := g.First(&task, "id = ?", taskUUID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	// 把任务过期，触发超时失败分支。
	task.CreatedAt = time.Now().Add(-time.Hour)
	if err := g.Model(&model.AITask{}).Where("id = ?", task.ID).Update("created_at", task.CreatedAt).Error; err != nil {
		t.Fatalf("调整任务时间失败: %v", err)
	}

	taskService, _ := newVideoTaskService(t, g, cfg, r)
	if err := taskService.PollTask(context.Background(), &task, time.Now()); err != nil {
		t.Fatalf("轮询失败: %v", err)
	}
	// 再轮询一次：终态任务不再处理，也不会二次退款。
	if err := taskService.PollTask(context.Background(), &task, time.Now()); err != nil {
		t.Fatalf("重复轮询失败: %v", err)
	}
	balance, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 5_000_000 {
		t.Fatalf("失败应全额退还, 余额=%d", balance.PurchasedMicros)
	}
	var refunds int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, billing.TxTypeRefund).Count(&refunds)
	if refunds != 1 {
		t.Fatalf("退款流水应只有一条, got %d", refunds)
	}
}

func TestChatNonStreamAndRequestConvergence(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := testutil.FakeOpenAIUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	channelIDs, _ := json.Marshal([]uuid.UUID{channel.ID})
	textModel := model.ModelCatalog{
		ID: uuid.New(), Name: "test-text2", DisplayName: "文本", Capability: "text", Provider: "openai",
		Constraints: datatypes.JSON([]byte(`{}`)),
		CreditCost:  datatypes.JSON([]byte(`{"version":1,"dimensions":[],"prices":[{"params":{},"costMicros":20000}]}`)),
		ChannelIDs:  channelIDs,
		Enabled:     true,
	}
	if err := g.Create(&textModel).Error; err != nil {
		t.Fatalf("写入文本模型失败: %v", err)
	}
	r, _ := newAITestRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "chat2@example.com", "chatuser2", "password123", true)
	seedCredits(t, g, user.ID, 1_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-text2", "text", map[string]any{})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/chat/completions", token, map[string]any{
		"model": "test-text2", "messages": []map[string]any{{"role": "user", "content": "你好"}},
		"stream": false, "quoteToken": quote["quoteToken"], "idempotencyKey": "chat-nonstream-1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("非流式对话失败: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["content"] != "你好，世界" {
		t.Fatalf("内容错误: %v", body)
	}
	if body["credits"] == nil {
		t.Fatalf("响应应带计费快照: %v", body)
	}

	// 启动收敛：把一条请求改成超时的 running，应被置为 failed 并退款。
	balanceBefore, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	stale := &model.AIRequest{
		ID:              uuid.New(),
		UserID:          user.ID,
		Capability:      "image",
		Model:           "test-image",
		BaseCostMicros:  100000,
		FinalCostMicros: 100000,
		Status:          "running",
	}
	if err := g.Create(stale).Error; err != nil {
		t.Fatalf("写入滞留请求失败: %v", err)
	}
	if err := g.Model(&model.AIRequest{}).Where("id = ?", stale.ID).Update("updated_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}
	requestService := service.NewAIRequestService(g)
	converged, err := requestService.ConvergeStaleRunning(context.Background(), func(string) time.Duration {
		return time.Minute
	}, time.Now())
	if err != nil {
		t.Fatalf("收敛失败: %v", err)
	}
	if converged != 1 {
		t.Fatalf("应收敛一条滞留请求, got %d", converged)
	}
	var reloaded model.AIRequest
	g.First(&reloaded, "id = ?", stale.ID)
	if reloaded.Status != "failed" {
		t.Fatalf("滞留请求应置为 failed: %s", reloaded.Status)
	}
	balanceAfter, _ := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), user.ID)
	if balanceAfter.PurchasedMicros != balanceBefore.PurchasedMicros {
		t.Fatalf("无消费流水的请求不应改变余额")
	}
}

func cryptoForTest(t *testing.T) (*crypto.Cipher, error) {
	t.Helper()
	return crypto.New("0123456789abcdef0123456789abcdef")
}

func TestVideoGenerationRecordCarriesTaskHandle(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CredentialKey = "0123456789abcdef0123456789abcdef"
	upstream := fakeVideoUpstream(t)
	channel := seedPlatformChannel(t, g, upstream.URL, "openai")
	seedVideoModel(t, g, channel.ID)
	r, _ := newAITestRouter(t, g, cfg)
	user := testutil.CreateUser(t, g, "videohandle@example.com", "videohandle", "password123", true)
	seedCredits(t, g, user.ID, 5_000_000)
	token := testutil.AccessToken(t, cfg, &user)

	quote := mustQuote(t, r, token, "test-video", "video", map[string]any{"resolution": "480p", "duration": "4"})
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/ai/videos/generations", token, map[string]any{
		"model": "test-video", "prompt": "刷新续跑", "resolution": "480p", "duration": 4,
		"quoteToken": quote["quoteToken"], "idempotencyKey": "video-handle-1",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("创建任务失败: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	taskID := body["taskId"].(string)
	generationID := body["generationId"].(string)

	// 前端刷新后按生成记录恢复轮询：result.task 必须带 id 与 model。
	list := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?kind=video&status=pending", token, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("查询待处理生成记录失败: %d %s", list.Code, list.Body.String())
	}
	items := testutil.DecodeItems(t, list)
	if len(items) != 1 || items[0]["id"] != generationID {
		t.Fatalf("应有一条 pending 生成记录: %v", items)
	}
	result, _ := items[0]["result"].(map[string]any)
	task, _ := result["task"].(map[string]any)
	if task["id"] != taskID || task["model"] != "test-video" {
		t.Fatalf("生成记录应带任务句柄: %v", result)
	}

	// 任务收敛为成功后，任务句柄仍保留在结果里。
	taskService, _ := newVideoTaskService(t, g, cfg, r)
	taskUUID, _ := uuid.Parse(taskID)
	var stored model.AITask
	if err := g.First(&stored, "id = ?", taskUUID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if err := taskService.PollTask(context.Background(), &stored, time.Now()); err != nil {
		t.Fatalf("轮询失败: %v", err)
	}
	var generation model.Generation
	if err := g.First(&generation, "id = ?", uuid.MustParse(generationID)).Error; err != nil {
		t.Fatalf("读取生成记录失败: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(generation.Result, &parsed); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if parsed["task"] == nil || parsed["video"] == nil {
		t.Fatalf("收敛后应同时保留任务句柄与产物: %v", parsed)
	}
}
