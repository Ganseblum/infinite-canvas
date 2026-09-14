package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/model"
)

var testSecret = []byte("middleware-test-secret-middleware-test-secret")

func newAuthTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", Auth(testSecret), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"userId": c.GetString("user_id"),
			"role":   c.GetString("user_role"),
		})
	})
	return r
}

func authErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v body=%s", err, w.Body.String())
	}
	return body.Error.Code
}

func doAuthRequest(r *gin.Engine, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMissingToken(t *testing.T) {
	w := doAuthRequest(newAuthTestRouter(), "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应返回 401, got %d", w.Code)
	}
	if code := authErrorCode(t, w); code != "UNAUTHORIZED" {
		t.Fatalf("无 token 错误码应为 UNAUTHORIZED, got %s", code)
	}
}

func TestAuthInvalidToken(t *testing.T) {
	r := newAuthTestRouter()

	for _, header := range []string{"Bearer not-a-jwt", "Bearer " + signedToken(t, []byte("another-secret-another-secret-32"))} {
		w := doAuthRequest(r, header)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("无效 token 应返回 401, got %d header=%s", w.Code, header)
		}
		if code := authErrorCode(t, w); code != "UNAUTHORIZED" {
			t.Fatalf("无效 token 错误码应为 UNAUTHORIZED, got %s", code)
		}
	}
}

func TestAuthExpiredToken(t *testing.T) {
	expired := signedTokenAt(t, testSecret, time.Now().Add(-time.Hour), time.Now().Add(-time.Minute))
	w := doAuthRequest(newAuthTestRouter(), "Bearer "+expired)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("过期 token 应返回 401, got %d", w.Code)
	}
	if code := authErrorCode(t, w); code != "TOKEN_EXPIRED" {
		t.Fatalf("过期 token 错误码应为 TOKEN_EXPIRED, got %s", code)
	}
}

func TestAuthValidTokenWritesUserContext(t *testing.T) {
	uid := uuid.New()
	user := model.User{ID: uid, Role: "admin"}
	token, err := auth.IssueAccessToken(&user, testSecret)
	if err != nil {
		t.Fatalf("签发 access token 失败: %v", err)
	}
	w := doAuthRequest(newAuthTestRouter(), "Bearer "+token)
	if w.Code != http.StatusOK {
		t.Fatalf("合法 token 应返回 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.UserID != uid.String() || body.Role != "admin" {
		t.Fatalf("上下文用户信息不符: %+v", body)
	}
}

func signedToken(t *testing.T, secret []byte) string {
	t.Helper()
	now := time.Now()
	return signedTokenAt(t, secret, now, now.Add(auth.AccessTokenTTL))
}

func signedTokenAt(t *testing.T, secret []byte, issuedAt, expiresAt time.Time) string {
	t.Helper()
	claims := auth.AccessClaims{
		Sub:  uuid.NewString(),
		Role: "user",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}
	return token
}
