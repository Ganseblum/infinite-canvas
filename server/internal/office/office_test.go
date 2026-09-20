package office_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/office"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/testutil"
)

const officeToken = "office-test-token"

// fakeRuntime 是契约二假上游：B1 按 script 回放 SSE（release 非 nil 时先阻塞，模拟
// 排队中的 run）、B2 记录取消、B3 按 exists 表对账。
type fakeRuntime struct {
	mu          sync.Mutex
	starts      []office.StartRunRequest
	cancels     []string
	exists      map[string]bool
	script      []office.Envelope
	release     chan struct{}
	releaseOnce sync.Once
	srv         *httptest.Server
}

func newFakeRuntime(t *testing.T, script []office.Envelope, blocking bool) *fakeRuntime {
	t.Helper()
	rt := &fakeRuntime{script: script, exists: map[string]bool{}}
	if blocking {
		rt.release = make(chan struct{})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", rt.handleStart)
	mux.HandleFunc("POST /v1/runs/{id}/cancel", rt.handleCancel)
	mux.HandleFunc("GET /v1/runs/{id}", rt.handleStatus)
	rt.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		rt.releaseNow()
		rt.srv.Close()
	})
	return rt
}

func (f *fakeRuntime) releaseNow() {
	if f.release != nil {
		f.releaseOnce.Do(func() { close(f.release) })
	}
}

func (f *fakeRuntime) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeRuntime) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Office-Internal-Token") != officeToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req office.StartRunRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.starts = append(f.starts, req)
	f.mu.Unlock()
	if f.release != nil {
		select {
		case <-f.release:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)
	f.mu.Lock()
	script := append([]office.Envelope(nil), f.script...)
	f.mu.Unlock()
	for _, env := range script {
		raw, _ := json.Marshal(env)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
	}
}

func (f *fakeRuntime) handleCancel(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.cancels = append(f.cancels, r.PathValue("id"))
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (f *fakeRuntime) handleStatus(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	exists := f.exists[r.PathValue("id")]
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"exists": exists, "phase": "running"})
}

// ev 构造契约二事件。
func ev(typ string, payload map[string]any) office.Envelope {
	raw, _ := json.Marshal(payload)
	return office.Envelope{V: 1, Type: typ, Payload: raw}
}

// officeEnv 组装与生产同源的契约一路由 + 假 Runtime 上游。
type officeEnv struct {
	g      *gorm.DB
	cfg    *config.Config
	svc    *office.Service
	rt     *fakeRuntime
	router *gin.Engine
	user   model.PlatformUser
	token  string
}

func newOfficeEnv(t *testing.T, script []office.Envelope, blocking bool) *officeEnv {
	t.Helper()
	g := testutil.NewTestDB(t)
	if err := office.Migrate(g); err != nil {
		t.Fatalf("office 建表失败: %v", err)
	}
	rt := newFakeRuntime(t, script, blocking)
	svc := office.NewService(g, billing.NewService(g, model.ProductCanvas), office.RuntimeConfig{
		BaseURL: rt.srv.URL,
		Token:   officeToken,
	})
	user := testutil.CreateUser(t, g, "office-user@example.com", "officeuser", "password123", true)
	grantCredits(t, g, user.ID, 10_000_000)
	cfg := testutil.TestConfig()
	env := &officeEnv{g: g, cfg: cfg, svc: svc, rt: rt, user: user}
	env.router = testutil.NewRouter(t, func(r *gin.Engine) {
		office.MountOfficeRoutes(r.Group("/api/v1/office", middleware.Auth([]byte(cfg.JWTSecret))), office.NewOfficeHandler(svc))
	})
	env.token = testutil.AccessToken(t, cfg, &user)
	return env
}

func (e *officeEnv) api(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoAuthJSON(e.router, method, path, e.token, body)
}

func grantCredits(t *testing.T, g *gorm.DB, userID uuid.UUID, micros int64) {
	t.Helper()
	if _, err := billing.NewService(g, model.ProductCanvas).
		Adjust(g, userID, billing.BucketGranted, micros, "测试赠送", "test"); err != nil {
		t.Fatalf("写入测试点数失败: %v", err)
	}
}

