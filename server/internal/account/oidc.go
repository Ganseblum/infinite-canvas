package account

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
)

const (
	// oidcCodeTTL 授权码有效期：PLAN-PLATFORM-ACCOUNT-MEMBERSHIP T09「TTL 10 分钟内自定」取上限 10 分钟。
	oidcCodeTTL = 10 * time.Minute
	// oidcAccessTokenTTL OIDC access/id_token 有效期：评审门④「token 15min 有效期」契约值。
	oidcAccessTokenTTL = 15 * time.Minute
	// oidcEnvPrivateKey 是 RS256 签名私钥的环境变量名（PEM PKCS#8）。
	// 在 handler 内单独读取而不进 config.Config：OIDC 密钥是专属凭据，
	// 且 config 包不在 T09 的文件清单内；main.go 启动时已加载 .env，这里能读到。
	oidcEnvPrivateKey = "OIDC_JWKS_PRIVATE_KEY"
)

// OIDC 端点沿用 errs 统一响应外壳，code 采用 RFC 6749 的 OAuth 2.0 标准错误码。
var (
	errOIDCInvalidRequest   = errs.New(http.StatusBadRequest, "INVALID_REQUEST", "授权请求参数无效")
	errOIDCInvalidClient    = errs.New(http.StatusUnauthorized, "INVALID_CLIENT", "客户端认证失败")
	errOIDCInvalidGrant     = errs.New(http.StatusBadRequest, "INVALID_GRANT", "授权码无效、已使用或已过期")
	errOIDCUnsupportedGrant = errs.New(http.StatusBadRequest, "UNSUPPORTED_GRANT_TYPE", "不支持的 grant_type")
)

// ===== 签名密钥 =====

// oidcSigningKey 是 RS256 签名密钥与其 kid（公钥 SHA-256 前 16 位十六进制）。
type oidcSigningKey struct {
	key *rsa.PrivateKey
	kid string
}

// newOIDCSigningKey 优先解析 OIDC_JWKS_PRIVATE_KEY（PEM PKCS#8）；
// 提供了但解析失败直接报错（fail-fast，沿仓库口径）；
// 未配置时生成临时 RSA 2048——重启后旧 token 全部失效，生产环境必须显式配置。
func newOIDCSigningKey(pemKey string) (*oidcSigningKey, error) {
	if strings.TrimSpace(pemKey) == "" {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		slog.Warn("OIDC_JWKS_PRIVATE_KEY 未配置，已生成临时 RS256 密钥：重启后旧 token 失效，生产环境必须配置")
		return newOIDCSigningKeyWithRSA(key)
	}
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("OIDC_JWKS_PRIVATE_KEY 不是有效的 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("OIDC_JWKS_PRIVATE_KEY 解析失败（需要 PKCS#8 RSA 私钥）: " + err.Error())
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("OIDC_JWKS_PRIVATE_KEY 必须是 RSA 私钥")
	}
	return newOIDCSigningKeyWithRSA(key)
}

func newOIDCSigningKeyWithRSA(key *rsa.PrivateKey) (*oidcSigningKey, error) {
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	return &oidcSigningKey{key: key, kid: hex.EncodeToString(sum[:])[:16]}, nil
}

// ===== 进程内一次性授权码 =====

// oidcCodeEntry 是一条已签发授权码的绑定信息：code 只与签发时的
// 用户、客户端、redirect_uri、scope 与 PKCE challenge 绑定，换 token 时逐项复核。
type oidcCodeEntry struct {
	userID      uuid.UUID
	clientID    string
	redirectURI string
	scope       string
	challenge   string
	expiresAt   time.Time
}

// oidcCodeStore 进程内一次性授权码存储（单实例够用口径，同 middleware.Limiter 的注释）：
// 重启即失效、多实例不共享，持久化表不在本期契约内（PLAN T09）；
// 水平扩容或多实例部署时需换共享存储。
type oidcCodeStore struct {
	mu    sync.Mutex
	codes map[string]oidcCodeEntry
}

func newOIDCCodeStore() *oidcCodeStore {
	return &oidcCodeStore{codes: make(map[string]oidcCodeEntry)}
}

func (s *oidcCodeStore) save(code string, entry oidcCodeEntry, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.codes {
		if now.After(v.expiresAt) {
			delete(s.codes, k)
		}
	}
	s.codes[code] = entry
}

// consume 一次性消费授权码：并发使用同一 code 时只有一个请求成功。
func (s *oidcCodeStore) consume(code string, now time.Time) (oidcCodeEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.codes[code]
	if !ok {
		return oidcCodeEntry{}, false
	}
	delete(s.codes, code)
	if now.After(entry.expiresAt) {
		return oidcCodeEntry{}, false
	}
	return entry, true
}

// ===== Handler =====

// OIDCHandler 实现 OIDC Provider 四端点（PLAN T09）：
// authorize / token / userinfo / jwks.json。
type OIDCHandler struct {
	db    *gorm.DB
	cfg   *config.Config
	idn   *identity.Service
	key   *oidcSigningKey
	codes *oidcCodeStore
}

