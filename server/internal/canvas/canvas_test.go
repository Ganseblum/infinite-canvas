package canvas

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/testutil"
)

func canvasPayload(title string) map[string]any {
	return map[string]any{
		"title": title,
		"data": map[string]any{
			"nodes": []map[string]any{
				{"id": "n1", "type": "image", "metadata": map[string]any{"storageKey": "image:Cover1"}},
				{"id": "n2", "type": "text", "metadata": map[string]any{"content": "hi"}},
			},
			"connections": []map[string]any{
				{"id": "c1", "fromNodeId": "n1", "toNodeId": "n2"},
			},
			"viewport": map[string]any{"x": 0, "y": 0, "k": 1},
		},
	}
}

func createCanvas(t *testing.T, r http.Handler, token string, body map[string]any) map[string]any {
	t.Helper()
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/canvases", token, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建画布失败: code=%d body=%s", w.Code, w.Body.String())
	}
	return testutil.DecodeBody(t, w)
}

func TestCanvasCRUDRevisionAndSoftDelete(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "canvas@example.com", "canvasuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	created := createCanvas(t, r, token, canvasPayload("分镜草图"))
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("创建响应缺少 id")
	}
	if created["revision"].(float64) != 1 {
		t.Fatalf("新建画布 revision 应为 1: %v", created["revision"])
	}
	if created["nodeCount"].(float64) != 2 || created["connectionCount"].(float64) != 1 {
		t.Fatalf("节点/连线计数应由服务端计算: %v", created)
	}
	if created["coverKey"] != "image:Cover1" {
		t.Fatalf("coverKey 应取第一个图片节点: %v", created["coverKey"])
	}

	// 列表只回摘要，不含 data
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表失败: code=%d body=%s", w.Code, w.Body.String())
	}
	list := testutil.DecodeBody(t, w)
	if list["total"].(float64) != 1 || list["page"].(float64) != 1 || list["size"].(float64) != 20 {
		t.Fatalf("分页字段不符: %v", list)
	}
	items := testutil.DecodeItems(t, w)
	if _, hasData := items[0]["data"]; hasData {
		t.Fatalf("列表摘要不应包含 data: %v", items[0])
	}
	for _, key := range []string{"id", "title", "nodeCount", "connectionCount", "coverKey", "updatedAt"} {
		if _, ok := items[0][key]; !ok {
			t.Fatalf("列表摘要缺少字段 %s: %v", key, items[0])
		}
	}

	// 详情含 data 与 revision
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases/"+id, token, nil)
	detail := testutil.DecodeBody(t, w)
	if detail["revision"].(float64) != 1 {
		t.Fatalf("详情 revision 应为 1: %v", detail["revision"])
	}
	if _, ok := detail["data"].(map[string]any); !ok {
		t.Fatalf("详情应包含 data 对象: %v", detail)
	}

	// PUT 乐观锁：正确 revision 成功并递增
	newData := map[string]any{"nodes": []map[string]any{{"id": "n1", "type": "text"}}, "connections": []any{}}
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/canvases/"+id, token, map[string]any{"data": newData, "revision": 1})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT 失败: code=%d body=%s", w.Code, w.Body.String())
	}
	if testutil.DecodeBody(t, w)["revision"].(float64) != 2 {
		t.Fatalf("PUT 成功应返回 revision=2: %s", w.Body.String())
	}

	// 旧 revision 再写 → 409 附当前 revision
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/canvases/"+id, token, map[string]any{"data": newData, "revision": 1})
	if w.Code != http.StatusConflict {
		t.Fatalf("revision 冲突应返回 409, got %d body=%s", w.Code, w.Body.String())
	}
	conflict := testutil.DecodeBody(t, w)
	errObj := conflict["error"].(map[string]any)
	if errObj["code"] != "REVISION_CONFLICT" {
		t.Fatalf("冲突错误码应为 REVISION_CONFLICT: %v", errObj)
	}
	if errObj["revision"].(float64) != 2 {
		t.Fatalf("冲突响应应附服务端当前 revision=2: %v", errObj)
	}

	// PATCH 只改标题且不递增 revision
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/canvases/"+id, token, map[string]any{"title": "新标题"})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH 失败: code=%d body=%s", w.Code, w.Body.String())
	}
	patchBody := testutil.DecodeBody(t, w)
	if patchBody["title"] != "新标题" || patchBody["id"] != id {
		t.Fatalf("PATCH 响应不符: %v", patchBody)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases/"+id, token, nil)
	detail = testutil.DecodeBody(t, w)
	if detail["title"] != "新标题" {
		t.Fatalf("PATCH 后标题未生效: %v", detail["title"])
	}
	if detail["revision"].(float64) != 2 {
		t.Fatalf("PATCH 不应递增 revision: %v", detail["revision"])
	}
	// 计数由服务端从新 data 计算（1 个节点、0 条连线）
	if detail["nodeCount"].(float64) != 1 || detail["connectionCount"].(float64) != 0 {
		t.Fatalf("PUT 后计数未更新: %v", detail)
	}

	// 删除：软删、幂等、删除后 404
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/canvases/"+id, token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除应返回 204, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases/"+id, token, nil); w.Code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/canvases/"+id, token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("重复删除应仍返回 204, got %d", w.Code)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases", token, nil)
	if testutil.DecodeBody(t, w)["total"].(float64) != 0 {
		t.Fatalf("删除后列表应不含该画布: %s", w.Body.String())
	}
}

