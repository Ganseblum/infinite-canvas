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

	// MediaTokenTTL 与 refresh token 同周期：媒体 cookie 在每次刷新时轮换。
	MediaTokenTTL = 30 * 24 * time.Hour
	// MediaCookieName / MediaCookiePath 限定这枚只读凭据的可见路径。
	MediaCookieName = "ic_media"
	MediaCookiePath = "/api/media"
	// MediaScope 是媒体 JWT 的唯一合法 scope。业务接口的 access token
	// 不带 scope，解析到 scope=media 的令牌一律拒绝，避免两套凭据混用。
	MediaScope = "media"
)

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type AccessClaims struct {
	Sub   string `json:"sub"`
	Role  string `json:"role"`
	Scope string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

func IssueAccessToken(user *model.User, secret []byte) (string, error) {
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

// MediaClaims 是 ic_media cookie 的 claims，只放 sub 与 scope。
type MediaClaims struct {
	Sub   string `json:"sub"`
	Scope string `json:"scope"`
	jwt.RegisteredClaims
}

// IssueMediaToken 独立签发一枚只读媒体 JWT，有效期与 refresh token 同为 30 天。
func IssueMediaToken(userID uuid.UUID, secret []byte) (string, error) {
	now := time.Now()
	claims := MediaClaims{
		Sub:   userID.String(),
		Scope: MediaScope,
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

// RoleUser 是新建用户的默认角色。
const RoleUser = "user"
