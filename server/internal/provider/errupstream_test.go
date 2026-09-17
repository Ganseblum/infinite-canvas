package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deadBaseURL 指向 127.0.0.1 的 1 号端口，必然连接失败（未收到任何响应）。
const deadBaseURL = "http://127.0.0.1:1"

// TestUpstreamConnectionFailureClassified 连接层失败按约定打 ErrUpstream{Status:0}，
// 三份 provider 的全部出站入口都要打标，供重试与切渠道判定使用。
func TestUpstreamConnectionFailureClassified(t *testing.T) {
	cases := []struct {
		name string
		call func() error
	}{
		{"openai Images", func() error {
			_, err := NewOpenAI(deadBaseURL, "k", nil).Images(context.Background(), ImageRequest{Model: "m", Prompt: "p", N: 1})
			return err
		}},
		{"openai Speech", func() error {
			_, err := NewOpenAI(deadBaseURL, "k", nil).Speech(context.Background(), SpeechRequest{Model: "tts-1", Input: "你好"})
			return err
		}},
		{"openai CreateVideo", func() error {
			_, err := NewOpenAI(deadBaseURL, "k", nil).CreateVideo(context.Background(), VideoRequest{Model: "sora-2", Prompt: "p", Duration: 5})
			return err
		}},
		{"openai PollVideo", func() error {
			_, err := NewOpenAI(deadBaseURL, "k", nil).PollVideo(context.Background(), VideoTask{Provider: "openai", UpstreamTaskID: "t"})
			return err
		}},
		{"openai ChatStream", func() error {
			return NewOpenAI(deadBaseURL, "k", nil).ChatStream(context.Background(), ChatRequest{Model: "gpt-5"}, &recordingSink{})
		}},
		{"gemini Images", func() error {
			_, err := NewGemini(deadBaseURL, "k", nil).Images(context.Background(), ImageRequest{Model: "m", Prompt: "p", N: 1})
			return err
		}},
		{"gemini CreateVideo", func() error {
			_, err := NewGemini(deadBaseURL, "k", nil).CreateVideo(context.Background(), VideoRequest{Model: "veo", Prompt: "p", Duration: 8})
			return err
		}},
		{"gemini PollVideo", func() error {
			_, err := NewGemini(deadBaseURL, "k", nil).PollVideo(context.Background(), VideoTask{Provider: "gemini", UpstreamTaskID: "t"})
			return err
		}},
		{"gemini ChatStream", func() error {
			return NewGemini(deadBaseURL, "k", nil).ChatStream(context.Background(), ChatRequest{Model: "gemini-2"}, &recordingSink{})
		}},
		{"ark CreateVideo", func() error {
			_, err := NewArk(deadBaseURL, "k", nil).CreateVideo(context.Background(), VideoRequest{Model: "seedance", Prompt: "p", Duration: 5})
			return err
		}},
		{"ark PollVideo", func() error {
			_, err := NewArk(deadBaseURL, "k", nil).PollVideo(context.Background(), VideoTask{Provider: "ark", UpstreamTaskID: "t"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			var up *ErrUpstream
			if !errors.As(err, &up) {
				t.Fatalf("连接失败应被打标为 ErrUpstream, got %v (%T)", err, err)
			}
			if up.Status != 0 {
				t.Fatalf("未收到响应时 Status 应为 0, got %d", up.Status)
			}
			if up.Body == "" {
				t.Fatal("Body 应保留原始错误信息供排查")
			}
		})
	}
}

// TestUpstreamCanceledNotClassified 取消与超时必须原样上抛，不得打标，
// 否则会被重试表误判成「可重试的连接失败」。
func TestUpstreamCanceledNotClassified(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := []struct {
		name string
		call func() error
	}{
		{"openai Images", func() error {
			_, err := NewOpenAI(deadBaseURL, "k", nil).Images(ctx, ImageRequest{Model: "m", Prompt: "p", N: 1})
			return err
		}},
		{"openai ChatStream", func() error {
			return NewOpenAI(deadBaseURL, "k", nil).ChatStream(ctx, ChatRequest{Model: "gpt-5"}, &recordingSink{})
		}},
		{"gemini Images", func() error {
			_, err := NewGemini(deadBaseURL, "k", nil).Images(ctx, ImageRequest{Model: "m", Prompt: "p", N: 1})
			return err
		}},
		{"ark CreateVideo", func() error {
			_, err := NewArk(deadBaseURL, "k", nil).CreateVideo(ctx, VideoRequest{Model: "seedance", Prompt: "p", Duration: 5})
			return err
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("取消的错误应原样上抛, got %v", err)
			}
			var up *ErrUpstream
			if errors.As(err, &up) {
				t.Fatalf("取消的错误不允许被打标, got %#v", up)
			}
		})
	}
}

// TestOpenAIImagesShapeErrorIsUpstream 2xx 但响应无法解析归一为 502，
// 与 SSE 协议失败的既有约定一致，收紧后的重试判定才能看见这类错误。
func TestOpenAIImagesShapeErrorIsUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()
	_, err := NewOpenAI(server.URL, "k", server.Client()).Images(context.Background(), ImageRequest{Model: "m", Prompt: "p", N: 1})
	var up *ErrUpstream
	if !errors.As(err, &up) {
		t.Fatalf("响应形状异常应被打标为 ErrUpstream, got %v (%T)", err, err)
	}
	if up.Status != http.StatusBadGateway {
		t.Fatalf("Status = %d, want 502", up.Status)
	}
	if !strings.Contains(up.Body, "解析图像响应失败") {
		t.Fatalf("Body = %q, 应包含解析失败信息", up.Body)
	}
}