func balanceOf(t *testing.T, g *gorm.DB, userID uuid.UUID) int64 {
	t.Helper()
	account, err := billing.NewService(g, model.ProductCanvas).Balance(context.Background(), userID)
	if err != nil {
		t.Fatalf("读取余额失败: %v", err)
	}
	return account.PurchasedMicros + account.GrantedMicros
}

// waitRunStatus 轮询等待 run 到达任一给定状态（编排协程异步落库）。
func waitRunStatus(t *testing.T, g *gorm.DB, runID string, want ...string) office.OfficeRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var run office.OfficeRun
		if err := g.First(&run, "id = ?", runID).Error; err == nil {
			for _, s := range want {
				if run.Status == s {
					return run
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s 未在期限内到达状态 %v", runID, want)
	return office.OfficeRun{}
}

func createSession(t *testing.T, e *officeEnv) string {
	t.Helper()
	w := e.api(t, http.MethodPost, "/api/v1/office/sessions", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建会话失败: %d %s", w.Code, w.Body.String())
	}
	return testutil.DecodeBody(t, w)["id"].(string)
}

func postMessage(t *testing.T, e *officeEnv, sessionID, content, clientMsgID string) *httptest.ResponseRecorder {
	t.Helper()
	return e.api(t, http.MethodPost, "/api/v1/office/sessions/"+sessionID+"/messages", map[string]any{
		"content": content, "clientMsgId": clientMsgID, "attachmentIds": []string{"f_ignored"},
	})
}

func countOf(t *testing.T, g *gorm.DB, dest any, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := g.Model(dest).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("计数查询失败: %v", err)
	}
	return count
}

// waitStartCount 轮询等待契约二收到 n 次起跑请求（编排协程异步发起）。
func waitStartCount(t *testing.T, rt *fakeRuntime, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rt.startCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("契约二起跑次数未达到 %d，实际 %d", n, rt.startCount())
}

// TestPostMessageIdempotent A5 同 clientMsgId 重放返回同 run，不重复建 run 与消息（E1）。
func TestPostMessageIdempotent(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	sessionID := createSession(t, env)

	first := postMessage(t, env, sessionID, "整理成本周周报", "cmid-1")
	if first.Code != http.StatusCreated {
		t.Fatalf("首发应 201: %d %s", first.Code, first.Body.String())
	}
	var body struct {
		RunID     string `json:"runId"`
		Status    string `json:"status"`
		ClientMsg string `json:"clientMsgId"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != office.RunQueued || body.ClientMsg != "cmid-1" {
		t.Fatalf("首发响应不符: %+v", body)
	}

	second := postMessage(t, env, sessionID, "整理成本周周报", "cmid-1")
	if second.Code != http.StatusOK {
		t.Fatalf("重放应 200: %d %s", second.Code, second.Body.String())
	}
	var replay struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.RunID != body.RunID {
		t.Fatalf("重放 run 不一致: %s != %s", replay.RunID, body.RunID)
	}
	if got := countOf(t, env.g, &office.OfficeRun{}, "session_id = ?", sessionID); got != 1 {
		t.Fatalf("run 应只有 1 条，实际 %d", got)
	}
	if got := countOf(t, env.g, &office.OfficeMessage{}, "session_id = ?", sessionID); got != 1 {
		t.Fatalf("消息应只有 1 条，实际 %d", got)
	}
	waitStartCount(t, env.rt, 1)
	if got := env.rt.startCount(); got != 1 {
		t.Fatalf("契约二应只起跑 1 次，实际 %d", got)
	}
}

// TestPostMessageConflict A5 活动期间不同 clientMsgId 提交返回 409 run_conflict（E2）。
func TestPostMessageConflict(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	sessionID := createSession(t, env)

	if code := postMessage(t, env, sessionID, "第一条", "cmid-1").Code; code != http.StatusCreated {
		t.Fatalf("首发应 201: %d", code)
	}
	w := postMessage(t, env, sessionID, "第二条", "cmid-2")
	if w.Code != http.StatusConflict {
		t.Fatalf("并发提交应 409: %d %s", w.Code, w.Body.String())
	}
	if code := testutil.ErrorCode(t, w); code != "run_conflict" {
		t.Fatalf("错误码应为 run_conflict，实际 %s", code)
	}
	if got := countOf(t, env.g, &office.OfficeMessage{}, "session_id = ?", sessionID); got != 1 {
		t.Fatalf("被拒消息不应落库，实际 %d", got)
	}
}

// TestEventsSeqReplayAndMaterialization 事件按 seq 连续落库、A6 重放、终态物化与扣点。
func TestEventsSeqReplayAndMaterialization(t *testing.T) {
	script := []office.Envelope{
		ev("run_started", map[string]any{"model": "glm-5.3", "agentName": "默认"}),
		ev("delta", map[string]any{"text": "你"}),
		ev("delta", map[string]any{"text": "好"}),
		ev("done", map[string]any{"inputTokens": float64(10), "outputTokens": float64(2000)}),
	}
	env := newOfficeEnv(t, script, false)
	sessionID := createSession(t, env)

	w := postMessage(t, env, sessionID, "打个招呼", "cmid-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("首发应 201: %d %s", w.Code, w.Body.String())
	}
	runID := testutil.DecodeBody(t, w)["runId"].(string)
	run := waitRunStatus(t, env.g, runID, office.RunSucceeded)

	// 事件 seq 连续且顺序与 script 一致。
	var events []office.OfficeEvent
	if err := env.g.Where("run_id = ?", runID).Order("seq ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{"run_started", "delta", "delta", "done"}
	if len(events) != len(wantTypes) {
		t.Fatalf("事件数应 %d，实际 %d", len(wantTypes), len(events))
	}
	for i, e := range events {
		if e.Seq != i+1 || e.Type != wantTypes[i] {
			t.Fatalf("事件 %d 应为 seq=%d type=%s，实际 seq=%d type=%s", i, i+1, wantTypes[i], e.Seq, e.Type)
		}
	}

	// 终态与并发闸清理。
	if run.TokensOut != 2000 || run.TokensIn != 10 {
		t.Fatalf("用量应 10/2000，实际 %d/%d", run.TokensIn, run.TokensOut)
	}
	if run.StartedAt == nil || run.FinishedAt == nil {
		t.Fatal("started_at/finished_at 应写入")
	}
	var session office.OfficeSession
	if err := env.g.First(&session, "id = ?", sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.ActiveRunID != nil {
		t.Fatalf("终态后 active_run_id 应清空，实际 %s", *session.ActiveRunID)
	}

	// assistant 消息物化：快照权威（spec §7-1）。
	var messages []office.OfficeMessage
	if err := env.g.Where("session_id = ?", sessionID).Order("created_at ASC").Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" {
		t.Fatalf("应物化 user+assistant 两条，实际 %+v", messages)
	}
	var assistantContent struct {
		SchemaVersion int    `json:"schema_version"`
		Text          string `json:"text"`
	}
	if err := json.Unmarshal(messages[1].Content, &assistantContent); err != nil {
		t.Fatal(err)
	}
	if assistantContent.Text != "你好" || assistantContent.SchemaVersion != 1 {
		t.Fatalf("物化内容不符: %+v", assistantContent)
	}
	if messages[1].ThreadID != sessionID || messages[1].TurnID != runID {
		t.Fatalf("归属键不符: thread=%s turn=%s", messages[1].ThreadID, messages[1].TurnID)
	}

	// 扣点：2000 token × 1 点/1000 = 2 点 = 2_000_000 微元（1 点 = 1e6 微元）。
	if got := balanceOf(t, env.g, env.user.ID); got != 10_000_000-2_000_000 {
		t.Fatalf("余额应扣 2 点（2_000_000 微元），实际 %d", got)
	}
	if !run.CreditsCharged || run.Credits == nil || *run.Credits != 2_000_000 {
		t.Fatalf("扣点幂等闸/金额不符: charged=%v credits=%v", run.CreditsCharged, run.Credits)
	}

	// A4 快照。
	snapshot := env.api(t, http.MethodGet, "/api/v1/office/sessions/"+sessionID+"/messages", nil)
	if snapshot.Code != http.StatusOK {
		t.Fatalf("快照应 200: %d", snapshot.Code)
	}
	items := testutil.DecodeItems(t, snapshot)
	if len(items) != 2 {
		t.Fatalf("快照应 2 条，实际 %d", len(items))
	}

	// A6 重放 lastSeq=1：seq 2..4，终态事件在末尾。
	replay := env.api(t, http.MethodGet, fmt.Sprintf("/api/v1/office/sessions/%s/runs/%s/stream?lastSeq=1", sessionID, runID), nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("重放应 200: %d", replay.Code)
	}
	body := replay.Body.String()
	if strings.Contains(body, `"type":"run_started"`) {
		t.Fatal("lastSeq=1 不应重放 seq=1")
	}
	if !strings.Contains(body, `"seq":2`) || !strings.Contains(body, `"seq":4`) || !strings.Contains(body, "event: done") {
		t.Fatalf("重放内容不符: %s", body)
	}
	// run 已终态：补发终态事件后收流（done 在最后）。
	if strings.Index(body, "event: done") < strings.LastIndex(body, "event: delta") {
		t.Fatal("done 应是最后一个事件")
	}
}

// TestCancelIdempotent A7 取消幂等：queued 强制置 cancelled，重复取消 200，之后可重试。
func TestCancelIdempotent(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	sessionID := createSession(t, env)

	w := postMessage(t, env, sessionID, "会被取消的任务", "cmid-1")
	runID := testutil.DecodeBody(t, w)["runId"].(string)

	first := env.api(t, http.MethodPost, "/api/v1/office/runs/"+runID+"/cancel", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("取消应 200: %d %s", first.Code, first.Body.String())
	}
	second := env.api(t, http.MethodPost, "/api/v1/office/runs/"+runID+"/cancel", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("重复取消应幂等 200: %d", second.Code)
	}

	var run office.OfficeRun
	if err := env.g.First(&run, "id = ?", runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != office.RunCancelled || run.ErrorCode == nil || *run.ErrorCode != "cancelled" {
		t.Fatalf("run 应 cancelled，实际 %s/%v", run.Status, run.ErrorCode)
	}
	var session office.OfficeSession
	if err := env.g.First(&session, "id = ?", sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.ActiveRunID != nil {
		t.Fatal("取消后 active_run_id 应清空")
	}
	// 合成 error(cancelled) 事件恰好一条（终态后 Runtime 事件被丢弃）。
	if got := countOf(t, env.g, &office.OfficeEvent{}, "run_id = ? AND type = ?", runID, "error"); got != 1 {
		t.Fatalf("error 事件应 1 条，实际 %d", got)
	}
	// 取消后可重试（dirty history 语义）。
	if code := postMessage(t, env, sessionID, "重试的新任务", "cmid-2").Code; code != http.StatusCreated {
		t.Fatalf("取消后新提交应 201: %d", code)
	}
}

// TestCancelTerminalRunsIdempotent 对终态 run 取消返回 200 且不追加事件。
func TestCancelTerminalRunsIdempotent(t *testing.T) {
	script := []office.Envelope{
		ev("run_started", map[string]any{}),
		ev("done", map[string]any{"outputTokens": float64(0)}),
	}
	env := newOfficeEnv(t, script, false)
	sessionID := createSession(t, env)
	w := postMessage(t, env, sessionID, "正常完成", "cmid-1")
	runID := testutil.DecodeBody(t, w)["runId"].(string)
	waitRunStatus(t, env.g, runID, office.RunSucceeded)

	before := countOf(t, env.g, &office.OfficeEvent{}, "run_id = ?", runID)
	if code := env.api(t, http.MethodPost, "/api/v1/office/runs/"+runID+"/cancel", nil).Code; code != http.StatusOK {
		t.Fatalf("终态取消应幂等 200: %d", code)
	}
	var run office.OfficeRun
	if err := env.g.First(&run, "id = ?", runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != office.RunSucceeded {
		t.Fatalf("终态不应被改写，实际 %s", run.Status)
	}
	if after := countOf(t, env.g, &office.OfficeEvent{}, "run_id = ?", runID); after != before {
		t.Fatalf("终态取消不应追加事件: %d -> %d", before, after)
	}
}

// TestOrphanReclaim 孤儿扫描（E13）：Runtime 无记录 → failed(orphan_reclaimed)；
// 仍有记录 → 跳过。TTL 注入为 30ms。
func TestOrphanReclaim(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	env.svc.OrphanTTL = 30 * time.Millisecond
	sessionID := createSession(t, env)

	w := postMessage(t, env, sessionID, "会被回收的任务", "cmid-1")
	runID := testutil.DecodeBody(t, w)["runId"].(string)

	// 未过 TTL：不回收。
	if n, err := env.svc.ScanOrphans(context.Background()); err != nil || n != 0 {
		t.Fatalf("TTL 内不应回收: n=%d err=%v", n, err)
	}
	if err := env.g.Model(&office.OfficeRun{}).Where("id = ?", runID).
		UpdateColumn("updated_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	// Runtime 无记录（exists=false 默认）→ 回收。
	n, err := env.svc.ScanOrphans(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("应回收 1 条: n=%d err=%v", n, err)
	}
	var run office.OfficeRun
	if err := env.g.First(&run, "id = ?", runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != office.RunFailed || run.ErrorCode == nil || *run.ErrorCode != "orphan_reclaimed" {
		t.Fatalf("应 failed(orphan_reclaimed)，实际 %s/%v", run.Status, run.ErrorCode)
	}
	if got := countOf(t, env.g, &office.OfficeEvent{}, "run_id = ? AND type = ?", runID, "error"); got != 1 {
		t.Fatalf("回收应落 error 事件，实际 %d", got)
	}
	// 回收后用户可重试。
	if code := postMessage(t, env, sessionID, "重试", "cmid-2").Code; code != http.StatusCreated {
		t.Fatalf("回收后新提交应 201: %d", code)
	}

	// Runtime 仍有记录的 run 不回收（M1 裁决：续转留 M4+）。用独立会话：
	// 原会话被 cmid-2 的排队 run 占着并发闸，再提交只会 409。
	sessionID2 := createSession(t, env)
	w2 := postMessage(t, env, sessionID2, "Runtime 还在跑", "cmid-3")
	runID2 := testutil.DecodeBody(t, w2)["runId"].(string)
	env.rt.mu.Lock()
	env.rt.exists[runID2] = true
	env.rt.mu.Unlock()
	if err := env.g.Model(&office.OfficeRun{}).Where("id = ?", runID2).
		UpdateColumn("updated_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := env.svc.ScanOrphans(context.Background()); err != nil || n != 0 {
		t.Fatalf("Runtime 在跑的 run 不应回收: n=%d err=%v", n, err)
	}
	var run2 office.OfficeRun
	if err := env.g.First(&run2, "id = ?", runID2).Error; err != nil {
		t.Fatal(err)
	}
	if run2.Status != office.RunQueued {
		t.Fatalf("仍在跑的 run 状态不应变，实际 %s", run2.Status)
	}
}

// TestRuntimeUnreachable E3：起跑失败 → failed(runtime_unreachable)、清并发闸、可重试。
func TestRuntimeUnreachable(t *testing.T) {
	g := testutil.NewTestDB(t)
	if err := office.Migrate(g); err != nil {
		t.Fatal(err)
	}
	dead := httptest.NewServer(http.NewServeMux())
	deadURL := dead.URL
	dead.Close() // 立即关闭：指向一个拒绝连接的端口

	svc := office.NewService(g, billing.NewService(g, model.ProductCanvas), office.RuntimeConfig{
		BaseURL: deadURL, Token: officeToken,
	})
	user := testutil.CreateUser(t, g, "office-dead@example.com", "officedead", "password123", true)
	grantCredits(t, g, user.ID, 10_000_000)
	cfg := testutil.TestConfig()
	env := &officeEnv{g: g, cfg: cfg, svc: svc, router: testutil.NewRouter(t, func(r *gin.Engine) {
		office.MountOfficeRoutes(r.Group("/api/v1/office", middleware.Auth([]byte(cfg.JWTSecret))), office.NewOfficeHandler(svc))
	}), user: user}
	env.token = testutil.AccessToken(t, cfg, &user)

	sessionID := createSession(t, env)
	w := postMessage(t, env, sessionID, "上游不可达", "cmid-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("起跑失败也应先 201（run 已排队）: %d", w.Code)
	}
	runID := testutil.DecodeBody(t, w)["runId"].(string)
	run := waitRunStatus(t, g, runID, office.RunFailed)
	if run.ErrorCode == nil || *run.ErrorCode != "runtime_unreachable" {
		t.Fatalf("错误码应 runtime_unreachable，实际 %v", run.ErrorCode)
	}
	var session office.OfficeSession
	if err := g.First(&session, "id = ?", sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.ActiveRunID != nil {
		t.Fatal("起跑失败后 active_run_id 应清空")
	}
	if code := postMessage(t, env, sessionID, "重试", "cmid-2").Code; code != http.StatusCreated {
		t.Fatalf("失败后可重试: %d", code)
	}
}

// TestStreamGapAndBadLastSeq E11：lastSeq 落在已清理区间 → 410 events_expired；非法值 400。
func TestStreamGapAndBadLastSeq(t *testing.T) {
	script := []office.Envelope{
		ev("run_started", map[string]any{}),
		ev("delta", map[string]any{"text": "a"}),
		ev("delta", map[string]any{"text": "b"}),
		ev("done", map[string]any{}),
	}
	env := newOfficeEnv(t, script, false)
	sessionID := createSession(t, env)
	w := postMessage(t, env, sessionID, "重放空洞", "cmid-1")
	runID := testutil.DecodeBody(t, w)["runId"].(string)
	waitRunStatus(t, env.g, runID, office.RunSucceeded)

	// 模拟保留期清理：删掉 seq<=2，制造「lastSeq=1 落在已清理区间」。
	if err := env.g.Where("run_id = ? AND seq <= ?", runID, 2).Delete(&office.OfficeEvent{}).Error; err != nil {
		t.Fatal(err)
	}
	gap := env.api(t, http.MethodGet, fmt.Sprintf("/api/v1/office/sessions/%s/runs/%s/stream?lastSeq=1", sessionID, runID), nil)
	if gap.Code != http.StatusGone {
		t.Fatalf("重放空洞应 410: %d %s", gap.Code, gap.Body.String())
	}
	if code := testutil.ErrorCode(t, gap); code != "events_expired" {
		t.Fatalf("错误码应 events_expired，实际 %s", code)
	}
	bad := env.api(t, http.MethodGet, fmt.Sprintf("/api/v1/office/sessions/%s/runs/%s/stream?lastSeq=abc", sessionID, runID), nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("非法 lastSeq 应 400: %d", bad.Code)
	}
	// 未带 lastSeq：重放现存全部（终态即收流）。
	full := env.api(t, http.MethodGet, fmt.Sprintf("/api/v1/office/sessions/%s/runs/%s/stream", sessionID, runID), nil)
	if full.Code != http.StatusOK || !strings.Contains(full.Body.String(), "event: done") {
		t.Fatalf("无 lastSeq 应全量重放并补发终态: %d %s", full.Code, full.Body.String())
	}
}

// TestOfficeAuthz 未登录 401；越权访问他人会话/run 一律 404（不泄露存在性）。
func TestOfficeAuthz(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	sessionID := createSession(t, env)
	w := postMessage(t, env, sessionID, "机密任务", "cmid-1")
	runID := testutil.DecodeBody(t, w)["runId"].(string)

	anon := testutil.DoJSON(env.router, http.MethodGet, "/api/v1/office/sessions", nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401: %d", anon.Code)
	}

	other := testutil.CreateUser(t, env.g, "office-other@example.com", "officeother", "password123", true)
	otherTk := testutil.AccessToken(t, env.cfg, &other)
	if code := testutil.DoAuthJSON(env.router, http.MethodGet, "/api/v1/office/sessions/"+sessionID, otherTk, nil).Code; code != http.StatusNotFound {
		t.Fatalf("越权读会话应 404: %d", code)
	}
	if code := testutil.DoAuthJSON(env.router, http.MethodPost, "/api/v1/office/runs/"+runID+"/cancel", otherTk, nil).Code; code != http.StatusNotFound {
		t.Fatalf("越权取消应 404: %d", code)
	}
	stream := fmt.Sprintf("/api/v1/office/sessions/%s/runs/%s/stream", sessionID, runID)
	if code := testutil.DoAuthJSON(env.router, http.MethodGet, stream, otherTk, nil).Code; code != http.StatusNotFound {
		t.Fatalf("越权订阅应 404: %d", code)
	}
}

// TestLiveBroadcastOnCancel A6 实时通道：订阅中的连接收到 cancel 合成终态事件后收流（E7 不受断开影响）。
func TestLiveBroadcastOnCancel(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	sessionID := createSession(t, env)
	w := postMessage(t, env, sessionID, "边跑边看", "cmid-1")
	runID := testutil.DecodeBody(t, w)["runId"].(string)

	// 真实 HTTP 服务承载 SSE（httptest.Recorder 无法测增量流）。
	srv := httptest.NewServer(env.router)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/office/sessions/%s/runs/%s/stream", srv.URL, sessionID, runID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 应 200: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type 应为 text/event-stream，实际 %s", ct)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		env.api(t, http.MethodPost, "/api/v1/office/runs/"+runID+"/cancel", nil)
	}()

	scanner := bufio.NewScanner(resp.Body)
	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataLine = strings.TrimPrefix(line, "data:")
			break // 首个事件应为 cancel 合成的 error(cancelled)
		}
	}
	if dataLine == "" {
		t.Fatal("未收到实时事件")
	}
	var env2 struct {
		Type    string `json:"type"`
		Seq     int    `json:"seq"`
		Payload struct {
			Code string `json:"code"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(dataLine), &env2); err != nil {
		t.Fatal(err)
	}
	if env2.Type != "error" || env2.Payload.Code != "cancelled" || env2.Seq != 1 {
		t.Fatalf("实时事件应为 seq=1 error(cancelled)，实际 %+v", env2)
	}
	cancel() // 断开订阅连接，handler 应随之返回
}

// TestSessionListPagination A2 按 updated_at 倒序 + cursor 分页。
func TestSessionListPagination(t *testing.T) {
	env := newOfficeEnv(t, nil, true)
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, createSession(t, env))
		time.Sleep(2 * time.Millisecond) // 保证 updated_at 可分辨
	}
	page1 := testutil.DecodeBody(t, env.api(t, http.MethodGet, "/api/v1/office/sessions?limit=2", nil))
	items := page1["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("limit=2 应返回 2 条，实际 %d", len(items))
	}
	cursor, _ := page1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("应存在 next_cursor")
	}
	page2 := testutil.DecodeBody(t, env.api(t, http.MethodGet, "/api/v1/office/sessions?limit=2&cursor="+cursor, nil))
	items2 := page2["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("第二页应 1 条，实际 %d", len(items2))
	}
	first := items[0].(map[string]any)
	if first["id"].(string) != ids[2] {
		t.Fatalf("列表应 updated_at 倒序，实际 %v", first["id"])
	}
}