func TestCanvasCrossUserIsolation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	owner := testutil.CreateUser(t, g, "owner@example.com", "owneruser", "password123", true)
	other := testutil.CreateUser(t, g, "other@example.com", "otheruser", "password123", true)
	ownerToken := testutil.AccessToken(t, cfg, &owner)
	otherToken := testutil.AccessToken(t, cfg, &other)

	created := createCanvas(t, r, ownerToken, canvasPayload("私密画布"))
	id := created["id"].(string)

	for _, tc := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, map[string]any{"data": map[string]any{}, "revision": 1}},
		{http.MethodPatch, map[string]any{"title": "偷改"}},
		{http.MethodDelete, nil},
	} {
		w := testutil.DoAuthJSON(r, tc.method, "/api/v1/canvases/"+id, otherToken, tc.body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("跨用户 %s 应返回 404, got %d body=%s", tc.method, w.Code, w.Body.String())
		}
		if code := testutil.ErrorCode(t, w); code != "NOT_FOUND" {
			t.Fatalf("跨用户 %s 错误码应为 NOT_FOUND, got %s", tc.method, code)
		}
	}
	// 原用户的记录未被影响
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases/"+id, ownerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("原用户仍应能访问: %d", w.Code)
	}
}

func TestCanvasListPaginationSearchAndSortValidation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "list@example.com", "listuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	for _, title := range []string{"Alpha 草图", "Beta 分镜", "Gamma 方案"} {
		createCanvas(t, r, token, canvasPayload(title))
		time.Sleep(2 * time.Millisecond)
	}

	// 分页
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases?page=1&size=2", token, nil)
	body := testutil.DecodeBody(t, w)
	if body["total"].(float64) != 3 || body["page"].(float64) != 1 || body["size"].(float64) != 2 {
		t.Fatalf("分页响应不符: %v", body)
	}
	if len(testutil.DecodeItems(t, w)) != 2 {
		t.Fatalf("第一页应返回 2 条")
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases?page=2&size=2", token, nil)
	if items := testutil.DecodeItems(t, w); len(items) != 1 {
		t.Fatalf("第二页应返回 1 条, got %d", len(items))
	}

	// 搜索大小写不敏感
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases?q=alpha", token, nil)
	if items := testutil.DecodeItems(t, w); len(items) != 1 || items[0]["title"] != "Alpha 草图" {
		t.Fatalf("搜索应大小写不敏感命中: %v", items)
	}
	// 排序白名单：title 升序
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases?sort=title", token, nil)
	items := testutil.DecodeItems(t, w)
	if items[0]["title"] != "Alpha 草图" || items[2]["title"] != "Gamma 方案" {
		t.Fatalf("按标题升序不符: %v", items)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/canvases?sort=-title", token, nil)
	items = testutil.DecodeItems(t, w)
	if items[0]["title"] != "Gamma 方案" {
		t.Fatalf("按标题降序不符: %v", items)
	}

	// 非法参数
	for _, path := range []string{
		"/api/v1/canvases?sort=id",
		"/api/v1/canvases?size=101",
		"/api/v1/canvases?size=0",
		"/api/v1/canvases?page=0",
		"/api/v1/canvases?page=abc",
	} {
		w = testutil.DoAuthJSON(r, http.MethodGet, path, token, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400, got %d body=%s", path, w.Code, w.Body.String())
		}
		if code := testutil.ErrorCode(t, w); code != "VALIDATION_FAILED" {
			t.Fatalf("%s 错误码应为 VALIDATION_FAILED, got %s", path, code)
		}
	}
}

func TestCanvasDataLimitAndServerComputedFields(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "limit@example.com", "limituser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	// 客户端上报的冗余字段被忽略
	body := canvasPayload("客户端伪造计数")
	body["nodeCount"] = 999
	body["connectionCount"] = 999
	body["coverKey"] = "image:Fake"
	created := createCanvas(t, r, token, body)
	if created["nodeCount"].(float64) != 2 || created["coverKey"] != "image:Cover1" {
		t.Fatalf("服务端应忽略客户端上报的冗余字段: %v", created)
	}

	// 超过 2 MB 的 data 被拒
	huge := map[string]any{"title": "超大画布", "data": map[string]any{"blob": strings.Repeat("a", maxCanvasDataBytes+1)}}
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/canvases", token, huge)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超过 2 MB 应返回 400, got %d body=%s", w.Code, w.Body.String())
	}
	if code := testutil.ErrorCode(t, w); code != "VALIDATION_FAILED" {
		t.Fatalf("超限错误码应为 VALIDATION_FAILED, got %s", code)
	}

	// data 不是对象同样非法
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/canvases", token, map[string]any{"title": "bad", "data": []int{1, 2}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("数组 data 应返回 400, got %d", w.Code)
	}

	// 空 data 缺省为空画布
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/canvases", token, map[string]any{"title": "空画布"})
	if w.Code != http.StatusCreated {
		t.Fatalf("缺省 data 应可创建: %d body=%s", w.Code, w.Body.String())
	}
	empty := testutil.DecodeBody(t, w)
	if empty["nodeCount"].(float64) != 0 || empty["connectionCount"].(float64) != 0 {
		t.Fatalf("空画布计数应为 0: %v", empty)
	}
}

