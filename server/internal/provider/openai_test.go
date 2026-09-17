package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recordingSink struct {
	deltas []string
	calls  []ToolCall
	done   string
}

func (s *recordingSink) Delta(text string) error {
	s.deltas = append(s.deltas, text)
	return nil
}

func (s *recordingSink) ToolCall(call ToolCall) error {
	s.calls = append(s.calls, call)
	return nil
}

func (s *recordingSink) Done(finishReason string) { s.done = finishReason }

// sseBlock 把若干 SSE 行拼成一个以空行结尾的事件块。
func sseBlock(lines ...string) string {
	return strings.Join(lines, "\n") + "\n\n"
}

func runChatStream(t *testing.T, body string) (*recordingSink, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	sink := &recordingSink{}
	err := NewOpenAI(server.URL, "test-key", server.Client()).ChatStream(context.Background(), ChatRequest{Model: "gpt-5"}, sink)
	return sink, err
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestChatStreamDispatchShapes 覆盖三种上游形状：只发 event 名、只发载荷 type、两者都有。
func TestChatStreamDispatchShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "只发 event 名",
			body: sseBlock("event: response.output_text.delta", `data: {"delta":"你好"}`) +
				sseBlock("event: response.output_text.delta", `data: {"delta":"世界"}`) +
				sseBlock("event: response.completed", `data: {"response":{"output":[]}}`),
			want: []string{"你好", "世界"},
		},
		{
			name: "只发载荷 type",
			body: sseBlock(`data: {"type":"response.output_text.delta","delta":"你好"}`) +
				sseBlock(`data: {"type":"response.output_text.delta","delta":"世界"}`) +
				sseBlock(`data: {"type":"response.completed","response":{"output":[]}}`) +
				sseBlock("data: [DONE]"),
			want: []string{"你好", "世界"},
		},
		{
			name: "event 名与 type 同时存在时不重复分发",
			body: sseBlock("event: response.output_text.delta", `data: {"type":"response.output_text.delta","delta":"你好"}`) +
				sseBlock("event: response.output_text.delta", `data: {"type":"response.output_text.delta","delta":"世界"}`),
			want: []string{"你好", "世界"},
		},
		{
			name: "两者都取不到的数据行被忽略",
			body: sseBlock(`data: {"foo":"bar"}`),
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink, err := runChatStream(t, tc.body)
			if err != nil {
				t.Fatalf("ChatStream 返回错误: %v", err)
			}
			if !equalStrings(sink.deltas, tc.want) {
				t.Fatalf("delta 序列不符: got %v want %v", sink.deltas, tc.want)
			}
			if sink.done != "stop" {
				t.Fatalf("finishReason = %q, want stop", sink.done)
			}
		})
	}
}

func TestChatStreamDoneBackfillsMissingText(t *testing.T) {
	t.Run("没有 delta 时补发全文", func(t *testing.T) {
		sink, err := runChatStream(t, sseBlock("event: response.output_text.done", `data: {"item_id":"msg_1","output_index":0,"content_index":0,"text":"完整回答"}`))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if !equalStrings(sink.deltas, []string{"完整回答"}) {
			t.Fatalf("应补发 done 全文: got %v", sink.deltas)
		}
	})

	t.Run("同一输出已发过 delta 时不补发", func(t *testing.T) {
		sink, err := runChatStream(t, sseBlock("event: response.output_text.delta", `data: {"item_id":"msg_1","delta":"你好"}`)+
			sseBlock("event: response.output_text.done", `data: {"item_id":"msg_1","text":"你好，世界"}`))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if !equalStrings(sink.deltas, []string{"你好"}) {
			t.Fatalf("done 不应重复发送: got %v", sink.deltas)
		}
	})

	t.Run("跨分发形状按输出下标匹配", func(t *testing.T) {
		// delta 走 event 名，done 走载荷 type，两者必须归到同一个输出。
		sink, err := runChatStream(t, sseBlock("event: response.output_text.delta", `data: {"output_index":0,"content_index":0,"delta":"你好"}`)+
			sseBlock(`data: {"type":"response.output_text.done","output_index":0,"content_index":0,"text":"你好，世界"}`))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if !equalStrings(sink.deltas, []string{"你好"}) {
			t.Fatalf("done 不应重复发送: got %v", sink.deltas)
		}
	})

	t.Run("重复投递的 done 只补发一次", func(t *testing.T) {
		block := sseBlock("event: response.output_text.done", `data: {"output_index":0,"content_index":0,"text":"全文"}`)
		sink, err := runChatStream(t, block+block)
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if !equalStrings(sink.deltas, []string{"全文"}) {
			t.Fatalf("重复 done 只应补发一次: got %v", sink.deltas)
		}
	})

	t.Run("整条流只补发一次", func(t *testing.T) {
		// 与原版一致：done 兜底判定的是「整条流是否已产出过文本」，不按输出粒度。
		// 按输出粒度判断时，delta 带 item_id、done 不带这类上游会把同一段文本发两遍。
		sink, err := runChatStream(t, sseBlock("event: response.output_text.done", `data: {"output_index":0,"content_index":0,"text":"第一段"}`)+
			sseBlock("event: response.output_text.done", `data: {"output_index":1,"content_index":0,"text":"第二段"}`))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if !equalStrings(sink.deltas, []string{"第一段"}) {
			t.Fatalf("只应补发第一段: got %v", sink.deltas)
		}
	})
}

