package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
)

// ===== 夹具 =====

// testJWKSPEM 生成测试用 RS256 私钥并导出 PKCS#8 PEM，模拟生产通过
// OIDC_JWKS_PRIVATE_KEY 注入密钥的路径。
func testJWKSPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试私钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("序列化 PKCS#8 失败: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// newOIDCHandlerForTest 以环境变量注入的 PEM 构造 OIDCHandler（覆盖 fail-fast 之外的解析路径）。
func newOIDCHandlerForTest(t *testing.T, g *gorm.DB, cfg *config.Config) *OIDCHandler {
	t.Helper()
	t.Setenv("OIDC_JWKS_PRIVATE_KEY", testJWKSPEM(t))
	h, err := NewOIDCHandler(g, cfg, identity.NewService(g))
	if err != nil {
		t.Fatalf("构造 OIDCHandler 失败: %v", err)
	}
	return h
}

// newOIDCRouter 在 auth 路由之外挂上 OIDC 四端点（与 main.go 的挂载口径一致：
// authorize 走 Auth + RequireActiveUser，token/jwks 公开，userinfo 验 OIDC token）。
func newOIDCRouter(t *testing.T, g *gorm.DB, cfg *config.Config, oidcH *OIDCHandler, authH *AuthHandler) *gin.Engine {
	t.Helper()
	r := newAuthRouter(t, cfg, authH)
	secret := []byte(cfg.JWTSecret)
	active := middleware.RequireActiveUser(identity.NewService(g))
	oidc := r.Group("/api/oidc", middleware.Auth(secret), active)
	oidc.GET("/authorize", oidcH.Authorize)
	r.POST("/api/oidc/token", oidcH.Token)
	r.GET("/api/oidc/userinfo", oidcH.Userinfo)
	r.GET("/api/oidc/jwks.json", oidcH.JWKS)
	return r
}

func createOIDCClient(t *testing.T, g *gorm.DB, redirectURIs []string) (model.OAuthClient, string) {
	t.Helper()
	secret := "client-secret-" + uuid.NewString()
	clientID, err := newOAuthClientID()
	if err != nil {
		t.Fatalf("生成 client_id 失败: %v", err)
	}
	raw, err := json.Marshal(redirectURIs)
	if err != nil {
		t.Fatalf("序列化回调地址失败: %v", err)
	}
	client := model.OAuthClient{
		ID:               uuid.New(),
		Product:          model.ProductCanvas,
		Name:             "测试接入客户端",
		ClientID:         clientID,
		ClientSecretHash: hashOAuthClientSecret(secret),
		RedirectURIs:     datatypes.JSON(raw),
		Enabled:          true,
	}
	if err := g.Create(&client).Error; err != nil {
		t.Fatalf("创建 OAuth 客户端失败: %v", err)
	}
	return client, secret
}

// s256Challenge 按 RFC 7636 S256 计算 code_challenge。
func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func doForm(r http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func authorizeGet(r http.Handler, accessToken string, params url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/oidc/authorize?"+params.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func cloneValues(v url.Values) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	return c
}

// authorizeForCode 走完一次成功的 authorize，返回 302 回跳里的 code。
func authorizeForCode(t *testing.T, r http.Handler, accessToken string, client model.OAuthClient, verifier string) string {
	t.Helper()
	params := url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {"https://app.example.com/callback"},
		"response_type":         {"code"},
		"scope":                 {"openid profile"},
		"state":                 {"st-123"},
		"code_challenge":        {s256Challenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	w := authorizeGet(r, accessToken, params)
	if w.Code != http.StatusFound {
		t.Fatalf("authorize 应 302, got %d body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("解析回跳地址失败: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("302 回跳应带 code")
	}
	return code
}

type oidcTokenResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	IDToken     string `json:"id_token"`
}

func decodeTokenResp(t *testing.T, w *httptest.ResponseRecorder) oidcTokenResp {
	t.Helper()
	var resp oidcTokenResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析 token 响应失败: %v body=%s", err, w.Body.String())
	}
	return resp
}

func tokenForm(client model.OAuthClient, secret, code, redirectURI, verifier string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ClientID},
		"client_secret": {secret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
}

const (
	oidcRedirectURI = "https://app.example.com/callback"
	oidcVerifier    = "test-verifier-test-verifier-test-verifier-123456"
)

// ===== 全链路 =====

// TestOIDCFullChain 注册用户 → authorize（PKCE + state）→ 302 取 code → token 换
// access_token → userinfo → jwks 用公开 n/e 重组公钥验签。
func TestOIDCFullChain(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	cfg.AppBaseURL = "https://canvas.example.com"
	oidcH := newOIDCHandlerForTest(t, g, cfg)
	authH := NewAuthHandler(g, cfg, testMailer())
	r := newOIDCRouter(t, g, cfg, oidcH, authH)

	_, sess, cookie := registerUser(t, r, "oidc@example.com", "oidcuser", "password123")

	// cookie Path 断言：M3 起从 /api/auth 扩为 /api（PLAN D8 直接切换）
	if cookie.Path != "/api" {
		t.Fatalf("refresh cookie Path 应为 /api, got %q", cookie.Path)
	}

	client, secret := createOIDCClient(t, g, []string{oidcRedirectURI})
	params := url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {oidcRedirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid profile"},
		"state":                 {"st-123"},
		"code_challenge":        {s256Challenge(oidcVerifier)},
		"code_challenge_method": {"S256"},
	}
	w := authorizeGet(r, sess.AccessToken, params)
	if w.Code != http.StatusFound {
		t.Fatalf("authorize 应 302, got %d body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("解析回跳地址失败: %v", err)
	}
	if scheme := loc.Scheme + "://" + loc.Host + loc.Path; scheme != oidcRedirectURI {
		t.Fatalf("应回跳注册地址, got %s", loc.String())
	}
	if got := loc.Query().Get("state"); got != "st-123" {
		t.Fatalf("state 应透传, got %q", got)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("302 回跳应带 code")
	}

	// token（form 提交）
	w = doForm(r, "/api/oidc/token", tokenForm(client, secret, code, oidcRedirectURI, oidcVerifier))
	if w.Code != http.StatusOK {
		t.Fatalf("token 换取失败: code=%d body=%s", w.Code, w.Body.String())
	}
	tok := decodeTokenResp(t, w)
	if tok.TokenType != "Bearer" || tok.ExpiresIn != 900 {
		t.Fatalf("token_type 应为 Bearer、expires_in 应为 900, got %s/%d", tok.TokenType, tok.ExpiresIn)
	}
	if tok.Scope != "openid profile" {
		t.Fatalf("scope 应原样返回, got %q", tok.Scope)
	}
	if tok.IDToken == "" {
		t.Fatal("scope 含 openid 时应签发 id_token")
	}

	// userinfo
	w = doAuthJSON(r, http.MethodGet, "/api/oidc/userinfo", tok.AccessToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("userinfo 失败: code=%d body=%s", w.Code, w.Body.String())
	}
	ui := decodeBody(t, w)
	if ui["sub"] != sess.User.ID {
		t.Fatalf("sub 应为用户 id, got %v want %s", ui["sub"], sess.User.ID)
	}
	if ui["email"] != "oidc@example.com" || ui["username"] != "oidcuser" || ui["displayName"] != "oidcuser" {
		t.Fatalf("userinfo 档案不正确: %v", ui)
	}
	if ui["emailVerified"] != false {
		t.Fatalf("未验证邮箱 emailVerified 应为 false, got %v", ui["emailVerified"])
	}

	// jwks：kid 与 token header 一致，并用公开 n/e 重组公钥验签 access_token
	w = doJSON(r, http.MethodGet, "/api/oidc/jwks.json", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("jwks 失败: code=%d body=%s", w.Code, w.Body.String())
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &jwks); err != nil || len(jwks.Keys) != 1 {
		t.Fatalf("jwks 应含一把密钥: err=%v body=%s", err, w.Body.String())
	}
	key := jwks.Keys[0]
	if key.Kty != "RSA" || key.Alg != "RS256" || key.Use != "sig" || key.Kid == "" || key.N == "" || key.E == "" {
		t.Fatalf("jwks 字段不完整: %+v", key)
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(strings.Split(tok.AccessToken, ".")[0])
	if err != nil {
		t.Fatalf("解析 token header 失败: %v", err)
	}
	var hdr struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &hdr); err != nil || hdr.Kid != key.Kid {
		t.Fatalf("token header kid 应与 jwks 一致: hdr=%q jwks=%q err=%v", hdr.Kid, key.Kid, err)
	}
	nBytes, _ := base64.RawURLEncoding.DecodeString(key.N)
	eBytes, _ := base64.RawURLEncoding.DecodeString(key.E)
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(new(big.Int).SetBytes(eBytes).Int64())}
	parsed, err := jwt.Parse(tok.AccessToken, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, errors.New("签名算法不匹配")
		}
		return pub, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("jwks 公钥应能验签 access_token: err=%v valid=%v", err, parsed != nil && parsed.Valid)
	}
}

// ===== authorize 安全用例 =====

func TestOIDCAuthorizeRejects(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	oidcH := newOIDCHandlerForTest(t, g, cfg)
	authH := NewAuthHandler(g, cfg, testMailer())
	r := newOIDCRouter(t, g, cfg, oidcH, authH)

	_, sess, _ := registerUser(t, r, "reject@example.com", "rejectuser", "password123")
	client, _ := createOIDCClient(t, g, []string{oidcRedirectURI})

	base := url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {oidcRedirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid"},
		"code_challenge":        {s256Challenge(oidcVerifier)},
		"code_challenge_method": {"S256"},
	}
	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"未注册 redirect_uri 拒绝且不重定向", func(v url.Values) { v.Set("redirect_uri", "https://evil.example.com/cb") }},
		{"未知 client_id 拒绝", func(v url.Values) { v.Set("client_id", "ic_unknown") }},
		{"缺 code_challenge 拒绝", func(v url.Values) { v.Del("code_challenge") }},
		{"plain method 拒绝", func(v url.Values) { v.Set("code_challenge_method", "plain"); v.Set("code_challenge", "plain-verifier") }},
		{"缺 method 拒绝", func(v url.Values) { v.Del("code_challenge_method") }},
		{"response_type 非 code 拒绝", func(v url.Values) { v.Set("response_type", "token") }},
		{"scope 缺 openid 拒绝", func(v url.Values) { v.Set("scope", "profile email") }},
	}
	for _, tc := range cases {
		v := cloneValues(base)
		tc.mutate(v)
		w := authorizeGet(r, sess.AccessToken, v)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: 应 400, got %d body=%s", tc.name, w.Code, w.Body.String())
		}
		if w.Header().Get("Location") != "" {
			t.Fatalf("%s: 不应重定向", tc.name)
		}
		if code := errorCode(t, w); code != "INVALID_REQUEST" {
			t.Fatalf("%s: 应返回 INVALID_REQUEST, got %s", tc.name, code)
		}
	}
}

