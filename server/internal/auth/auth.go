// Package auth 提供认证原语：bcrypt 密码哈希、HS256 会话与媒体令牌的签发
// 校验、随机令牌生成与摘要。令牌校验失败只分过期与无效两类，由调用方
// 映射为不同错误码。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/infinite-canvas/server/internal/model"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
	EmailTokenTTL   = 24 * time.Hour
	// ResetPasswordTokenTTL 是重置密码令牌的专用有效期：账号接管路径上的凭据
	// 可用窗口要压到 1 小时（总规划），不能与验证邮件共用 24 小时。
	ResetPasswordTokenTTL = 1 * time.Hour

	// MediaTokenTTL 与 refresh token 同周期：媒体 cookie 在每次刷新时轮换。
	MediaTokenTTL = 30 * 24 * time.Hour
	// MediaCookieName / MediaCookiePath 限定这枚只读凭据的可见路径。
	MediaCookieName = "ic_media"
	MediaCookiePath = "/api/v1/media"
	// MediaScope 是媒体 JWT 的唯一合法 scope。业务接口的 access token
	// 不带 scope，解析到 scope=media 的令牌一律拒绝，避免两套凭据混用。
	MediaScope = "media"
)

// HashPassword 用 bcrypt（cost 12）哈希密码。
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword 校验明文密码与 bcrypt 哈希是否匹配。
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// AccessClaims 是平台 access token 的 claims：用户 ID、角色与可选 scope。
type AccessClaims struct {
	Sub   string `json:"sub"`
	Role  string `json:"role"`
	Scope string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

// IssueAccessToken 签发 HS256 access token（15 分钟），业务接口鉴权用。
func IssueAccessToken(user *model.PlatformUser, secret []byte) (string, error) {
	now := time.Now()
	claims := AccessClaims{
		Sub:  user.ID.String(),
		Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseAccessToken 验签并解析 access token：过期返回 ErrTokenExpired，
// 其余任何失败（含 scope=media 的媒体令牌）返回 ErrInvalidToken。
func ParseAccessToken(tokenStr string, secret []byte) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &AccessClaims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("签名算法不匹配")
		}
		return secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*AccessClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	// 媒体 cookie 用同一把密钥签发，靠 scope 区分；scope=media 的 JWT
	// 不得当 access token 使用，否则两套凭据的边界会被抹平。
	if claims.Scope == MediaScope {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// MediaClaims 是 ic_media cookie 的 claims：sub、scope 与签发时的媒体令牌版本。
type MediaClaims struct {
	Sub   string `json:"sub"`
	Scope string `json:"scope"`
	// Ver 是签发时用户的 media_token_version；中间件校验与当前值一致，
	// 改密/重置/封禁递增版本后旧媒体令牌立即失效（差异清单 #9）。
	Ver int `json:"ver,omitempty"`
	jwt.RegisteredClaims
}

// IssueMediaToken 独立签发一枚只读媒体 JWT，有效期与 refresh token 同为 30 天。
// ver 传签发时用户的 MediaTokenVersion，撤销时靠递增版本让旧令牌失效。
func IssueMediaToken(userID uuid.UUID, secret []byte, ver int) (string, error) {
	now := time.Now()
	claims := MediaClaims{
		Sub:   userID.String(),
		Scope: MediaScope,
		Ver:   ver,
		RegisteredClaims: jwt.RegisteredClaims{
			// jti 保证同一秒内重新签发也是不同的令牌，刷新时可见轮换。
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(MediaTokenTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseMediaToken 校验签名后强制要求 scope=media，任何一步不过都返回错误。
func ParseMediaToken(tokenStr string, secret []byte) (*MediaClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &MediaClaims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("签名算法不匹配")
		}
		return secret, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*MediaClaims)
	if !ok || !token.Valid || claims.Scope != MediaScope {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// 令牌校验失败的两类口径：过期可提示重新登录，无效一律拒绝。
var (
	ErrTokenExpired = errors.New("token expired")
	ErrInvalidToken = errors.New("invalid token")
)

// NewRefreshToken 生成 32 字节随机值 base64url 编码的 refresh token，
// 返回明文与 sha256 哈希（明文只在下发瞬间存在）。
func NewRefreshToken() (plain string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashToken(plain), nil
}

// HashToken 返回令牌明文的 sha256 base64url 摘要；数据库只存该哈希，泄库也无法直接登录。
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// HashEmail 返回规范化邮箱的 sha256 十六进制摘要，用作限流 key，
// 避免明文邮箱出现在内存 map 或日志里。
func HashEmail(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// NewEmailToken 生成用于邮件验证/重置的令牌。
func NewEmailToken() (plain string, hash string, err error) {
	return NewRefreshToken()
}

// NewTemporaryPassword 生成管理员建号时交付的一次性临时密码。
// crypto/rand.Text() 使用 base32 字符集（A-Z2-7，不含 0/1 等易混淆字符），26 位约 130 位熵。
func NewTemporaryPassword() string { return rand.Text() }

// RoleUser 是新建用户的默认角色。
const RoleUser = "user"
