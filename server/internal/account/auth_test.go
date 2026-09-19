package account

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/testutil"
)

func TestRegisterVerifyLoginRefreshLogout(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	logs := testutil.CaptureLogs(t)
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	_, sess, _ := registerUser(t, r, "alice@example.com", "alice", "password123")

	// 注册只允许产生一条验证令牌
	var tokenCount int64
	if err := g.Model(&model.EmailToken{}).Where("user_id = ?", sess.User.ID).Count(&tokenCount).Error; err != nil {
		t.Fatalf("统计 EmailToken 失败: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("每次注册应只产生一条 EmailToken, got %d", tokenCount)
	}

	// 邮箱验证
	token := testutil.LastEmailToken(t, logs)
	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/verify-email", map[string]string{"token": token})
	if w.Code != http.StatusOK {
		t.Fatalf("验证邮箱失败: code=%d body=%s", w.Code, w.Body.String())
	}
	var verified struct {
		User struct {
			EmailVerified bool `json:"emailVerified"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &verified); err != nil || !verified.User.EmailVerified {
		t.Fatalf("验证后 emailVerified 应为 true: err=%v body=%s", err, w.Body.String())
	}

	// 令牌一次性
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/verify-email", map[string]string{"token": token})
	if w.Code != http.StatusGone {
		t.Fatalf("重复使用验证令牌应返回 410, got %d body=%s", w.Code, w.Body.String())
	}

	// 登录
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "alice@example.com", "password": "password123"})
	if w.Code != http.StatusOK {
		t.Fatalf("登录失败: code=%d body=%s", w.Code, w.Body.String())
	}
	loginCookie := testutil.FindCookie(w, RefreshCookieName)
	if loginCookie == nil {
		t.Fatal("登录未下发 refresh cookie")
	}

	// 刷新并轮换
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, loginCookie)
	if w.Code != http.StatusOK {
		t.Fatalf("刷新失败: code=%d body=%s", w.Code, w.Body.String())
	}
	rotated := testutil.FindCookie(w, RefreshCookieName)
	if rotated == nil || rotated.Value == loginCookie.Value {
		t.Fatal("刷新应轮换 refresh token")
	}

	// 登出
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/logout", nil, rotated)
	if w.Code != http.StatusNoContent {
		t.Fatalf("登出失败: code=%d body=%s", w.Code, w.Body.String())
	}
	if cleared := testutil.FindCookie(w, RefreshCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("登出应清除 refresh cookie")
	}

	// 已登出的令牌不可再用
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, rotated)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("登出后刷新应返回 401, got %d", w.Code)
	}
}

func TestRefreshReuseRevokesAllTokens(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	_, sess, c1 := registerUser(t, r, "reuse@example.com", "reuseuser", "password123")

	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, c1)
	if w.Code != http.StatusOK {
		t.Fatalf("首次刷新失败: code=%d body=%s", w.Code, w.Body.String())
	}
	c2 := testutil.FindCookie(w, RefreshCookieName)
	if c2 == nil {
		t.Fatal("刷新未下发新 cookie")
	}

	// 复用已轮换的旧令牌：真正复用应撤销全部令牌并清除 cookie
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, c1)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("复用已撤销令牌应返回 401, got %d", w.Code)
	}
	if code := testutil.ErrorCode(t, w); code != "UNAUTHORIZED" {
		t.Fatalf("复用检测应返回 UNAUTHORIZED, got %s", code)
	}
	if cleared := testutil.FindCookie(w, RefreshCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("复用检测应下发清 cookie")
	}

	var active int64
	if err := g.Model(&model.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", sess.User.ID).
		Count(&active).Error; err != nil {
		t.Fatalf("统计有效令牌失败: %v", err)
	}
	if active != 0 {
		t.Fatalf("复用检测应撤销该用户全部令牌, 仍有 %d 条有效", active)
	}

	// 轮换出来的新令牌同样已被撤销
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, c2)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("全部撤销后刷新应返回 401, got %d", w.Code)
	}
}

func TestConcurrentRefreshOnlyOneSucceeds(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	_, _, cookie := registerUser(t, r, "race@example.com", "raceuser", "password123")

	// 保证所有请求都读到轮换前的令牌状态，稳定命中并发竞争失败分支
	const n = 5
	testutil.RefreshReadBarrier(g, n)
	responses := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, cookie)
		}(i)
	}
	wg.Wait()

	codes := make([]int, n)
	winner := -1
	for i, w := range responses {
		codes[i] = w.Code
		switch w.Code {
		case http.StatusOK:
			if winner >= 0 {
				t.Fatalf("并发刷新应只成功一次, codes=%v", codes)
			}
			winner = i
		case http.StatusUnauthorized:
			if code := testutil.ErrorCode(t, w); code != "UNAUTHORIZED" {
				t.Fatalf("竞争失败应返回 UNAUTHORIZED, got %s", code)
			}
		default:
			t.Fatalf("并发刷新出现意外状态码: %d body=%s", w.Code, w.Body.String())
		}
	}
	if winner < 0 {
		t.Fatalf("并发刷新没有任何请求成功, codes=%v", codes)
	}

	// 竞争失败方不得撤销令牌，也不得下发清 cookie 的 Set-Cookie
	for i, w := range responses {
		if i == winner {
			continue
		}
		if c := testutil.FindCookie(w, RefreshCookieName); c != nil {
			t.Fatalf("竞争失败响应不应携带 refresh cookie, got %+v", c)
		}
	}

	// 胜者拿到的新 refresh cookie 之后仍能成功刷新（旧实现会误伤胜者会话）
	rotated := testutil.FindCookie(responses[winner], RefreshCookieName)
	if rotated == nil || rotated.Value == cookie.Value {
		t.Fatal("胜者应拿到轮换后的新 refresh cookie")
	}
	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/refresh", nil, rotated)
	if w.Code != http.StatusOK {
		t.Fatalf("胜者的新 refresh cookie 应可继续刷新, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	testutil.CreateUser(t, g, "lock@example.com", "lockuser", "password123", false)
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	for i := 0; i < 5; i++ {
		w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "lock@example.com", "password": "wrong-password"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败登录应返回 401, got %d body=%s", i+1, w.Code, w.Body.String())
		}
	}

	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "lock@example.com", "password": "password123"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("连续 5 次失败后应锁定, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("锁定响应应带 Retry-After")
	}
}

func TestLoginSuccessResetsFailureCount(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	testutil.CreateUser(t, g, "reset@example.com", "resetuser", "password123", false)
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	wrong := func() {
		t.Helper()
		w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "reset@example.com", "password": "wrong-password"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("失败登录应返回 401, got %d", w.Code)
		}
	}
	right := func() {
		t.Helper()
		w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/login", map[string]string{"account": "reset@example.com", "password": "password123"})
		if w.Code != http.StatusOK {
			t.Fatalf("成功登录应返回 200, got %d body=%s", w.Code, w.Body.String())
		}
	}

	// 4 次失败后成功登录，计数应清零；再来 4 次失败后仍可登录
	for i := 0; i < 4; i++ {
		wrong()
	}
	right()
	for i := 0; i < 4; i++ {
		wrong()
	}
	right()
}

// TestConcurrentEmailVerifyOnlyOneSucceeds 并发验证邮箱：同一令牌只能被消费一次，
// 竞争失败方返回 410 TOKEN_INVALID，且不影响胜者完成验证。
func TestConcurrentEmailVerifyOnlyOneSucceeds(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	logs := testutil.CaptureLogs(t)
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	_, _, _ = registerUser(t, r, "racetoken@example.com", "racetoken", "password123")
	token := testutil.LastEmailToken(t, logs)

	// 保证所有请求都读完令牌后再竞争写入
	const n = 3
	testutil.TableReadBarrier(g, "email_tokens", n)
	responses := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/verify-email", map[string]string{"token": token})
		}(i)
	}
	wg.Wait()

	winner := -1
	for _, w := range responses {
		switch w.Code {
		case http.StatusOK:
			if winner >= 0 {
				t.Fatal("同一验证令牌并发消费应只成功一次")
			}
			winner = 1
		case http.StatusGone:
			if code := testutil.ErrorCode(t, w); code != "TOKEN_INVALID" {
				t.Fatalf("竞争失败应返回 TOKEN_INVALID, got %s", code)
			}
		default:
			t.Fatalf("并发验证出现意外状态码: %d body=%s", w.Code, w.Body.String())
		}
	}
	if winner < 0 {
		t.Fatal("并发验证没有任何请求成功")
	}
	var verified model.PlatformUser
	if err := g.Where("username = ?", "racetoken").First(&verified).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if verified.EmailVerifiedAt == nil {
		t.Fatal("胜者应完成邮箱验证")
	}
}

// TestEmailTokenTTLByPurpose 重置密码令牌 1 小时、验证邮件 24 小时，邮件文案与之一致。
func TestEmailTokenTTLByPurpose(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	logs := testutil.CaptureLogs(t)
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	user := testutil.CreateUser(t, g, "ttl@example.com", "ttluser", "password123", false)

	// 重置密码令牌 1 小时
	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "ttl@example.com"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("找回密码应返回 204, got %d", w.Code)
	}
	var reset model.EmailToken
	if err := g.Where("purpose = ?", "reset_password").First(&reset).Error; err != nil {
		t.Fatalf("读取重置令牌失败: %v", err)
	}
	if d := time.Until(reset.ExpiresAt); d < 55*time.Minute || d > 65*time.Minute {
		t.Fatalf("重置令牌应约 1 小时有效, got %s", d)
	}
	if !strings.Contains(logs.String(), "1 小时内有效") {
		t.Fatalf("重置邮件文案应写 1 小时: %s", logs.String())
	}

	// 验证邮件令牌 24 小时
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/auth/verify-email/send", testutil.AccessToken(t, cfg, &user), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("重发验证邮件应返回 204, got %d body=%s", w.Code, w.Body.String())
	}
	var verify model.EmailToken
	if err := g.Where("purpose = ?", "verify_email").First(&verify).Error; err != nil {
		t.Fatalf("读取验证令牌失败: %v", err)
	}
	if d := time.Until(verify.ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("验证令牌应约 24 小时有效, got %s", d)
	}
	if !strings.Contains(logs.String(), "24 小时内有效") {
		t.Fatalf("验证邮件文案应写 24 小时: %s", logs.String())
	}
}

func TestRegisterRateLimitByIP(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	for i := 0; i < 10; i++ {
		w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/register", map[string]string{
			"email":    fmt.Sprintf("reg%d@example.com", i),
			"username": fmt.Sprintf("reguser%d", i),
			"password": "password123",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("第 %d 次注册应成功, got %d body=%s", i+1, w.Code, w.Body.String())
		}
	}

	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/register", map[string]string{
		"email": "reg-limit@example.com", "username": "reglimit", "password": "password123",
	})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("同 IP 第 11 次注册应被限流, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("限流响应应带 Retry-After")
	}
}

func TestMailRateLimitByEmail(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	// 找回密码：同邮箱 3 次/小时
	for i := 0; i < 3; i++ {
		w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "forgot@example.com"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("第 %d 次找回密码应返回 204, got %d", i+1, w.Code)
		}
	}
	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "forgot@example.com"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("同一邮箱第 4 次找回密码应被限流, got %d", w.Code)
	}
	// 不存在的邮箱也一视同仁，且换邮箱不受影响
	w = testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "other@example.com"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("换邮箱应可继续发送, got %d", w.Code)
	}

	// 重发验证邮件：同邮箱 3 次/小时
	_, sess, _ := registerUser(t, r, "verify@example.com", "verifyuser", "password123")
	for i := 0; i < 3; i++ {
		w := testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/auth/verify-email/send", sess.AccessToken, nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("第 %d 次重发验证邮件应返回 204, got %d body=%s", i+1, w.Code, w.Body.String())
		}
	}
	w = testutil.DoAuthJSON(r, http.MethodPost, "/api/v1/auth/verify-email/send", sess.AccessToken, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("同一邮箱第 4 次重发验证邮件应被限流, got %d", w.Code)
	}
}

func TestMailRateLimitByIP(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	h := NewAuthHandler(g, cfg, testutil.TestMailer())
	r := newAuthRouter(t, cfg, h)

	for i := 0; i < 10; i++ {
		w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{
			"email": fmt.Sprintf("mail%d@example.com", i),
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("第 %d 次发信应返回 204, got %d", i+1, w.Code)
		}
	}
	w := testutil.DoJSON(r, http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "mail-limit@example.com"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("同 IP 第 11 次发信应被限流, got %d", w.Code)
	}
}
