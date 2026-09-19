package canvas

import (
	"net/http"
	"testing"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/testutil"
)

func createAsset(t *testing.T, r http.Handler, token string, body map[string]any) map[string]any {
	t.Helper()
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/assets", token, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建素材失败: code=%d body=%s", w.Code, w.Body.String())
	}
	return testutil.DecodeBody(t, w)
}

func TestAssetCRUDAndSubsetPatch(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "asset@example.com", "assetuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	created := createAsset(t, r, token, map[string]any{
		"kind":  "text",
		"title": "会议纪要",
		"tags":  []string{"工作", "草稿"},
		"data":  map[string]any{"content": "这是一段关于预算的正文", "source": "手动添加", "note": ""},
	})
	id := created["id"].(string)
	if len(created["tags"].([]any)) != 2 {
		t.Fatalf("创建响应应回带标签: %v", created)
	}
	if created["storageKey"] != "" || created["bytes"].(float64) != 0 {
		t.Fatalf("文本素材默认 storageKey 为空、bytes 为 0: %v", created)
	}

	// 详情
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets/"+id, token, nil)
	if w.Code != http.StatusOK || testutil.DecodeBody(t, w)["title"] != "会议纪要" {
		t.Fatalf("素材详情不符: %d %s", w.Code, w.Body.String())
	}

	// PATCH 子集：只改标题，标签与 data 不变
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/assets/"+id, token, map[string]any{"title": "会议纪要 v2"})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH 标题失败: %d body=%s", w.Code, w.Body.String())
	}
	patched := testutil.DecodeBody(t, w)
	if patched["title"] != "会议纪要 v2" || len(patched["tags"].([]any)) != 2 {
		t.Fatalf("PATCH 不应影响其他字段: %v", patched)
	}

	// PATCH 标签整体替换
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/assets/"+id, token, map[string]any{"tags": []string{"归档"}})
	patched = testutil.DecodeBody(t, w)
	if len(patched["tags"].([]any)) != 1 || patched["tags"].([]any)[0] != "归档" {
		t.Fatalf("标签应整体替换: %v", patched["tags"])
	}

	// PATCH data
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/assets/"+id, token, map[string]any{
		"data": map[string]any{"content": "换一段正文"},
	})
	patched = testutil.DecodeBody(t, w)
	if patched["data"].(map[string]any)["content"] != "换一段正文" {
		t.Fatalf("PATCH data 未生效: %v", patched["data"])
	}

	// PATCH storageKey 同步镜像进 data.storageKey
	w = testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/assets/"+id, token, map[string]any{"storageKey": "image:NewKey1"})
	patched = testutil.DecodeBody(t, w)
	if patched["storageKey"] != "image:NewKey1" {
		t.Fatalf("PATCH storageKey 未生效: %v", patched)
	}
	if patched["data"].(map[string]any)["storageKey"] != "image:NewKey1" {
		t.Fatalf("storageKey 应镜像进 data: %v", patched["data"])
	}

	// 空 PATCH → 400
	if w := testutil.DoAuthJSON(r, http.MethodPatch, "/api/v1/assets/"+id, token, map[string]any{}); w.Code != http.StatusBadRequest {
		t.Fatalf("空 PATCH 应 400, got %d", w.Code)
	}

	// 删除幂等
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/assets/"+id, token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除素材应 204, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets/"+id, token, nil); w.Code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, got %d", w.Code)
	}
	if w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/v1/assets/"+id, token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("重复删除应仍 204, got %d", w.Code)
	}
}