// NewOIDCHandler 构造 OIDC Provider。签名密钥按 newOIDCSigningKey 的口径解析或生成，
// 构造失败（PEM 配置无效等）由调用方启动即失败。
func NewOIDCHandler(db *gorm.DB, cfg *config.Config, idn *identity.Service) (*OIDCHandler, error) {
	key, err := newOIDCSigningKey(os.Getenv(oidcEnvPrivateKey))
	if err != nil {
		return nil, err
	}
	return &OIDCHandler{db: db, cfg: cfg, idn: idn, key: key, codes: newOIDCCodeStore()}, nil
}

// newOAuthClientID 生成「ic_ + 32 位十六进制随机数」的客户端 ID（T10 admin 管理端与测试共用）。
func NewOAuthClientID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "ic_" + hex.EncodeToString(buf), nil
}

// hashOAuthClientSecret 返回客户端密钥的 SHA-256 十六进制摘要：库内只存摘要，不存明文。
func HashOAuthClientSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// oauthClientRedirectURIs 解析注册的回调地址 JSON 数组。
func oauthClientRedirectURIs(client *model.OAuthClient) []string {
	var uris []string
	_ = json.Unmarshal(client.RedirectURIs, &uris)
	return uris
}

// loadClient 按 client_id 精确查找启用中的客户端；不存在、停用或参数为空一律 INVALID_REQUEST。
func (h *OIDCHandler) loadClient(clientID string) (*model.OAuthClient, *errs.AppError) {
	if clientID == "" {
		return nil, errOIDCInvalidRequest
	}
	var client model.OAuthClient
	if err := h.db.First(&client, "client_id = ?", clientID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errOIDCInvalidRequest
		}
		slog.Error("查询 OAuth 客户端失败", "err", err)
		return nil, errs.ErrInternal
	}
	if !client.Enabled {
		return nil, errOIDCInvalidRequest
	}
	return &client, nil
}