func TestCanvasTitleLengthValidation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "title@example.com", "titleuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	long := strings.Repeat("题", 201)
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/canvases", token, map[string]any{"title": long})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超长标题应返回 400, got %d", w.Code)
	}

	created := createCanvas(t, r, token, canvasPayload("正常标题"))
	id := created["id"].(string)
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/canvases/"+id, token, map[string]any{"title": long})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH 超长标题应返回 400, got %d", w.Code)
	}
	// PUT 缺少 revision → 400
	w = testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/canvases/"+id, token, map[string]any{"data": map[string]any{}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 revision 的 PUT 应返回 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCanvasDeletedRecordNotVisibleInDetail(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "softdel@example.com", "softdel", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	created := createCanvas(t, r, token, canvasPayload("待删除"))
	id := created["id"].(string)
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/canvases/"+id, token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除失败: %d", w.Code)
	}
	// 数据库仍保留软删行，deleted_at 非空
	var canvas model.Canvas
	if err := g.Unscoped().Where("id = ?", id).First(&canvas).Error; err != nil {
		t.Fatalf("软删记录应保留在库中: %v", err)
	}
	if !canvas.DeletedAt.Valid {
		t.Fatal("deleted_at 应被写入")
	}
	if canvas.Revision != 1 || canvas.Title != "待删除" {
		t.Fatalf("软删不应改动其他字段: %+v", canvas)
	}
	// PUT / PATCH 对已删记录同样 404
	if w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/canvases/"+id, token, map[string]any{"title": "x"}); w.Code != http.StatusNotFound {
		t.Fatalf("已删记录 PATCH 应 404, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodPut, "/api/v1/canvases/"+id, token, map[string]any{"data": map[string]any{}, "revision": 1}); w.Code != http.StatusNotFound {
		t.Fatalf("已删记录 PUT 应 404, got %d", w.Code)
	}
}