func TestAssetListFiltersTagsFacetsAndSearchScope(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "assetlist@example.com", "assetlist", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	createAsset(t, r, token, map[string]any{
		"kind": "text", "title": "预算说明", "tags": []string{"甲", "乙"},
		"data": map[string]any{"content": "只出现在正文里的关键字：财务自由"},
	})
	createAsset(t, r, token, map[string]any{
		"kind": "image", "title": "参考图", "tags": []string{"甲"},
		"storageKey": "image:Png123", "bytes": 100,
		"data": map[string]any{"storageKey": "image:Png123", "mimeType": "image/png", "width": 10, "height": 10},
	})
	createAsset(t, r, token, map[string]any{
		"kind": "video", "title": "片头", "tags": []string{"乙"},
		"storageKey": "video:Vid123", "bytes": 200,
		"data": map[string]any{"storageKey": "video:Vid123", "mimeType": "video/mp4"},
	})

	// 第一页返回标签全集 facets
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?page=1&size=2", token, nil)
	body := testutil.DecodeBody(t, w)
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("第一页应返回标签全集: %v", body["tags"])
	}
	if body["total"].(float64) != 3 {
		t.Fatalf("total 不符: %v", body["total"])
	}
	// 第二页不再返回 facets
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?page=2&size=2", token, nil)
	if _, hasTags := testutil.DecodeBody(t, w)["tags"]; hasTags {
		t.Fatal("第二页不应重复返回 tags facets")
	}

	// kind 筛选
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?kind=image", token, nil)
	items := testutil.DecodeItems(t, w)
	if len(items) != 1 || items[0]["title"] != "参考图" {
		t.Fatalf("kind 筛选不符: %v", items)
	}
	// 枚举外 kind → 400
	if w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?kind=audio", token, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("非法 kind 应 400, got %d", w.Code)
	}

	// 标签 AND 语义
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?tag=甲&tag=乙", token, nil)
	items = testutil.DecodeItems(t, w)
	if len(items) != 1 || items[0]["title"] != "预算说明" {
		t.Fatalf("多标签应同时包含: %v", items)
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?tag=甲", token, nil)
	if items = testutil.DecodeItems(t, w); len(items) != 2 {
		t.Fatalf("单标签命中数不符: %v", items)
	}

	// q 只搜 title 与 data.content：正文命中
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?q=财务自由", token, nil)
	items = testutil.DecodeItems(t, w)
	if len(items) != 1 || items[0]["title"] != "预算说明" {
		t.Fatalf("正文搜索未命中: %v", items)
	}
	// 搜 title
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?q=参考", token, nil)
	if items = testutil.DecodeItems(t, w); len(items) != 1 || items[0]["title"] != "参考图" {
		t.Fatalf("标题搜索未命中: %v", items)
	}
	// 搜 storageKey 片段或 mimeType 不应命中任何媒体素材
	for _, q := range []string{"Png123", "png", "video/mp4"} {
		w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?q="+q, token, nil)
		if items = testutil.DecodeItems(t, w); len(items) != 0 {
			t.Fatalf("关键字 %q 不应命中媒体元数据: %v", q, items)
		}
	}

	// 排序白名单与非法参数
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?sort=title", token, nil)
	if items = testutil.DecodeItems(t, w); items[0]["title"] != "参考图" {
		t.Fatalf("按标题排序不符: %v", items)
	}
	for _, path := range []string{"/api/v1/assets?sort=bytes", "/api/v1/assets?size=101", "/api/v1/assets?page=0"} {
		if w := testutil.DoAuthJSON(r, http.MethodGet, path, token, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400, got %d", path, w.Code)
		}
	}
}

func TestAssetCrossUserIsolation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	owner := testutil.CreateUser(t, g, "assetowner@example.com", "assetowner", "password123", true)
	other := testutil.CreateUser(t, g, "assetother@example.com", "assetother", "password123", true)
	ownerToken := testutil.AccessToken(t, cfg, &owner)
	otherToken := testutil.AccessToken(t, cfg, &other)

	created := createAsset(t, r, ownerToken, map[string]any{
		"kind": "text", "title": "私有", "data": map[string]any{"content": "x"},
	})
	id := created["id"].(string)

	for _, tc := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPatch, map[string]any{"title": "偷改"}},
		{http.MethodDelete, nil},
	} {
		w := testutil.DoAuthJSON(r, tc.method, "/api/v1/assets/"+id, otherToken, tc.body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("跨用户 %s 应 404, got %d body=%s", tc.method, w.Code, w.Body.String())
		}
	}

	// 跨用户标签不混入 facets
	otherAsset := createAsset(t, r, otherToken, map[string]any{
		"kind": "text", "title": "别人的", "tags": []string{"私人标签"},
		"data": map[string]any{"content": "y"},
	})
	if otherAsset["id"] == "" {
		t.Fatal("创建失败")
	}
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/v1/assets?page=1", ownerToken, nil)
	if tags, ok := testutil.DecodeBody(t, w)["tags"].([]any); !ok || len(tags) != 0 {
		t.Fatalf("facets 不应包含其他用户的标签: %v", w.Body.String())
	}
	if items := testutil.DecodeItems(t, w); len(items) != 1 {
		t.Fatalf("列表不应包含其他用户的素材: %v", items)
	}
}

func TestAssetValidation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("local"))
	user := testutil.CreateUser(t, g, "assetvalid@example.com", "assetvalid", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	cases := []map[string]any{
		{"kind": "audio", "title": "x", "data": map[string]any{}},
		{"kind": "text", "title": "x", "storageKey": "image:bad.dot", "data": map[string]any{}},
		{"kind": "text", "title": "x", "bytes": -1, "data": map[string]any{}},
		{"kind": "text", "title": "x", "data": []int{1}},
		{"kind": "text", "title": "x", "tags": []string{"  "}, "data": map[string]any{}},
	}
	for i, body := range cases {
		w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/assets", token, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("非法素材 #%d 应 400, got %d body=%s", i, w.Code, w.Body.String())
		}
		if code := testutil.ErrorCode(t, w); code != "VALIDATION_FAILED" {
			t.Fatalf("非法素材 #%d 错误码应为 VALIDATION_FAILED, got %s", i, code)
		}
	}

	// 标签去重与去空格后落库
	created := createAsset(t, r, token, map[string]any{
		"kind": "text", "title": "标签", "tags": []string{" a ", "a", "b"},
		"data": map[string]any{"content": "x"},
	})
	if len(created["tags"].([]any)) != 2 {
		t.Fatalf("标签应去重去空格: %v", created["tags"])
	}
	var count int64
	if err := g.Model(&model.AssetTag{}).Count(&count).Error; err != nil {
		t.Fatalf("统计标签失败: %v", err)
	}
	if count != 2 {
		t.Fatalf("asset_tags 应只有 2 行, got %d", count)
	}
}
