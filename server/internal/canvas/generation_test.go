package canvas

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/testutil"
)

func seedGeneration(t *testing.T, g *gorm.DB, userID uuid.UUID, kind, status string, createdAt time.Time) model.Generation {
	t.Helper()
	gen := model.Generation{
		ID:               uuid.New(),
		UserID:           userID,
		Kind:             kind,
		Status:           status,
		Prompt:           "prompt-" + status,
		Model:            "test-model",
		DurationMs:       100,
		ModerationStatus: "skipped",
		CreatedAt:        createdAt,
	}
	if err := g.Create(&gen).Error; err != nil {
		t.Fatalf("写入生成记录失败: %v", err)
	}
	return gen
}

func encodeForTest(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func TestGenerationCursorPagination(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "gen@example.com", "genuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		seedGeneration(t, g, user.ID, "image", "success", base.Add(time.Duration(i)*time.Minute))
	}

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?size=2", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表失败: %d body=%s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	items := testutil.DecodeItems(t, w)
	if len(items) != 2 {
		t.Fatalf("第一页应有 2 条: %v", items)
	}
	if _, hasTotal := body["total"]; hasTotal {
		t.Fatal("生成记录列表不应返回 total")
	}
	cursor, _ := body["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("还有下一页时应返回 nextCursor")
	}

	seen := map[string]bool{}
	for _, item := range items {
		seen[item["id"].(string)] = true
	}
	// 跟随游标翻页，直到 nextCursor 为 null
	pages := 1
	for cursor != "" {
		w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?size=2&cursor="+cursor, token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("游标翻页失败: %d body=%s", w.Code, w.Body.String())
		}
		body = testutil.DecodeBody(t, w)
		items = testutil.DecodeItems(t, w)
		for _, item := range items {
			id := item["id"].(string)
			if seen[id] {
				t.Fatalf("翻页出现重复记录: %s", id)
			}
			seen[id] = true
		}
		cursor, _ = body["nextCursor"].(string)
		pages++
		if pages > 5 {
			t.Fatal("游标未收敛")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("应恰好取回 5 条, got %d", len(seen))
	}
	if pages != 3 {
		t.Fatalf("size=2 时 5 条记录应分 3 页, got %d", pages)
	}

	// 时间倒序
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?size=1", token, nil)
	items = testutil.DecodeItems(t, w)
	if items[0]["prompt"] != "prompt-success" {
		t.Fatalf("应按 created_at 倒序: %v", items[0])
	}
}

func TestGenerationPendingReturnsAllWithoutPagination(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "pending@example.com", "pendinguser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	base := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		seedGeneration(t, g, user.ID, "video", "pending", base.Add(time.Duration(i)*time.Minute))
	}
	seedGeneration(t, g, user.ID, "video", "success", base)
	seedGeneration(t, g, user.ID, "image", "pending", base)

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?status=pending", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pending 查询失败: %d body=%s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["nextCursor"] != nil {
		t.Fatalf("pending 查询 nextCursor 应恒为 null: %v", body["nextCursor"])
	}
	if items := testutil.DecodeItems(t, w); len(items) != 4 {
		t.Fatalf("pending 应一次返回全部 4 条: %d", len(items))
	}
	// kind 与 status 组合
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?kind=video&status=pending", token, nil)
	if items := testutil.DecodeItems(t, w); len(items) != 3 {
		t.Fatalf("video+pending 应有 3 条: %d", len(items))
	}
}

func TestGenerationPendingLimit(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "pendinglimit@example.com", "pendinglimit", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	batch := make([]model.Generation, 0, pendingLimit+5)
	for i := 0; i < pendingLimit+5; i++ {
		batch = append(batch, model.Generation{
			ID:               uuid.New(),
			UserID:           user.ID,
			Kind:             "video",
			Status:           "pending",
			Prompt:           "p",
			ModerationStatus: "skipped",
			CreatedAt:        base.Add(time.Duration(i) * time.Second),
		})
	}
	if err := g.CreateInBatches(&batch, 50).Error; err != nil {
		t.Fatalf("批量写入 pending 失败: %v", err)
	}

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations?status=pending", token, nil)
	if items := testutil.DecodeItems(t, w); len(items) != pendingLimit {
		t.Fatalf("pending 查询应截断到 %d 条, got %d", pendingLimit, len(items))
	}
}

func TestGenerationValidationAndCrossUser(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	owner := testutil.CreateUser(t, g, "genowner@example.com", "genowner", "password123", true)
	other := testutil.CreateUser(t, g, "genother@example.com", "genother", "password123", true)
	ownerToken := testutil.AccessToken(t, cfg, &owner)
	otherToken := testutil.AccessToken(t, cfg, &other)

	// 非法枚举与游标
	for _, path := range []string{
		"/api/v1/generations?kind=audio",
		"/api/v1/generations?status=queued",
		"/api/v1/generations?size=101",
		"/api/v1/generations?cursor=not-base64!!",
		"/api/v1/generations?cursor=" + encodeForTest("no-separator"),
		"/api/v1/generations?cursor=" + encodeForTest("2026-09-10T12:00:00Z|not-a-uuid"),
	} {
		w := testutil.DoAuthJSON(r, http.MethodGet, path, ownerToken, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400, got %d body=%s", path, w.Code, w.Body.String())
		}
		if code := testutil.ErrorCode(t, w); code != "VALIDATION_FAILED" {
			t.Fatalf("%s 错误码应为 VALIDATION_FAILED, got %s", path, code)
		}
	}

	gen := seedGeneration(t, g, owner.ID, "image", "success", time.Now())
	// 跨用户详情与删除 404
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations/"+gen.ID.String(), otherToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("跨用户详情应 404, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/generations/"+gen.ID.String(), otherToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("跨用户删除应 404, got %d", w.Code)
	}
	// 详情正常
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations/"+gen.ID.String(), ownerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("本人详情应 200, got %d", w.Code)
	}
	// 删除幂等，删除后列表不含
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/generations/"+gen.ID.String(), ownerToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除应 204, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/generations/"+gen.ID.String(), ownerToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("重复删除应 204, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations/"+gen.ID.String(), ownerToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, got %d", w.Code)
	}
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/generations", ownerToken, nil)
	if items := testutil.DecodeItems(t, w); len(items) != 0 {
		t.Fatalf("删除后列表应为空: %v", items)
	}
}

func TestGenerationNoWriteEndpoints(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "genwrite@example.com", "genwrite", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	if w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/generations", token, map[string]any{}); w.Code != http.StatusNotFound {
		t.Fatalf("不应存在 POST /api/generations, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/generations/"+uuid.NewString(), token, map[string]any{}); w.Code != http.StatusNotFound {
		t.Fatalf("不应存在 PATCH /api/generations/{id}, got %d", w.Code)
	}
}
