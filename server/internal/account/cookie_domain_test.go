package account

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/testutil"
)

// cookiesByName 取出响应里某名字的全部 Set-Cookie（testutil.FindCookie 只返回首个同名，
// 成对断言需要自己遍历，域内完成，不改 testutil）。
func cookiesByName(w *httptest.ResponseRecorder, name string) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func TestCookieDomainSharedAcrossSubdomains(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	cfg.CookieDomain = "example.com"
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	// 注册即登录：refresh cookie 直接带 Domain
	_, _, regCookie := registerUser(t, r, "domain@example.com", "domainuser", "password123")
	if regCookie == nil || regCookie.Domain != cfg.CookieDomain {
		t.Fatalf("注册下发 refresh cookie 应带 Domain=%s, got %+v", cfg.CookieDomain, regCookie)
	}

	// 登录：ic_refresh 与 ic_media 都恰好一枚且带 Domain
	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "domain@example.com", "password": "password123"})
	if w.Code != http.StatusOK {
		t.Fatalf("登录失败: code=%d body=%s", w.Code, w.Body.String())
	}
	for _, name := range []string{RefreshCookieName, auth.MediaCookieName} {
		cookies := cookiesByName(w, name)
		if len(cookies) != 1 || cookies[0].Domain != cfg.CookieDomain {
			t.Fatalf("登录下发 %s 应恰好一枚且 Domain=%s, got %+v", name, cfg.CookieDomain, cookies)
		}
	}

	// 刷新轮换后的 Set-Cookie 同样带 Domain
	login := cookiesByName(w, RefreshCookieName)[0]
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, login)
	if w.Code != http.StatusOK {
		t.Fatalf("刷新失败: code=%d body=%s", w.Code, w.Body.String())
	}
	rotated := cookiesByName(w, RefreshCookieName)
	if len(rotated) != 1 || rotated[0].Domain != cfg.CookieDomain || rotated[0].Value == login.Value {
		t.Fatalf("刷新应轮换且新 cookie 带 Domain=%s, got %+v", cfg.CookieDomain, rotated)
	}

	// 登出：每个 cookie 名各两枚 MaxAge<0 的变体（带 Domain + host-only）
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/logout", nil, rotated[0])
	if w.Code != http.StatusNoContent {
		t.Fatalf("登出失败: code=%d body=%s", w.Code, w.Body.String())
	}
	for _, name := range []string{RefreshCookieName, auth.MediaCookieName} {
		cleared := cookiesByName(w, name)
		if len(cleared) != 2 {
			t.Fatalf("登出应对 %s 下发两枚清除 cookie, got %d: %+v", name, len(cleared), cleared)
		}
		domains := map[string]bool{}
		for _, c := range cleared {
			if c.MaxAge >= 0 {
				t.Fatalf("登出清除 cookie 的 MaxAge 应 <0, got %+v", c)
			}
			domains[c.Domain] = true
		}
		if !domains[cfg.CookieDomain] || !domains[""] {
			t.Fatalf("登出应同时覆盖 Domain 与 host-only 两个变体, got %+v", cleared)
		}
	}
}

// 未配置 CookieDomain（零值空串）时保持 host-only：登录/刷新不带 Domain，
// 登出每个名字只发一枚清除 cookie，与既有行为一致。
func TestCookieDomainEmptyKeepsHostOnly(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	_, _, loginCookie := registerUser(t, r, "hostonly@example.com", "hostonlyuser", "password123")
	if loginCookie == nil || loginCookie.Domain != "" {
		t.Fatalf("未配置 CookieDomain 时 refresh cookie 应为 host-only, got %+v", loginCookie)
	}

	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/logout", nil, loginCookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("登出失败: code=%d body=%s", w.Code, w.Body.String())
	}
	for _, name := range []string{RefreshCookieName, auth.MediaCookieName} {
		cleared := cookiesByName(w, name)
		if len(cleared) != 1 || cleared[0].Domain != "" || cleared[0].MaxAge >= 0 {
			t.Fatalf("未配置 CookieDomain 时登出应只发一枚 host-only 清除 cookie, got %+v", cleared)
		}
	}
}
