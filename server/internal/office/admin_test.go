package office_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/office"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/testutil"
)

// newOfficeAdminEnv 组装与生产一致的管理面中间件链（Auth → RequireActiveUser →
// LoadAdminAccess → RequirePermission），handler 用 internal/office 的 AdminHandler。
func newOfficeAdminEnv(t *testing.T) (*gorm.DB, *gin.Engine, *config.Config) {
	t.Helper()
	g := testutil.NewTestDB(t)
	if err := office.Migrate(g); err != nil {
		t.Fatalf("office 建表失败: %v", err)
	}
	cfg := testutil.TestConfig()
	identitySvc := identity.NewService(g)
	r := testutil.NewRouter(t, func(r *gin.Engine) {
		h := office.NewAdminHandler(g)
		group := r.Group("/api/admin",
			middleware.Auth([]byte(cfg.JWTSecret)),
			middleware.RequireActiveUser(identitySvc),
			middleware.LoadAdminAccess(identitySvc, g))
		group.GET("/office/sessions", middleware.RequirePermission(authz.PermOfficeRead), h.ListSessions)
		group.GET("/office/sessions/:id", middleware.RequirePermission(authz.PermOfficeRead), h.GetSession)
		group.GET("/office/stats", middleware.RequirePermission(authz.PermOfficeRead), h.Stats)
	})
	return g, r, cfg
}

// adminSeed* 直接写库构造会话/run/消息/事件夹具（不经过契约一接口，便于精确控制时间窗口）。

func adminSeedSession(t *testing.T, g *gorm.DB, id, userID, title string, createdAt, updatedAt time.Time) office.OfficeSession {
	t.Helper()
	sess := office.OfficeSession{
		ID: id, UserID: userID, Title: title, WorkspacePath: id, Status: "active",
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if err := g.Create(&sess).Error; err != nil {
		t.Fatalf("写入办公会话失败: %v", err)
	}
	return sess
}

func adminSeedRun(t *testing.T, g *gorm.DB, id, sessionID, status, modelName string, createdAt time.Time) office.OfficeRun {
	t.Helper()
	run := office.OfficeRun{
		ID: id, SessionID: sessionID, Status: status, Model: modelName,
		ClientMsgID: "cmid-" + id, CreatedAt: createdAt,
	}
	if err := g.Create(&run).Error; err != nil {
		t.Fatalf("写入办公 run 失败: %v", err)
	}
	return run
}

func adminSeedMessage(t *testing.T, g *gorm.DB, id, sessionID, runID, role, text string, createdAt time.Time) office.OfficeMessage {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "text": text})
	msg := office.OfficeMessage{
		ID: id, SessionID: sessionID, RunID: runID, Role: role,
		Content: datatypes.JSON(raw), ThreadID: sessionID, TurnID: runID, CreatedAt: createdAt,
	}
	if err := g.Create(&msg).Error; err != nil {
		t.Fatalf("写入办公消息失败: %v", err)
	}
	return msg
}

func adminSeedEvent(t *testing.T, g *gorm.DB, runID string, seq int, typ string, payload map[string]any, createdAt time.Time) office.OfficeEvent {
	t.Helper()
	raw, _ := json.Marshal(payload)
	event := office.OfficeEvent{RunID: runID, Seq: seq, Type: typ, Payload: datatypes.JSON(raw), CreatedAt: createdAt}
	if err := g.Create(&event).Error; err != nil {
		t.Fatalf("写入办公事件失败: %v", err)
	}
	return event
}

func adminLogin(t *testing.T, g *gorm.DB, cfg *config.Config) string {
	t.Helper()
	user := testutil.CreateUser(t, g, "admin-office@example.com", "adminoffice", "password123", true)
	testutil.PromoteAdmin(t, g, &user)
	return testutil.AccessToken(t, cfg, &user)
}

