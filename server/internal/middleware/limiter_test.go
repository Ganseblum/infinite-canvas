package middleware

import (
	"testing"
	"time"
)

// TestLimiterRetryAfterSeconds 限流器的重试等待对外契约是秒：
// 曾把 time.Duration 纳秒差值直接当秒返回，导致 Retry-After 头出现天文数字。
func TestLimiterRetryAfterSeconds(t *testing.T) {
	l := NewLimiter(10*time.Minute, 2)
	now := time.Now()
	if ok, _ := l.Allow("k", now); !ok {
		t.Fatal("窗口内第一次应放行")
	}
	if ok, _ := l.Allow("k", now.Add(time.Second)); !ok {
		t.Fatal("窗口内第二次应放行")
	}
	ok, retryAfter := l.Allow("k", now.Add(2*time.Second))
	if ok {
		t.Fatal("第三次应被限流")
	}
	// 窗口 10 分钟，已过 2 秒，剩余等待应为 598 秒左右，绝不可能是纳秒量级。
	if retryAfter <= 0 || retryAfter > int(10*time.Minute/time.Second) {
		t.Fatalf("Retry-After 应为窗口内的秒数(1-600), got %d", retryAfter)
	}
	if _, retryAfter2 := l.IsBlocked("k", now.Add(2*time.Second)); retryAfter2 <= 0 || retryAfter2 > int(10*time.Minute/time.Second) {
		t.Fatalf("IsBlocked 的 Retry-After 应为窗口内的秒数(1-600), got %d", retryAfter2)
	}
}
