package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestClientIPOnlyTrustsConfiguredProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func(trusted []string) *gin.Engine {
		r := gin.New()
		if err := r.SetTrustedProxies(trusted); err != nil {
			t.Fatalf("设置可信代理失败: %v", err)
		}
		r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, ClientIP(c)) })
		return r
	}
	request := func(r *gin.Engine, remote, realIP, forwardedFor string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		if realIP != "" {
			req.Header.Set("X-Real-IP", realIP)
		}
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Body.String()
	}

	noTrust := newRouter(nil)
	if got := request(noTrust, "203.0.113.10:1234", "1.2.3.4", ""); got != "203.0.113.10" {
		t.Fatalf("未配置可信代理时不应采用 X-Real-IP, got %s", got)
	}
	if got := request(noTrust, "203.0.113.10:1234", "", "1.2.3.4"); got != "203.0.113.10" {
		t.Fatalf("未配置可信代理时不应采用 X-Forwarded-For, got %s", got)
	}

	trusted := newRouter([]string{"10.0.0.0/8"})
	if got := request(trusted, "10.1.2.3:5555", "1.2.3.4", ""); got != "1.2.3.4" {
		t.Fatalf("受信代理应解析 X-Real-IP, got %s", got)
	}
	if got := request(trusted, "10.1.2.3:5555", "", "1.2.3.4"); got != "1.2.3.4" {
		t.Fatalf("受信代理应解析 X-Forwarded-For, got %s", got)
	}
	// 客户端伪造的 XFF 在 nginx 追加真实 IP 后，应取最右侧的可信客户端地址
	if got := request(trusted, "10.1.2.3:5555", "", "6.6.6.6, 1.2.3.4"); got != "1.2.3.4" {
		t.Fatalf("应跳过伪造的 XFF 前缀, got %s", got)
	}
	if got := request(trusted, "203.0.113.10:1234", "1.2.3.4", "1.2.3.4"); got != "203.0.113.10" {
		t.Fatalf("非受信来源仍应使用 RemoteAddr, got %s", got)
	}
}