// TestOIDCAuthorizeRequiresLogin 未登录（无 Bearer）访问 authorize 应 401。
func TestOIDCAuthorizeRequiresLogin(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	oidcH := newOIDCHandlerForTest(t, g, cfg)
	authH := NewAuthHandler(g, cfg, testMailer())
	r := newOIDCRouter(t, g, cfg, oidcH, authH)
	client, _ := createOIDCClient(t, g, []string{oidcRedirectURI})

	params := url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {oidcRedirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid"},
		"code_challenge":        {s256Challenge(oidcVerifier)},
		"code_challenge_method": {"S256"},
	}
	w := authorizeGet(r, "", params)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录 authorize 应 401, got %d", w.Code)
	}
}

// ===== token 安全用例 =====

func TestOIDCTokenSecurity(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	oidcH := newOIDCHandlerForTest(t, g, cfg)
	authH := NewAuthHandler(g, cfg, testMailer())
	r := newOIDCRouter(t, g, cfg, oidcH, authH)

	_, sess, _ := registerUser(t, r, "toksec@example.com", "toksecuser", "password123")
	client, secret := createOIDCClient(t, g, []string{oidcRedirectURI})

	// code 一次性消费：第一次成功，第二次拒绝
	code := authorizeForCode(t, r, sess.AccessToken, client, oidcVerifier)
	w := doForm(r, "/api/oidc/token", tokenForm(client, secret, code, oidcRedirectURI, oidcVerifier))
	if w.Code != http.StatusOK {
		t.Fatalf("首次换 token 应成功: code=%d body=%s", w.Code, w.Body.String())
	}
	w = doForm(r, "/api/oidc/token", tokenForm(client, secret, code, oidcRedirectURI, oidcVerifier))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code 二次使用应拒绝, got %d body=%s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "INVALID_GRANT" {
		t.Fatalf("code 二次使用应返回 INVALID_GRANT, got %s", got)
	}

	// 错误 client_secret 拒绝
	code = authorizeForCode(t, r, sess.AccessToken, client, oidcVerifier)
	form := tokenForm(client, "wrong-secret", code, oidcRedirectURI, oidcVerifier)
	w = doForm(r, "/api/oidc/token", form)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("错误 client_secret 应 401, got %d body=%s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "INVALID_CLIENT" {
		t.Fatalf("错误 client_secret 应返回 INVALID_CLIENT, got %s", got)
	}
	// 认证失败的 code 未被消费，凭正确凭据仍可换取
	w = doForm(r, "/api/oidc/token", tokenForm(client, secret, code, oidcRedirectURI, oidcVerifier))
	if w.Code != http.StatusOK {
		t.Fatalf("正确凭据应换到 token: code=%d body=%s", w.Code, w.Body.String())
	}

	// verifier 不匹配拒绝
	code = authorizeForCode(t, r, sess.AccessToken, client, oidcVerifier)
	form = tokenForm(client, secret, code, oidcRedirectURI, "another-verifier-another-verifier-999999")
	w = doForm(r, "/api/oidc/token", form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("verifier 不匹配应 400, got %d body=%s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "INVALID_GRANT" {
		t.Fatalf("verifier 不匹配应返回 INVALID_GRANT, got %s", got)
	}

	// redirect_uri 与授权时不一致拒绝
	code = authorizeForCode(t, r, sess.AccessToken, client, oidcVerifier)
	form = tokenForm(client, secret, code, "https://app.example.com/other", oidcVerifier)
	w = doForm(r, "/api/oidc/token", form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("redirect_uri 不一致应 400, got %d body=%s", w.Code, w.Body.String())
	}

	// grant_type 非法拒绝
	code = authorizeForCode(t, r, sess.AccessToken, client, oidcVerifier)
	form = tokenForm(client, secret, code, oidcRedirectURI, oidcVerifier)
	form.Set("grant_type", "password")
	w = doForm(r, "/api/oidc/token", form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("grant_type=password 应 400, got %d body=%s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "UNSUPPORTED_GRANT_TYPE" {
		t.Fatalf("应返回 UNSUPPORTED_GRANT_TYPE, got %s", got)
	}
}

// TestOIDCTokenAcceptsJSON token 端点按契约同时支持 JSON 提交。
func TestOIDCTokenAcceptsJSON(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	oidcH := newOIDCHandlerForTest(t, g, cfg)
	authH := NewAuthHandler(g, cfg, testMailer())
	r := newOIDCRouter(t, g, cfg, oidcH, authH)

	_, sess, _ := registerUser(t, r, "tokjson@example.com", "tokjsonuser", "password123")
	client, secret := createOIDCClient(t, g, []string{oidcRedirectURI})
	code := authorizeForCode(t, r, sess.AccessToken, client, oidcVerifier)

	w := doJSON(r, http.MethodPost, "/api/oidc/token", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     client.ClientID,
		"client_secret": secret,
		"code":          code,
		"redirect_uri":  oidcRedirectURI,
		"code_verifier": oidcVerifier,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("JSON 提交换 token 应成功: code=%d body=%s", w.Code, w.Body.String())
	}
	if tok := decodeTokenResp(t, w); tok.AccessToken == "" {
		t.Fatal("JSON 提交应返回 access_token")
	}
}

// ===== userinfo 安全用例 =====

// TestOIDCUserinfoRejectsInvalidTokens 过期 OIDC token 返回 401 TOKEN_EXPIRED；
// 平台 HS256 会话 access token 不得当 OIDC token 使用（两套凭据不混用）。
func TestOIDCUserinfoRejectsInvalidTokens(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	oidcH := newOIDCHandlerForTest(t, g, cfg)
	authH := NewAuthHandler(g, cfg, testMailer())
	r := newOIDCRouter(t, g, cfg, oidcH, authH)

	user := createUser(t, g, "expired@example.com", "expireduser", "password123", false)
	client, _ := createOIDCClient(t, g, []string{oidcRedirectURI})

	// 直接构造过期 JWT（TTL 注入为负）
	expired, err := oidcH.signAccessToken(user.ID.String(), client.ClientID, "openid", -time.Minute)
	if err != nil {
		t.Fatalf("签发过期 token 失败: %v", err)
	}
	w := doAuthJSON(r, http.MethodGet, "/api/oidc/userinfo", expired, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("过期 token 应 401, got %d body=%s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "TOKEN_EXPIRED" {
		t.Fatalf("过期 token 应返回 TOKEN_EXPIRED, got %s", got)
	}

	// 平台 HS256 access token 不被 userinfo 接受
	w = doAuthJSON(r, http.MethodGet, "/api/oidc/userinfo", accessToken(t, cfg, &user), nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("平台 HS256 token 应 401, got %d", w.Code)
	}

	// 无 Bearer 应 401
	w = doJSON(r, http.MethodGet, "/api/oidc/userinfo", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("缺少 Bearer 应 401, got %d", w.Code)
	}
}

// ===== 密钥管理 =====

// TestOIDCKeyConfigFailFast 提供了 PEM 但解析失败时构造即报错（启动 fail-fast）。
func TestOIDCKeyConfigFailFast(t *testing.T) {
	g := newTestDB(t)
	t.Setenv("OIDC_JWKS_PRIVATE_KEY", "not-a-valid-pem")
	if _, err := NewOIDCHandler(g, testConfig(), identity.NewService(g)); err == nil {
		t.Fatal("PEM 解析失败应返回错误（启动即失败）")
	}
}

// TestOIDCTempKeyFallback 未配置 OIDC_JWKS_PRIVATE_KEY 时生成临时密钥，jwks 仍可用。
func TestOIDCTempKeyFallback(t *testing.T) {
	g := newTestDB(t)
	h, err := NewOIDCHandler(g, testConfig(), identity.NewService(g))
	if err != nil {
		t.Fatalf("未配置密钥时应回退临时密钥: %v", err)
	}
	r := gin.New()
	r.GET("/api/oidc/jwks.json", h.JWKS)
	w := doJSON(r, http.MethodGet, "/api/oidc/jwks.json", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("jwks 应可用: code=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	keys, ok := body["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("jwks 应含一把密钥: %s", w.Body.String())
	}
}
