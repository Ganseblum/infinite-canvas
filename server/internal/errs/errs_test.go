package errs

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWithFieldsAndRetryAfterDoNotShareState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	first := WithFields(ErrValidation, map[string]string{"email": "邮箱格式不正确"})
	second := WithRetryAfter(ErrRateLimited, 30)
	third := WithFields(ErrValidation, map[string]string{"username": "用户名已占用"})

	if ErrValidation.Fields != nil {
		t.Fatalf("包级单例 ErrValidation.Fields 被污染: %v", ErrValidation.Fields)
	}
	if ErrRateLimited.RetryAfter != 0 {
		t.Fatalf("包级单例 ErrRateLimited.RetryAfter 被污染: %d", ErrRateLimited.RetryAfter)
	}
	if first == ErrValidation || second == ErrRateLimited {
		t.Fatal("With* 必须返回副本而不是修改单例")
	}
	if third.Fields["email"] != "" || first.Fields["username"] != "" {
		t.Fatal("两个错误的 fields 不应互相影响")
	}

	type errBody struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}

	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	Abort(c1, first)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	Abort(c2, second)

	var body1 errBody
	if err := json.Unmarshal(w1.Body.Bytes(), &body1); err != nil {
		t.Fatalf("解析第一个响应失败: %v", err)
	}
	if body1.Error.Fields["email"] == "" {
		t.Fatalf("第一个响应应带 email 字段错误: %s", w1.Body.String())
	}
	if w1.Header().Get("Retry-After") != "" {
		t.Fatal("第一个响应不应带 Retry-After")
	}

	var body2 errBody
	if err := json.Unmarshal(w2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("解析第二个响应失败: %v", err)
	}
	if body2.Error.Fields != nil {
		t.Fatalf("第二个响应不应串入第一个请求的 fields: %s", w2.Body.String())
	}
	if w2.Header().Get("Retry-After") != "30" {
		t.Fatalf("第二个响应应带 Retry-After=30, got %s", w2.Header().Get("Retry-After"))
	}
}
