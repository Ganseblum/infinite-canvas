package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ArkProvider 承接火山方舟（Seedance）的视频任务创建与查询。
// 前端原来的「找最接近的比例」模糊匹配不复存在：选项由 constraints 驱动，
// 这里的换算是厂商格式改写，不是纠正用户输入。
type ArkProvider struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// NewArk 构造 Ark provider。client 为 nil 时用无整体超时的 Client，超时由请求 ctx 控制。
func NewArk(baseURL, apiKey string, client *http.Client) *ArkProvider {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	return &ArkProvider{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Client: client}
}

// doJSON 统一发 JSON 请求：响应体截断到 64MB 防异常上游撑爆内存，非 2xx 归一成 ErrUpstream。
func (p *ArkProvider) doJSON(ctx context.Context, method, url string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, classifyUpstreamDoErr(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ErrUpstream{Status: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

// Images Ark 不用于图像能力，保留接口以显式拒绝。
func (p *ArkProvider) Images(context.Context, ImageRequest) (ImageResult, error) {
	return ImageResult{}, ErrCapabilityUnsupported
}

// Speech Ark 不支持语音合成。
func (p *ArkProvider) Speech(context.Context, SpeechRequest) (SpeechResult, error) {
	return SpeechResult{}, ErrCapabilityUnsupported
}

// CreateVideo 创建任务并解析上游任务 id。
func (p *ArkProvider) CreateVideo(ctx context.Context, req VideoRequest) (VideoTask, error) {
	content := []map[string]any{{"type": "text", "text": req.Prompt}}
	if req.Mode == "frames" {
		if len(req.References) > 0 {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(req.References[0])}, "role": "first_frame"})
		}
		if len(req.References) > 1 {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(req.References[1])}, "role": "last_frame"})
		}
	} else {
		for _, ref := range req.References {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(ref)}})
		}
	}
	for _, ref := range req.VideoReferences {
		content = append(content, map[string]any{"type": "video_url", "video_url": map[string]any{"url": dataURL(ref)}})
	}
	for _, ref := range req.AudioReferences {
		content = append(content, map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": dataURL(ref)}})
	}
	payload := map[string]any{
		"model":    req.Model,
		"content":  content,
		"ratio":    defaultString(req.Ratio, "16:9"),
		"duration": maxInt(req.Duration, 8),
	}
	if req.Resolution != "" {
		payload["resolution"] = req.Resolution
	}
	if req.Watermark {
		payload["watermark"] = true
	}
	data, err := p.doJSON(ctx, http.MethodPost, p.BaseURL+"/contents/generations/tasks", payload)
	if err != nil {
		return VideoTask{}, err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.ID == "" {
		// 2xx 但认不出任务句柄，记 502 以触发渠道切换。
		return VideoTask{}, &ErrUpstream{Status: http.StatusBadGateway, Body: "上游未返回视频任务 id"}
	}
	return VideoTask{Provider: "ark", UpstreamTaskID: created.ID, PollAfterMs: 5000}, nil
}

// PollVideo 查询任务状态；终态时返回视频 URL。
func (p *ArkProvider) PollVideo(ctx context.Context, task VideoTask) (VideoState, error) {
	data, err := p.doJSON(ctx, http.MethodGet, p.BaseURL+"/contents/generations/tasks/"+task.UpstreamTaskID, nil)
	if err != nil {
		return VideoState{}, err
	}
	var payload struct {
		Status  string `json:"status"`
		Content *struct {
			VideoURL string `json:"video_url"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return VideoState{}, &ErrUpstream{Status: http.StatusBadGateway, Body: "解析 Ark 任务失败: " + err.Error()}
	}
	switch payload.Status {
	case "succeeded", "success":
		if payload.Content == nil || payload.Content.VideoURL == "" {
			return VideoState{Status: "failed", Error: "上游未返回视频地址"}, nil
		}
		return VideoState{Status: "succeeded", Video: &GeneratedImage{URL: payload.Content.VideoURL, MimeType: "video/mp4"}}, nil
	case "failed", "cancelled", "canceled", "expired":
		message := "视频生成失败"
		if payload.Error != nil && payload.Error.Message != "" {
			message = payload.Error.Message
		}
		return VideoState{Status: "failed", Error: message}, nil
	default:
		return VideoState{Status: "pending"}, nil
	}
}

// ChatStream Ark 复用 OpenAI 兼容的对话接口。
func (p *ArkProvider) ChatStream(ctx context.Context, req ChatRequest, sink StreamSink) error {
	fallback := NewOpenAI(p.BaseURL, p.APIKey, p.Client)
	return fallback.ChatStream(ctx, req, sink)
}

// dataURL 把内联素材编码成 data: URI，供 content 数组的 image_url / video_url 字段携带。
func dataURL(media InlineMedia) string {
	if media.MimeType == "" {
		media.MimeType = "application/octet-stream"
	}
	return "data:" + media.MimeType + ";base64," + base64.StdEncoding.EncodeToString(media.Data)
}
