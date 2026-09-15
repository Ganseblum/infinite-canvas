package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/infinite-canvas/server/internal/crypto"
)

func TestUpstreamDownloadRejectsPrivateAndInvalidTargets(t *testing.T) {
	g := newServiceDB(t)
	cipher, err := crypto.New("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	upstream := NewUpstreamService(g, cipher, DefaultUpstreamTimeouts())

	cases := []string{
		"file:///etc/passwd",
		"http://127.0.0.1/secret",
		"http://localhost:8080/internal",
		"http://10.0.0.8/private",
		"http://192.168.1.10/router",
		"http://169.254.169.254/latest/meta-data",
	}
	for _, target := range cases {
		if _, _, err := upstream.Download(context.Background(), target, 1024, time.Second); err == nil {
			t.Errorf("内网或非法地址应被拒绝: %s", target)
		}
	}
}

func TestUpstreamDownloadFollowsRedirectsWithValidation(t *testing.T) {
	g := newServiceDB(t)
	cipher, _ := crypto.New("0123456789abcdef0123456789abcdef")
	upstream := NewUpstreamService(g, cipher, DefaultUpstreamTimeouts())

	// 先跳到一个内网地址，每一跳都要重新校验，因此必须拒绝。
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/private", http.StatusFound)
	}))
	defer redirector.Close()
	upstream.SetAllowPrivate(true) // 允许访问测试服务器本身
	upstream.SetAllowPrivate(false)

	if _, _, err := upstream.Download(context.Background(), redirector.URL, 1024, time.Second); err == nil {
		t.Fatalf("重定向到内网地址应被拒绝")
	}
}

func TestUpstreamStatusAndFailoverClassification(t *testing.T) {
	upstream := &providerErrStub{status: http.StatusBadGateway}
	if !ShouldFailover(upstream) || !IsRetryableUpstream(upstream) {
		t.Fatalf("502 应可重试并允许切换渠道")
	}
	upstream = &providerErrStub{status: http.StatusUnauthorized}
	if ShouldFailover(upstream) || IsRetryableUpstream(upstream) {
		t.Fatalf("401 不应重试也不应切换")
	}
	upstream = &providerErrStub{status: http.StatusTooManyRequests}
	if ShouldFailover(upstream) || IsRetryableUpstream(upstream) {
		t.Fatalf("上游 429 不应重试")
	}
	if UpstreamStatus(upstream) != http.StatusTooManyRequests {
		t.Fatalf("应从错误里取出上游状态码")
	}
}
