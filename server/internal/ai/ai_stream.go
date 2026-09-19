package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/provider"
	"github.com/infinite-canvas/server/internal/service"
)

// chatStream 是文本对话的流式实现：统一的自有事件流，不是上游原始 SSE 的透传。
// 事件固定为 delta / tool_call / error / done 四种，厂商差异全部挡在 provider 里。
func (h *AIHandler) chatStream(c *gin.Context, user model.PlatformUser, request *model.AIRequest, reserved *service.ReserveResult, catalogItem model.ModelCatalog, req chatRequest) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		h.failRequest(c, request, reserved, errors.New("响应流不可用"), 0)
		return
	}
	stream := newSSEWriter(c, flusher)
	stream.headers()

	sink := &streamSink{handler: h, ctx: c.Request.Context(), writer: stream, started: time.Now()}
	callCtx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// 流式路径整个请求共用一个 sink：客户端已收到的字节无法撤回，
	// callChat 靠 sink.produced() 判定「已产出即终止」。
	sinkFor := func() provider.StreamSink { return sink }
	// 上游在独立 goroutine 里跑；本函数负责心跳、空闲超时与断开取消。
	done := make(chan error, 1)
	go func() {
		_, err := h.callChat(callCtx, catalogItem, provider.ChatRequest{
			Model:           catalogItem.Name,
			Messages:        req.Messages,
			ReasoningEffort: req.ReasoningEffort,
			Tools:           req.Tools,
			ToolChoice:      req.ToolChoice,
			Stream:          true,
		}, sinkFor)
		done <- err
	}()

	timeouts := h.upstream.Timeouts()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	idle := time.NewTimer(timeouts.StreamIdle)
	defer idle.Stop()

	var upstreamErr error
	timedOut := false
	for {
		select {
		case err := <-done:
			upstreamErr = err
			goto finished
		case <-c.Request.Context().Done():
			// 客户端断开：context 取消会同时关掉上游连接。
			upstreamErr = context.Canceled
			goto finished
		case <-idle.C:
			timedOut = true
			cancel()
			upstreamErr = context.DeadlineExceeded
			goto finished
		case <-heartbeat.C:
			if err := stream.comment("ping"); err != nil {
				upstreamErr = err
				goto finished
			}
		case <-sink.activity():
			// 有产出就把空闲计时往后推，整体超时仍由上层 context 控制。
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(timeouts.StreamIdle)
		}
	}

finished:
	durationMs := int(time.Since(sink.started).Milliseconds())
	if upstreamErr != nil && !sink.produced() {
		// 首个 delta 之前失败才算失败，全额退还。
		h.failRequest(c, request, reserved, upstreamErr, service.UpstreamStatus(upstreamErr))
		_ = stream.event("error", gin.H{
			"code":    errorCodeFor(upstreamErr, timedOut),
			"message": errorMessageFor(upstreamErr, timedOut),
		})
		return
	}
	// 已经产出过内容的，按成功处理，不退点。
	if err := h.requests.MarkSucceeded(nil, request.ID, durationMs); err != nil {
		slog.Error("收敛流式请求失败", "request", request.ID, "err", err)
	}
	remaining, _ := h.availableMicros(user.ID)
	_ = stream.event("done", gin.H{
		"credits":      h.creditsPayload(request, remaining),
		"finishReason": sink.finishReasonOr("stop"),
	})
}

func errorCodeFor(err error, timedOut bool) string {
	if timedOut || isTimeout(err) {
		return "UPSTREAM_TIMEOUT"
	}
	if status := service.UpstreamStatus(err); status > 0 {
		return "UPSTREAM_ERROR"
	}
	return "UPSTREAM_ERROR"
}

func errorMessageFor(err error, timedOut bool) string {
	if timedOut || isTimeout(err) {
		return "上游服务响应超时，请稍后重试"
	}
	if status := service.UpstreamStatus(err); status > 0 {
		return fmt.Sprintf("上游服务返回异常（HTTP %d）", status)
	}
	return "上游服务返回异常，请稍后重试"
}

// sseWriter 负责 SSE 的响应头、事件写入与 Flush。
type sseWriter struct {
	writer  gin.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(c *gin.Context, flusher http.Flusher) *sseWriter {
	return &sseWriter{writer: c.Writer, flusher: flusher}
}

func (w *sseWriter) headers() {
	w.writer.Header().Set("Content-Type", "text/event-stream")
	w.writer.Header().Set("Cache-Control", "no-cache")
	w.writer.Header().Set("Connection", "keep-alive")
	// X-Accel-Buffering 是给 nginx 看的，能在应用层就地关闭该响应的缓冲。
	w.writer.Header().Set("X-Accel-Buffering", "no")
	w.writer.WriteHeader(http.StatusOK)
	w.flusher.Flush()
}

func (w *sseWriter) event(name string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.writer, "event: %s\ndata: %s\n\n", name, raw); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (w *sseWriter) comment(text string) error {
	if _, err := fmt.Fprintf(w.writer, ": %s\n\n", text); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

// streamSink 把 provider 的回调写成 HTTP 事件，并在每个事件后唤醒心跳循环。
type streamSink struct {
	handler      *AIHandler
	ctx          context.Context
	writer       *sseWriter
	started      time.Time
	mu           sync.Mutex
	producedOnce bool
	finishReason string
	activityCh   chan struct{}
	activityInit sync.Once
}

func (s *streamSink) activity() <-chan struct{} {
	s.activityInit.Do(func() {
		s.activityCh = make(chan struct{}, 1)
	})
	return s.activityCh
}

func (s *streamSink) signal() {
	select {
	case s.activityCh <- struct{}{}:
	default:
	}
}

func (s *streamSink) Delta(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	s.producedOnce = true
	s.mu.Unlock()
	s.signal()
	return s.writer.event("delta", gin.H{"text": text})
}

func (s *streamSink) ToolCall(call provider.ToolCall) error {
	s.mu.Lock()
	s.producedOnce = true
	s.mu.Unlock()
	s.signal()
	return s.writer.event("tool_call", gin.H{"id": call.ID, "name": call.Name, "arguments": call.Arguments})
}

func (s *streamSink) Done(finishReason string) {
	s.mu.Lock()
	s.finishReason = finishReason
	s.mu.Unlock()
	s.signal()
}

func (s *streamSink) produced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.producedOnce
}

func (s *streamSink) finishReasonOr(fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finishReason == "" {
		return fallback
	}
	return s.finishReason
}

// sseNotSupportedError 用于在响应头已经写出后仍需提示错误的场景。
var sseNotSupportedError = errs.ErrInternal
