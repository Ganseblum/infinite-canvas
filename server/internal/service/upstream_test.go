package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/provider"
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

// TestUpstreamPredicatesRequireTaggedUpstreamErrors 谓词收紧后的完整分类表：
// 只有连接层失败（Status:0）与上游 5xx 可重试；上游明确拒绝与未打标的本地错误一律不重试。
func TestUpstreamPredicatesRequireTaggedUpstreamErrors(t *testing.T) {
	retryable := []error{
		&provider.ErrUpstream{Status: 0},
		&provider.ErrUpstream{Status: http.StatusInternalServerError},
		&provider.ErrUpstream{Status: http.StatusBadGateway},
		&provider.ErrUpstream{Status: http.StatusServiceUnavailable},
		&provider.ErrUpstream{Status: http.StatusGatewayTimeout},
	}
	for _, err := range retryable {
		if !IsRetryableUpstream(err) || !ShouldFailover(err) {
			t.Errorf("err=%v 应可原渠道重试并允许切换渠道", err)
		}
	}
	nonRetryable := []error{
		&provider.ErrUpstream{Status: http.StatusTooManyRequests},
		&provider.ErrUpstream{Status: http.StatusBadRequest},
		&provider.ErrUpstream{Status: http.StatusUnprocessableEntity},
		&provider.ErrUpstream{Status: http.StatusUnauthorized},
		&provider.ErrUpstream{Status: http.StatusForbidden},
	}
	for _, err := range nonRetryable {
		if IsRetryableUpstream(err) || ShouldFailover(err) {
			t.Errorf("err=%v 不应重试也不应切换", err)
		}
	}
	unclassified := []error{
		errors.New("普通本地错误"),
		context.Canceled,
		context.DeadlineExceeded,
	}
	for _, err := range unclassified {
		if IsRetryableUpstream(err) || ShouldFailover(err) {
			t.Errorf("未打标错误 %v 不允许重试或切换", err)
		}
	}
	// 能力不支持：不切换（错误响应由 handler 统一映射成 MODEL_NOT_SUPPORTED）。
	if ShouldFailover(provider.ErrCapabilityUnsupported) || IsRetryableUpstream(provider.ErrCapabilityUnsupported) {
		t.Fatalf("能力不支持不应重试也不应切换")
	}
	// Status:0 的错误在 UpstreamStatus 链路里自然取到 0，错误响应落到 UPSTREAM_ERROR 默认分支。
	if UpstreamStatus(&provider.ErrUpstream{Status: 0}) != 0 {
		t.Fatalf("连接层失败的 UpstreamStatus 应为 0")
	}
}