// TestAdminSessionsListFilterAndCursor 列表：updated_at 倒序、userId/status 筛选、
// 游标分页、userEmail/messagesCount/lastRunStatus 聚合列与非法参数 400。
func TestAdminSessionsListFilterAndCursor(t *testing.T) {
	g, r, cfg := newOfficeAdminEnv(t)
	token := adminLogin(t, g, cfg)

	u1 := testutil.CreateUser(t, g, "u1@example.com", "u1user", "password123", true)
	u2 := testutil.CreateUser(t, g, "u2@example.com", "u2user", "password123", true)
	now := time.Now()
	sA := adminSeedSession(t, g, "s-adm-sess-01", u1.ID.String(), "会话一", now.Add(-2*time.Hour), now.Add(-1*time.Hour))
	sB := adminSeedSession(t, g, "s-adm-sess-02", u1.ID.String(), "会话二", now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	sC := adminSeedSession(t, g, "s-adm-sess-03", u2.ID.String(), "会话三", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	adminSeedMessage(t, g, "m-adm-sess-01", sA.ID, "r-adm-sess-01", "user", "第一条", now.Add(-30*time.Minute))
	adminSeedMessage(t, g, "m-adm-sess-02", sA.ID, "r-adm-sess-01", "assistant", "回复", now.Add(-29*time.Minute))
	adminSeedRun(t, g, "r-adm-sess-01", sC.ID, office.RunSucceeded, "model-x", now.Add(-90*time.Minute))

	// 全量列表：updated_at 倒序 sA(-1h) > sC(-2h) > sB(-3h)，聚合列正确。
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表应 200: %d %s", w.Code, w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	if len(items) != 3 || items[0]["id"] != sA.ID || items[1]["id"] != sC.ID || items[2]["id"] != sB.ID {
		t.Fatalf("列表顺序不符: %v", items)
	}
	if items[0]["userEmail"] != u1.Email || items[2]["userEmail"] != u1.Email || items[1]["userEmail"] != u2.Email {
		t.Fatalf("userEmail 应来自平台用户表: %v", items)
	}
	if items[0]["messagesCount"] != float64(2) || items[1]["messagesCount"] != float64(0) {
		t.Fatalf("messagesCount 子查询不符: %v", items)
	}
	if items[0]["lastRunStatus"] != nil || items[1]["lastRunStatus"] != office.RunSucceeded {
		t.Fatalf("lastRunStatus 应无 run 为 null、有 run 为最新状态: %v", items)
	}

	// userId 筛选。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions?userId="+u1.ID.String(), token, nil)
	items = testutil.DecodeItems(t, w)
	if len(items) != 2 || items[0]["id"] != sA.ID || items[1]["id"] != sB.ID {
		t.Fatalf("userId 筛选不符: %v", items)
	}

	// status 筛选。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions?status=active", token, nil)
	if len(testutil.DecodeItems(t, w)) != 3 {
		t.Fatal("status=active 应命中全部 3 条")
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions?status=archived", token, nil)
	if len(testutil.DecodeItems(t, w)) != 0 {
		t.Fatal("status=archived 应命中 0 条")
	}

	// 游标分页：limit=1 逐页取完，末页 next_cursor 为空。
	cursor := ""
	seen := make([]string, 0, 3)
	for page := 0; page < 4; page++ {
		path := "/api/admin/office/sessions?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		body := testutil.DecodeBody(t, testutil.DoAuthJSON(r, http.MethodGet, path, token, nil))
		pageItems := body["items"].([]any)
		if len(pageItems) != 1 {
			t.Fatalf("每页应 1 条: %v", pageItems)
		}
		seen = append(seen, pageItems[0].(map[string]any)["id"].(string))
		cursor, _ = body["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 || seen[0] != sA.ID || seen[1] != sC.ID || seen[2] != sB.ID {
		t.Fatalf("游标翻页顺序不符: %v", seen)
	}

	// 非法参数 400。
	for _, path := range []string{
		"/api/admin/office/sessions?userId=not-a-uuid",
		"/api/admin/office/sessions?cursor=@@bad@@",
		"/api/admin/office/sessions?limit=0",
	} {
		if code := testutil.DoAuthJSON(r, http.MethodGet, path, token, nil).Code; code != http.StatusBadRequest {
			t.Fatalf("%s 应 400: %d", path, code)
		}
	}
}

// TestAdminSessionDetail 详情：会话全字段 + 全部消息按 created_at 升序；不存在 404。
func TestAdminSessionDetail(t *testing.T) {
	g, r, cfg := newOfficeAdminEnv(t)
	token := adminLogin(t, g, cfg)

	u1 := testutil.CreateUser(t, g, "detail-user@example.com", "detailuser", "password123", true)
	now := time.Now()
	sA := adminSeedSession(t, g, "s-adm-detail-01", u1.ID.String(), "详情会话", now.Add(-time.Hour), now.Add(-30*time.Minute))
	adminSeedRun(t, g, "r-adm-detail-01", sA.ID, office.RunFailed, "model-x", now.Add(-40*time.Minute))
	adminSeedMessage(t, g, "m-adm-detail-02", sA.ID, "r-adm-detail-01", "assistant", "回答", now.Add(-5*time.Minute))
	adminSeedMessage(t, g, "m-adm-detail-01", sA.ID, "r-adm-detail-01", "user", "提问", now.Add(-10*time.Minute))

	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions/"+sA.ID, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("详情应 200: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["id"] != sA.ID || body["title"] != "详情会话" || body["userId"] != u1.ID.String() ||
		body["userEmail"] != u1.Email || body["lastRunStatus"] != office.RunFailed {
		t.Fatalf("会话字段不符: %v", body)
	}
	messages := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("应含 2 条消息: %v", messages)
	}
	first := messages[0].(map[string]any)
	second := messages[1].(map[string]any)
	if first["id"] != "m-adm-detail-01" || second["id"] != "m-adm-detail-02" {
		t.Fatalf("消息应按 createdAt 升序: %v", messages)
	}
	if first["role"] != "user" || first["threadId"] != sA.ID || first["turnId"] != "r-adm-detail-01" {
		t.Fatalf("消息归属键不符: %v", first)
	}
	// content 按存储 JSON 原样返回，解码后是对象而非字符串。
	content, ok := first["content"].(map[string]any)
	if !ok || content["text"] != "提问" || content["schema_version"] != float64(1) {
		t.Fatalf("消息内容不符: %v", first["content"])
	}

	if code := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions/s-not-exist", token, nil).Code; code != http.StatusNotFound {
		t.Fatalf("不存在会话应 404: %d", code)
	}
}

// TestAdminStatsAggregation 统计聚合：3 用户、5+2 条 run 跨状态/模型/时间窗口、
// 2 条 tool_call 事件 + 1 条无 name 的 delta 事件，逐一断言各聚合值与窗口过滤。
func TestAdminStatsAggregation(t *testing.T) {
	g, r, cfg := newOfficeAdminEnv(t)
	token := adminLogin(t, g, cfg)

	u1 := testutil.CreateUser(t, g, "stat-u1@example.com", "statu1", "password123", true)
	u2 := testutil.CreateUser(t, g, "stat-u2@example.com", "statu2", "password123", true)
	u3 := testutil.CreateUser(t, g, "stat-u3@example.com", "statu3", "password123", true)
	now := time.Now()
	sA := adminSeedSession(t, g, "s-adm-stat-01", u1.ID.String(), "统计A", now, now)
	sB := adminSeedSession(t, g, "s-adm-stat-02", u1.ID.String(), "统计B", now, now)
	sC := adminSeedSession(t, g, "s-adm-stat-03", u2.ID.String(), "统计C", now, now)
	sD := adminSeedSession(t, g, "s-adm-stat-04", u3.ID.String(), "统计D", now, now)

	// 5 条近期 run：五种状态各一；model-a 3 次、model-b 2 次。活跃用户 u1、u2。
	adminSeedRun(t, g, "r-adm-stat-01", sA.ID, office.RunSucceeded, "model-a", now.Add(-time.Hour))
	adminSeedRun(t, g, "r-adm-stat-02", sA.ID, office.RunFailed, "model-a", now.Add(-time.Hour))
	adminSeedRun(t, g, "r-adm-stat-03", sB.ID, office.RunCancelled, "model-b", now.Add(-time.Hour))
	adminSeedRun(t, g, "r-adm-stat-04", sC.ID, office.RunRunning, "model-b", now.Add(-time.Hour))
	adminSeedRun(t, g, "r-adm-stat-05", sC.ID, office.RunQueued, "model-a", now.Add(-time.Hour))
	// 窗口外 run：10 天前（30 天窗口内）、40 天前（全部窗口外）。
	adminSeedRun(t, g, "r-adm-stat-06", sD.ID, office.RunSucceeded, "model-old", now.Add(-10*24*time.Hour))
	adminSeedRun(t, g, "r-adm-stat-07", sD.ID, office.RunFailed, "model-ancient", now.Add(-40*24*time.Hour))

	// 消息：u1 2 条、u2 1 条在 7 天窗口内；u3 1 条在 10 天前。
	adminSeedMessage(t, g, "m-adm-stat-01", sA.ID, "r-adm-stat-01", "user", "问一", now.Add(-time.Hour))
	adminSeedMessage(t, g, "m-adm-stat-02", sA.ID, "r-adm-stat-01", "assistant", "答一", now.Add(-time.Hour))
	adminSeedMessage(t, g, "m-adm-stat-03", sC.ID, "r-adm-stat-04", "user", "问二", now.Add(-time.Hour))
	adminSeedMessage(t, g, "m-adm-stat-04", sD.ID, "r-adm-stat-06", "user", "旧消息", now.Add(-10*24*time.Hour))

	// 事件：r1 上两次同名 tool_call（聚合为 count=2）+ 一条无 name 的 delta（不计入）；
	// 40 天前 run 上的一次 tool_call 不进任何窗口。
	adminSeedEvent(t, g, "r-adm-stat-01", 1, "tool_call", map[string]any{"toolCallId": "t1", "name": "web_search", "status": "running"}, now.Add(-time.Hour))
	adminSeedEvent(t, g, "r-adm-stat-01", 2, "tool_call", map[string]any{"toolCallId": "t2", "name": "web_search", "status": "running"}, now.Add(-time.Hour))
	adminSeedEvent(t, g, "r-adm-stat-01", 3, "delta", map[string]any{"text": "hi"}, now.Add(-time.Hour))
	adminSeedEvent(t, g, "r-adm-stat-07", 1, "tool_call", map[string]any{"toolCallId": "t3", "name": "old_tool", "status": "running"}, now.Add(-40*24*time.Hour))

	// 默认 days=7（r6/r7/m4 均不在窗口内）。
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/stats", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("统计应 200: %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["runsTotal"] != float64(5) || body["messagesTotal"] != float64(3) || body["sessionsTotal"] != float64(4) || body["activeUsers"] != float64(2) {
		t.Fatalf("7 天总览不符: %v", body)
	}
	byStatus := body["runsByStatus"].(map[string]any)
	for _, status := range []string{office.RunQueued, office.RunRunning, office.RunSucceeded, office.RunFailed, office.RunCancelled} {
		if byStatus[status] != float64(1) {
			t.Fatalf("runsByStatus.%s 应为 1: %v", status, byStatus)
		}
	}
	byModel := body["runsByModel"].([]any)
	if len(byModel) != 2 || byModel[0].(map[string]any)["model"] != "model-a" || byModel[0].(map[string]any)["count"] != float64(3) ||
		byModel[1].(map[string]any)["model"] != "model-b" || byModel[1].(map[string]any)["count"] != float64(2) {
		t.Fatalf("runsByModel 不符: %v", byModel)
	}
	topUsers := body["topUsers"].([]any)
	if len(topUsers) != 2 {
		t.Fatalf("7 天 topUsers 应 2 人: %v", topUsers)
	}
	first := topUsers[0].(map[string]any)
	if first["userId"] != u1.ID.String() || first["email"] != u1.Email || first["runs"] != float64(3) || first["messages"] != float64(2) {
		t.Fatalf("topUsers 首位应为 u1(runs=3, messages=2): %v", first)
	}
	second := topUsers[1].(map[string]any)
	if second["userId"] != u2.ID.String() || second["runs"] != float64(2) || second["messages"] != float64(1) {
		t.Fatalf("topUsers 第二位应为 u2(runs=2, messages=1): %v", second)
	}
	toolCalls := body["toolCalls"].([]any)
	if len(toolCalls) != 1 || toolCalls[0].(map[string]any)["name"] != "web_search" || toolCalls[0].(map[string]any)["count"] != float64(2) {
		t.Fatalf("toolCalls 应聚合 web_search=2（delta 无 name 不计入）: %v", toolCalls)
	}

	// days=30：r6/r7、m4 进入窗口，活跃用户增加 u3。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/stats?days=30", token, nil)
	body = testutil.DecodeBody(t, w)
	if body["runsTotal"] != float64(6) || body["messagesTotal"] != float64(4) || body["activeUsers"] != float64(3) {
		t.Fatalf("30 天总览不符: %v", body)
	}
	byModel = body["runsByModel"].([]any)
	if len(byModel) != 3 || byModel[2].(map[string]any)["model"] != "model-old" || byModel[2].(map[string]any)["count"] != float64(1) {
		t.Fatalf("30 天 runsByModel 应含 model-a/model-b/model-old（40 天前的 model-ancient 仍排除）: %v", byModel)
	}
	topUsers = body["topUsers"].([]any)
	if len(topUsers) != 3 {
		t.Fatalf("30 天 topUsers 应 3 人: %v", topUsers)
	}
	toolCalls = body["toolCalls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("40 天前的 tool_call 不应进入 30 天窗口: %v", toolCalls)
	}

	// 非法 days 400。
	if code := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/stats?days=5", token, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("days=5 应 400: %d", code)
	}
	if code := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/stats?days=abc", token, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("days=abc 应 400: %d", code)
	}
}

