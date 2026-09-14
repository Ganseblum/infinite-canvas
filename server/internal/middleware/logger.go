package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/errs"
)

// RequestID 为每个请求生成或沿用 X-Request-Id。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = randomID()
		}
		c.Set("request_id", id)
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "req-" + time.Now().Format("150405.000")
	}
	return hex.EncodeToString(buf)
}

// Logger 记录请求日志，脱敏密码与令牌字段。
func Logger(level slog.Level) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		dur := time.Since(start)
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"dur_ms", dur.Milliseconds(),
			"ip", ClientIP(c),
			"request_id", c.GetString("request_id"),
		}
		if uid, ok := c.Get("user_id"); ok {
			attrs = append(attrs, "user_id", uid)
		}
		switch {
		case status >= 500:
			slog.Error("request", attrs...)
		case status >= 400:
			slog.Warn("request", attrs...)
		default:
			slog.Debug("request", attrs...)
		}
		if dur > time.Second {
			slog.Warn("slow request", attrs...)
		}
	}
}

// Recovery 捕获 panic，统一返回 INTERNAL_ERROR。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic recovered", "err", r, "path", c.Request.URL.Path)
				errs.Abort(c, errs.ErrInternal)
			}
		}()
		c.Next()
	}
}

// ClientIP 返回真实客户端 IP。
// 仅当 RemoteAddr 命中 TRUSTED_PROXIES 配置的受信网段时，gin 才会采用
// X-Forwarded-For / X-Real-IP；否则一律回退 RemoteAddr，防止伪造代理头绕过限流。
func ClientIP(c *gin.Context) string {
	return c.ClientIP()
}