// TestGeminiChatStreamRejectsNonSSEBody 与 openai.go 同一判据：200 但整条流
// 没有任何 data 行就是非 SSE，必须报错并带响应体摘要，不能当成空流成功收场。
func TestGeminiChatStreamRejectsNonSSEBody(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		wantContain string
	}{
		{"JSON 错误体", "application/json", `{"error":{"code":404,"message":"model not found"}}`, "model not found"},
		{"HTML 错误页", "text/html", "<html><body>502 Bad Gateway</body></html>", "502 Bad Gateway"},
		{"空响应体", "text/event-stream", "", "SSE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			sink := &recordingSink{}
			err := NewGemini(server.URL, "k", server.Client()).ChatStream(context.Background(), ChatRequest{Model: "gemini-2"}, sink)
			var up *ErrUpstream
			if !errors.As(err, &up) {
				t.Fatalf("应返回 ErrUpstream, got %v (%T)", err, err)
			}
			if up.Status != http.StatusBadGateway {
				t.Fatalf("状态码 = %d, want 502", up.Status)
			}
			if !strings.Contains(up.Body, "不是 SSE 流") || !strings.Contains(up.Body, tc.wantContain) {
				t.Fatalf("错误信息 %q 应包含非 SSE 提示与摘要 %q", up.Body, tc.wantContain)
			}
			if len(sink.deltas) != 0 || len(sink.calls) != 0 || sink.done != "" {
				t.Fatalf("非 SSE 响应不应产出内容或调用 Done: deltas=%v calls=%v done=%q", sink.deltas, sink.calls, sink.done)
			}
		})
	}
}

// TestGeminiChatStreamSSEFrameStillWorks 有 data 行（含 [DONE]）仍按成功收场，
// 固化「非 SSE」判据不会误伤纯心跳夹正常事件的形状。
func TestGeminiChatStreamSSEFrameStillWorks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": keepalive\n\n" + sseBlock("data: [DONE]")))
	}))
	defer server.Close()
	sink := &recordingSink{}
	if err := NewGemini(server.URL, "k", server.Client()).ChatStream(context.Background(), ChatRequest{Model: "gemini-2"}, sink); err != nil {
		t.Fatalf("含 data 行的流不应报错: %v", err)
	}
	if sink.done != "stop" {
		t.Fatalf("finishReason = %q, want stop", sink.done)
	}
}