// TestAdminOfficeForbidden 无后台角色 / 只有 feedback 权限的 support 角色 / 匿名，
// 三类调用者在全部 office 管理端点上都必须被拒（support 的 is_system 不得放大成全量）。
func TestAdminOfficeForbidden(t *testing.T) {
	g, r, cfg := newOfficeAdminEnv(t)
	adminToken := adminLogin(t, g, cfg)

	// 管理员先确认端点可用（对照组）。
	if code := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/office/sessions", adminToken, nil).Code; code != http.StatusOK {
		t.Fatalf("管理员应可访问: %d", code)
	}

	plain := testutil.CreateUser(t, g, "office-plain@example.com", "officeplain", "password123", true)
	plainToken := testutil.AccessToken(t, cfg, &plain)
	support := testutil.CreateUser(t, g, "office-support@example.com", "officesupport", "password123", true)
	supportKey := authz.SupportRoleKey
	testutil.SetUserRole(t, g, &support, &supportKey)
	supportToken := testutil.AccessToken(t, cfg, &support)

	paths := []string{
		"/api/admin/office/sessions",
		"/api/admin/office/sessions/s-adm-sess-01",
		"/api/admin/office/stats",
	}
	for _, path := range paths {
		if code := testutil.DoAuthJSON(r, http.MethodGet, path, plainToken, nil).Code; code != http.StatusForbidden {
			t.Fatalf("无角色访问 %s 应 403: %d", path, code)
		}
		if code := testutil.DoAuthJSON(r, http.MethodGet, path, supportToken, nil).Code; code != http.StatusForbidden {
			t.Fatalf("support 角色访问 %s 应 403: %d", path, code)
		}
		if code := testutil.DoJSON(r, http.MethodGet, path, nil).Code; code != http.StatusUnauthorized {
			t.Fatalf("匿名访问 %s 应 401: %d", path, code)
		}
	}
}
