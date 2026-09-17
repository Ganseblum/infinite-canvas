// Package provider 把上游各家的协议差异收敛在服务端：handler 只认统一的请求与结果，
// 加一个新厂商不需要改任何 HTTP 代码。
package provider

import (
	"context"
	"errors"
	"strconv"
)

// ErrCapabilityUnsupported 表示该渠道不支持请求的能力，handler 统一映射成 400 MODEL_NOT_SUPPORTED。
var ErrCapabilityUnsupported = errors.New("该渠道不支持此能力")

// ErrUpstream 表示上游错误，供 handler 区分网络错误与上游错误并决定重试与切渠道。
// Status > 0 是上游真实 HTTP 状态码（非 2xx 用真实码；2xx 但响应形状无法解析的
// 按仓内既有约定归一为 502）；Status == 0 是连接层失败：Client.Do 报错且未收到任何
// 响应（连接拒绝、DNS 失败等）。请求被取消或超时（context.Canceled / DeadlineExceeded）
// 不进这个类型，由各 provider 原样上抛。
type ErrUpstream struct {
	Status int
	Body   string
}

func (e *ErrUpstream) Error() string {
	if e.Status == 0 {
		return "上游请求失败"
	}
	return "上游返回 " + strconv.Itoa(e.Status)
}

// classifyUpstreamDoErr 统一归类 Client.Do 的错误：取消与超时原样上抛（不得重试），
// 其余都是未收到任何响应的连接层失败，按约定打 ErrUpstream{Status: 0}。
func classifyUpstreamDoErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &ErrUpstream{Status: 0, Body: err.Error()}
}

// InlineMedia 是已经读入内存的参考素材。handler 从媒体存储读取，provider 只负责编码上传。
type InlineMedia struct {
	Data     []byte
	MimeType string
	Name     string
}

// ImageRequest 是统一的图像生成请求。references 非空即走图生图。
type ImageRequest struct {
	Model      string
	Prompt     string
	N          int
	Size       string // 像素尺寸（1024x1024）或比例（16:9），由 provider 各自换算
	Quality    string
	Background string
	References []InlineMedia
	Mask       *InlineMedia
}

type ImageResult struct {
	// Images 是上游返回的产物：base64 解码后的字节，或需要下载的 URL。
	Images []GeneratedImage
}

type GeneratedImage struct {
	Data     []byte
	MimeType string
	URL      string
}

// SpeechRequest 是统一的语音合成请求。
type SpeechRequest struct {
	Model        string
	Input        string
	Voice        string
	Format       string
	Speed        float64
	Instructions string
}

type SpeechResult struct {
	Data     []byte
	MimeType string
	URL      string
}

// VideoRequest 是统一的视频任务创建请求。ratio 与 resolution 用统一语义字段。
type VideoRequest struct {
	Model           string
	Prompt          string
	Duration        int
	Ratio           string
	Resolution      string
	GenerateAudio   bool
	Watermark       bool
	References      []InlineMedia
	VideoReferences []InlineMedia
	AudioReferences []InlineMedia
	Mode            string // frames | reference
}

// VideoTask 是上游任务句柄。PollAfterMs 由 provider 给出，前端不再硬编码轮询间隔。
type VideoTask struct {
	Provider       string
	UpstreamTaskID string
	PollAfterMs    int
}

// VideoState 是视频任务的一次查询结果。
type VideoState struct {
	Status string // pending | succeeded | failed
	Video  *GeneratedImage
	Error  string
}

// ChatMessage 沿用现有 AiTextMessage 的形状，content 可以是字符串或混排数组。
type ChatMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ChatTool 是函数工具声明，形状与 OpenAI 一致。
type ChatTool struct {
	Type     string       `json:"type"`
	Function ChatFunction `json:"function"`
}

type ChatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ToolCall 是统一的工具调用形状。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatRequest 是统一的对话请求。
type ChatRequest struct {
	Model           string
	Messages        []ChatMessage
	ReasoningEffort string
	Tools           []ChatTool
	ToolChoice      any
	Stream          bool
}

// ChatResult 是非流式对话的结果。
type ChatResult struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// StreamSink 由 handler 实现，provider 只负责把上游事件翻译成这三个回调。
type StreamSink interface {
	Delta(text string) error
	ToolCall(call ToolCall) error
	// Done 在一次流结束时调用，finishReason 取 stop、tool_calls 等。
	Done(finishReason string)
}

// Provider 是最小的能力接口，不关心 HTTP 细节。
type Provider interface {
	Images(ctx context.Context, req ImageRequest) (ImageResult, error)
	Speech(ctx context.Context, req SpeechRequest) (SpeechResult, error)
	CreateVideo(ctx context.Context, req VideoRequest) (VideoTask, error)
	PollVideo(ctx context.Context, task VideoTask) (VideoState, error)
	ChatStream(ctx context.Context, req ChatRequest, sink StreamSink) error
}