// Authorize GET /api/oidc/authorize：登录用户为已注册客户端签发一次性授权码并 302 回跳。
// 路由挂在 Auth(secret) + RequireActiveUser(idn) 之后，进入时已是登录且未封禁的账号。
// client_id 精确匹配；redirect_uri 必须与注册的完全一致，未注册一律 400 JSON、绝不重定向
// （评审门④）；response_type=code、scope 含 openid、PKCE 强制 S256（plain 拒绝、缺 challenge 拒绝）。
func (h *OIDCHandler) Authorize(c *gin.Context) {
	q := c.Request.URL.Query()
	client, appErr := h.loadClient(q.Get("client_id"))
	if appErr != nil {
		errs.Abort(c, appErr)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if !slices.Contains(oauthClientRedirectURIs(client), redirectURI) {
		errs.Abort(c, errOIDCInvalidRequest)
		return
	}
	if q.Get("response_type") != "code" || !slices.Contains(strings.Fields(q.Get("scope")), "openid") {
		errs.Abort(c, errOIDCInvalidRequest)
		return
	}
	challenge := q.Get("code_challenge")
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		errs.Abort(c, errOIDCInvalidRequest)
		return
	}
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	code, _, err := auth.NewRefreshToken()
	if err != nil {
		slog.Error("生成授权码失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	now := time.Now()
	h.codes.save(code, oidcCodeEntry{
		userID:      uid,
		clientID:    client.ClientID,
		redirectURI: redirectURI,
		scope:       q.Get("scope"),
		challenge:   challenge,
		expiresAt:   now.Add(oidcCodeTTL),
	}, now)
	u, err := url.Parse(redirectURI)
	if err != nil {
		errs.Abort(c, errOIDCInvalidRequest)
		return
	}
	qry := u.Query()
	qry.Set("code", code)
	if state := q.Get("state"); state != "" {
		qry.Set("state", state)
	}
	u.RawQuery = qry.Encode()
	c.Redirect(http.StatusFound, u.String())
}

type oidcTokenReq struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
}

// Token POST /api/oidc/token：客户端凭 client_secret + 授权码 + PKCE verifier
// 换 RS256 access_token（15 分钟）。Content-Type 支持 form 或 JSON（PLAN T09）。
func (h *OIDCHandler) Token(c *gin.Context) {
	var req oidcTokenReq
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&req); err != nil {
			errs.Abort(c, errOIDCInvalidRequest)
			return
		}
	} else {
		req.GrantType = c.PostForm("grant_type")
		req.ClientID = c.PostForm("client_id")
		req.ClientSecret = c.PostForm("client_secret")
		req.Code = c.PostForm("code")
		req.RedirectURI = c.PostForm("redirect_uri")
		req.CodeVerifier = c.PostForm("code_verifier")
	}
	if req.GrantType != "authorization_code" {
		errs.Abort(c, errOIDCUnsupportedGrant)
		return
	}
	client, appErr := h.loadClient(req.ClientID)
	if appErr != nil {
		errs.Abort(c, appErr)
		return
	}
	if subtle.ConstantTimeCompare([]byte(HashOAuthClientSecret(req.ClientSecret)), []byte(client.ClientSecretHash)) != 1 {
		errs.Abort(c, errOIDCInvalidClient)
		return
	}
	// code 一次性消费；client_id、redirect_uri、verifier 任一与授权时绑定不一致即拒绝。
	entry, ok := h.codes.consume(req.Code, time.Now())
	if !ok || entry.clientID != client.ClientID || entry.redirectURI != req.RedirectURI {
		errs.Abort(c, errOIDCInvalidGrant)
		return
	}
	if !verifyPKCES256(req.CodeVerifier, entry.challenge) {
		errs.Abort(c, errOIDCInvalidGrant)
		return
	}
	accessToken, err := h.signAccessToken(entry.userID.String(), client.ClientID, entry.scope, oidcAccessTokenTTL)
	if err != nil {
		slog.Error("签发 OIDC access token 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	idToken, err := h.signIDToken(entry.userID.String(), client.ClientID, oidcAccessTokenTTL)
	if err != nil {
		slog.Error("签发 OIDC id_token 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(oidcAccessTokenTTL.Seconds()),
		"scope":        entry.scope,
		// id_token 是 PLAN T09 允许的可选实现（claims 仅 sub+aud），scope 恒含 openid。
		"id_token": idToken,
	})
}

// verifyPKCES256 按 RFC 7636 S256 复核：challenge == base64url(sha256(verifier))。
func verifyPKCES256(verifier, challenge string) bool {
	if verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// oidcAccessClaims 是 OIDC access token 的 claims：iss（APP_BASE_URL）、sub、aud、scope、exp/iat。
type oidcAccessClaims struct {
	Scope string `json:"scope"`
	jwt.RegisteredClaims
}

func (h *OIDCHandler) issuer() string { return strings.TrimRight(h.cfg.AppBaseURL, "/") }

// signAccessToken 签发 RS256 access token 并在 header 写入 kid；ttl 参数供测试注入极短有效期。
func (h *OIDCHandler) signAccessToken(sub, aud, scope string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := oidcAccessClaims{
		Scope: scope,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    h.issuer(),
			Subject:   sub,
			Audience:  jwt.ClaimStrings{aud},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = h.key.kid
	return token.SignedString(h.key.key)
}

// signIDToken 签发 id_token（PLAN T09 允许的可选实现，claims 仅 sub/aud + iss/exp/iat）。
func (h *OIDCHandler) signIDToken(sub, aud string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    h.issuer(),
		Subject:   sub,
		Audience:  jwt.ClaimStrings{aud},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		ID:        uuid.NewString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = h.key.kid
	return token.SignedString(h.key.key)
}

// parseOIDCAccessToken 验签 + exp 校验（jwt/v5 解析时默认校验 exp），只接受本服务
// RS256 签发的 token；平台 HS256 会话令牌在这里不合法——两套凭据不得混用。
func (h *OIDCHandler) parseOIDCAccessToken(tokenStr string) (*oidcAccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &oidcAccessClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, errors.New("签名算法不匹配")
		}
		return &h.key.key.PublicKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, auth.ErrTokenExpired
		}
		return nil, auth.ErrInvalidToken
	}
	claims, ok := token.Claims.(*oidcAccessClaims)
	if !ok || !token.Valid {
		return nil, auth.ErrInvalidToken
	}
	return claims, nil
}

// Userinfo GET /api/oidc/userinfo：认证 OIDC 签发的 RS256 access token（Bearer，验签 + exp），
// 返回当前用户基础档案。不走 middleware.Auth(secret)——那是平台 HS256 会话令牌，
// 与 OIDC 签发的 access token 是两套凭据（PLAN T09 全链路：token → userinfo）。
func (h *OIDCHandler) Userinfo(c *gin.Context) {
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	claims, err := h.parseOIDCAccessToken(strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		if errors.Is(err, auth.ErrTokenExpired) {
			errs.Abort(c, errs.ErrTokenExpired)
			return
		}
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	user, err := h.idn.GetByID(c.Request.Context(), uid)
	if err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if user.Status == "disabled" {
		errs.Abort(c, errs.ErrAccountDisabled)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub":           user.ID.String(),
		"email":         user.Email,
		"username":      user.Username,
		"displayName":   user.DisplayName,
		"emailVerified": user.EmailVerifiedAt != nil,
	})
}

// JWKS GET /api/oidc/jwks.json：公开 RS256 公钥 JWKS，字段名对齐 JWK 标准。
func (h *OIDCHandler) JWKS(c *gin.Context) {
	pub := h.key.key.PublicKey
	c.JSON(http.StatusOK, gin.H{
		"keys": []gin.H{{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": h.key.kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}
