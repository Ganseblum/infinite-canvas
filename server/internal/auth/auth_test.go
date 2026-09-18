package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
)

func TestMediaTokenScopeBoundary(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	uid := uuid.New()

	mediaToken, err := IssueMediaToken(uid, secret, 0)
	if err != nil {
		t.Fatalf("签发媒体令牌失败: %v", err)
	}
	claims, err := ParseMediaToken(mediaToken, secret)
	if err != nil {
		t.Fatalf("解析媒体令牌失败: %v", err)
	}
	if claims.Sub != uid.String() || claims.Scope != MediaScope {
		t.Fatalf("媒体令牌 claims 不符: %+v", claims)
	}
	// 媒体令牌不得当 access token 用
	if _, err := ParseAccessToken(mediaToken, secret); err == nil {
		t.Fatal("scope=media 的 JWT 不应通过 access token 解析")
	}

	// access token 不得当媒体 cookie 用
	user := model.User{ID: uid, Role: "user"}
	access, err := IssueAccessToken(&user, secret)
	if err != nil {
		t.Fatalf("签发 access token 失败: %v", err)
	}
	if _, err := ParseMediaToken(access, secret); err == nil {
		t.Fatal("access token 不应通过媒体令牌解析")
	}
	// 换密钥签的媒体令牌不通过
	if _, err := ParseMediaToken(mediaToken, []byte(strings.Repeat("x", 32))); err == nil {
		t.Fatal("错误密钥不应验签通过")
	}
}

func TestHashEmailIsNormalizedAndOpaque(t *testing.T) {
	if HashEmail("Alice@Example.com ") != HashEmail("alice@example.com") {
		t.Fatal("邮箱哈希应忽略大小写与首尾空格")
	}
	if HashEmail("a@example.com") == HashEmail("b@example.com") {
		t.Fatal("不同邮箱应得到不同哈希")
	}
	if HashEmail("a@example.com") == "a@example.com" {
		t.Fatal("哈希不应等于明文邮箱")
	}
}
