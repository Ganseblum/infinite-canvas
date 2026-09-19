package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const (
	testAdminOrigin = "https://sim-admin.youc.online"
	testSiteOrigin  = "https://sim-art.youc.online"
)

func newCORSTestRouter(allowed []string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(allowed))
	r.GET("/api/v1/thing", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.POST("/api/v1/thing", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func doCORSRequest(t *testing.T, r *gin.Engine, method, origin string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/thing", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertNoCORSHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for key := range w.Header() {
		if strings.HasPrefix(key, "Access-Control-") {
			t.Fatalf("不应出现 CORS 头 %s=%s", key, w.Header().Get(key))
		}
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	w := doCORSRequest(t, r, http.MethodGet, testAdminOrigin, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("白名单来源应正常处理返回 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != testAdminOrigin {
		t.Fatalf("Access-Control-Allow-Origin 应精确回显 %s, got %q", testAdminOrigin, got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials 应为 true, got %q", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary 应包含 Origin, got %q", got)
	}
}

func TestCORSNeverWildcard(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	for _, origin := range []string{testAdminOrigin, testSiteOrigin, "null"} {
		w := doCORSRequest(t, r, http.MethodGet, origin, nil)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Fatalf("Access-Control-Allow-Origin 不能是 *, origin=%s", origin)
		}
	}
}

func TestCORSDisallowedOriginPassesThrough(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	w := doCORSRequest(t, r, http.MethodPost, testSiteOrigin, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("非白名单来源应正常处理返回 200 而不是 403, got %d", w.Code)
	}
	assertNoCORSHeaders(t, w)
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("带 Origin 的响应应写 Vary: Origin, got %q", got)
	}
}

func TestCORSNullOriginNotAllowed(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	w := doCORSRequest(t, r, http.MethodGet, "null", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("Origin: null 应正常处理返回 200, got %d", w.Code)
	}
	assertNoCORSHeaders(t, w)
}

func TestCORSPreflight(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	w := doCORSRequest(t, r, http.MethodOptions, testAdminOrigin, map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization,content-type",
	})

	if w.Code != http.StatusNoContent {
		t.Fatalf("预检应短路返回 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != testAdminOrigin {
		t.Fatalf("预检应回显 Origin %s, got %q", testAdminOrigin, got)
	}
	methods := w.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "GET") || !strings.Contains(methods, "POST") {
		t.Fatalf("Access-Control-Allow-Methods 应含 GET 与 POST, got %q", methods)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("Access-Control-Allow-Headers 应显式含 Authorization, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("Access-Control-Max-Age 应为 600, got %q", got)
	}
}

func TestCORSPreflightOnUnregisteredPath(t *testing.T) {
	// 引擎级中间件也必须覆盖没有匹配路由的路径（gin 的 noRoute 链），否则预检会落成 404/405。
	r := newCORSTestRouter([]string{testAdminOrigin})
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/does-not-exist", nil)
	req.Header.Set("Origin", testAdminOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("未注册路径的预检也应短路返回 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != testAdminOrigin {
		t.Fatalf("预检应回显 Origin %s, got %q", testAdminOrigin, got)
	}
}

func TestCORSExposesRetryAfter(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	simple := doCORSRequest(t, r, http.MethodGet, testAdminOrigin, nil)
	if got := simple.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Retry-After") {
		t.Fatalf("简单请求应暴露 Retry-After, got %q", got)
	}
	preflight := doCORSRequest(t, r, http.MethodOptions, testAdminOrigin, map[string]string{
		"Access-Control-Request-Method": "POST",
	})
	if got := preflight.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Retry-After") {
		t.Fatalf("预检应暴露 Retry-After, got %q", got)
	}
}

func TestCORSRequestWithoutOrigin(t *testing.T) {
	r := newCORSTestRouter([]string{testAdminOrigin})
	w := doCORSRequest(t, r, http.MethodGet, "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("无 Origin 应正常处理返回 200, got %d", w.Code)
	}
	assertNoCORSHeaders(t, w)
}

func TestCORSDisabledWhenNotConfigured(t *testing.T) {
	for _, allowed := range [][]string{nil, {}} {
		r := newCORSTestRouter(allowed)
		w := doCORSRequest(t, r, http.MethodGet, testAdminOrigin, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("未配置 CORS 时应正常处理返回 200, got %d", w.Code)
		}
		assertNoCORSHeaders(t, w)

		preflight := doCORSRequest(t, r, http.MethodOptions, testAdminOrigin, map[string]string{
			"Access-Control-Request-Method": "POST",
		})
		assertNoCORSHeaders(t, preflight)
	}
}