func TestChatStreamFailedAndIncompleteEvents(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantContain string
	}{
		{
			name:        "response.failed 没有 error 对象",
			body:        sseBlock("event: response.failed", `data: {"response":{"status":"failed"}}`),
			wantContain: "上游响应失败",
		},
		{
			name:        "response.incomplete 带出未完成原因",
			body:        sseBlock(`data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`),
			wantContain: "max_output_tokens",
		},
		{
			name:        "内层 error 消息优先",
			body:        sseBlock("event: response.failed", `data: {"response":{"error":{"message":"quota exceeded"}}}`),
			wantContain: "quota exceeded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink, err := runChatStream(t, tc.body)
			var upstream *ErrUpstream
			if !errors.As(err, &upstream) {
				t.Fatalf("应返回 ErrUpstream, got %v", err)
			}
			if upstream.Status != http.StatusBadGateway {
				t.Fatalf("状态码 = %d, want 502", upstream.Status)
			}
			if !strings.Contains(upstream.Body, tc.wantContain) {
				t.Fatalf("错误信息 %q 应包含 %q", upstream.Body, tc.wantContain)
			}
			if len(sink.deltas) != 0 || sink.done != "" {
				t.Fatalf("失败事件不应产出内容: deltas=%v done=%q", sink.deltas, sink.done)
			}
		})
	}
}

func TestChatStreamCollectsToolCalls(t *testing.T) {
	t.Run("只发 event 名", func(t *testing.T) {
		sink, err := runChatStream(t, sseBlock("event: response.output_text.delta", `data: {"delta":"调用工具"}`)+
			sseBlock("event: response.completed", `data: {"response":{"output":[{"type":"function_call","call_id":"call_1","name":"create_node","arguments":"{\"x\":1}"}]}}`))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if !equalStrings(sink.deltas, []string{"调用工具"}) {
			t.Fatalf("delta 不符: %v", sink.deltas)
		}
		if len(sink.calls) != 1 || sink.calls[0].ID != "call_1" || sink.calls[0].Name != "create_node" || sink.calls[0].Arguments != `{"x":1}` {
			t.Fatalf("工具调用解析错误: %+v", sink.calls)
		}
		if sink.done != "tool_calls" {
			t.Fatalf("finishReason = %q, want tool_calls", sink.done)
		}
	})

	t.Run("只发载荷 type", func(t *testing.T) {
		sink, err := runChatStream(t, sseBlock(`data: {"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"call_2","name":"export_node","arguments":"{}"}]}}`))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if len(sink.calls) != 1 || sink.calls[0].Name != "export_node" {
			t.Fatalf("工具调用解析错误: %+v", sink.calls)
		}
		if sink.done != "tool_calls" {
			t.Fatalf("finishReason = %q, want tool_calls", sink.done)
		}
	})
}
