package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// OpenAIProvider 覆盖 OpenAI 兼容格式：图像、语音、视频与 Responses 流式对话。
// 模型调用方式由平台保证，不存在用户自定义脚本。
type OpenAIProvider struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func NewOpenAI(baseURL, apiKey string, client *http.Client) *OpenAIProvider {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	return &OpenAIProvider{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Client: client}
}

func (p *OpenAIProvider) endpoint(path string) string {
	base := p.BaseURL
	if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v1beta") {
		return base + path
	}
	return base + "/v1" + path
}

func (p *OpenAIProvider) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	return req, nil
}

func (p *OpenAIProvider) doJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := p.newRequest(ctx, method, p.endpoint(path), body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
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

// Images 文生图走 /images/generations，带参考图或蒙版走 /images/edits。
func (p *OpenAIProvider) Images(ctx context.Context, req ImageRequest) (ImageResult, error) {
	size, err := resolveRequestSize(req.Quality, req.Size)
	if err != nil {
		return ImageResult{}, err
	}
	quality := normalizeQuality(req.Quality)
	background := normalizeBackground(req.Background)
	if len(req.References) == 0 && req.Mask == nil {
		payload := map[string]any{
			"model": req.Model, "prompt": req.Prompt, "n": req.N,
			"output_format": imageOutputFormat,
		}
		if size != "" {
			payload["size"] = size
		}
		if quality != "" {
			payload["quality"] = quality
		}
		if background != "" {
			payload["background"] = background
		}
		// gpt-image 系列不接受 response_format，dall-e 系列仍需要 b64_json。
		if !strings.Contains(req.Model, "gpt-image") {
			payload["response_format"] = "b64_json"
		}
		data, err := p.doJSON(ctx, http.MethodPost, "/images/generations", payload)
		if err != nil {
			return ImageResult{}, err
		}
		return parseOpenAIImages(data)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", req.Model)
	_ = writer.WriteField("prompt", req.Prompt)
	_ = writer.WriteField("n", fmt.Sprint(req.N))
	_ = writer.WriteField("output_format", imageOutputFormat)
	if size != "" {
		_ = writer.WriteField("size", size)
	}
	if quality != "" {
		_ = writer.WriteField("quality", quality)
	}
	if background != "" {
		_ = writer.WriteField("background", background)
	}
	if !strings.Contains(req.Model, "gpt-image") {
		_ = writer.WriteField("response_format", "b64_json")
	}
	// 多张参考图按 OpenAI 规范使用重复的 image[] 字段，单张继续用 image。
	field := "image"
	if len(req.References) > 1 {
		field = "image[]"
	}
	for index, ref := range req.References {
		if err := writeFilePart(writer, field, fmt.Sprintf("ref-%d", index), ref); err != nil {
			return ImageResult{}, err
		}
	}
	if req.Mask != nil {
		if err := writeFilePart(writer, "mask", "mask", *req.Mask); err != nil {
			return ImageResult{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return ImageResult{}, err
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.endpoint("/images/edits"), body)
	if err != nil {
		return ImageResult{}, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return ImageResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return ImageResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ImageResult{}, &ErrUpstream{Status: resp.StatusCode, Body: string(data)}
	}
	return parseOpenAIImages(data)
}

func writeFilePart(writer *multipart.Writer, field, name string, media InlineMedia) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, name))
	contentType := media.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(media.Data)
	return err
}

// errorsNoImage 表示上游返回成功但没有可用产物。
var errorsNoImage = errors.New("上游未返回图片")

type openAIImageItem struct {
	B64JSON string `json:"b64_json"`
	URL     string `json:"url"`
}

// openAIImageResponse 兼容原版支持的三种数组字段（data / images / results）与 {code,msg} 信封。
// code 用 RawMessage 接收：它可能是数字也可能是字符串，只有数字且非 0 才视为失败。
type openAIImageResponse struct {
	Data    []openAIImageItem `json:"data"`
	Images  []openAIImageItem `json:"images"`
	Results []openAIImageItem `json:"results"`
	Code    json.RawMessage   `json:"code"`
	Msg     string            `json:"msg"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func parseOpenAIImages(raw []byte) (ImageResult, error) {
	var payload openAIImageResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ImageResult{}, fmt.Errorf("解析图像响应失败: %w", err)
	}
	if code, ok := numericCode(payload.Code); ok && code != 0 {
		message := payload.Msg
		if message == "" {
			message = "上游返回失败"
		}
		return ImageResult{}, &ErrUpstream{Status: http.StatusBadGateway, Body: message}
	}
	if payload.Error != nil && payload.Error.Message != "" {
		return ImageResult{}, &ErrUpstream{Status: http.StatusBadGateway, Body: payload.Error.Message}
	}
	items := payload.Data
	if len(items) == 0 {
		items = payload.Images
	}
	if len(items) == 0 {
		items = payload.Results
	}
	result := ImageResult{}
	for _, item := range items {
		if item.B64JSON != "" {
			decoded, err := base64.StdEncoding.DecodeString(item.B64JSON)
			if err != nil {
				return ImageResult{}, fmt.Errorf("解码 base64 图像失败: %w", err)
			}
			result.Images = append(result.Images, GeneratedImage{Data: decoded, MimeType: "image/png"})
			continue
		}
		if item.URL != "" {
			result.Images = append(result.Images, GeneratedImage{URL: item.URL})
		}
	}
	if len(result.Images) == 0 {
		return ImageResult{}, errorsNoImage
	}
	return result, nil
}

// numericCode 只在 code 是数字时返回，字符串形式的 code 按「不是信封」处理。
func numericCode(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

// Speech 语音合成，上游直接返回二进制流。
func (p *OpenAIProvider) Speech(ctx context.Context, req SpeechRequest) (SpeechResult, error) {
	// voice 与 response_format 恒发（白名单外回落 alloy / mp3），speed 裁剪到 0.25..4。
	payload := map[string]any{
		"model":           req.Model,
		"input":           req.Input,
		"voice":           normalizeSpeechVoice(req.Voice),
		"response_format": normalizeSpeechFormat(req.Format),
		"speed":           normalizeSpeechSpeed(req.Speed),
	}
	if req.Instructions != "" {
		payload["instructions"] = req.Instructions
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return SpeechResult{}, err
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.endpoint("/audio/speech"), bytes.NewReader(raw))
	if err != nil {
		return SpeechResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return SpeechResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return SpeechResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SpeechResult{}, &ErrUpstream{Status: resp.StatusCode, Body: string(data)}
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" || strings.Contains(mimeType, "json") {
		mimeType = audioMimeType(req.Format)
	}
	return SpeechResult{Data: data, MimeType: mimeType}, nil
}

// CreateVideo 创建视频任务，只拿到上游任务 id，不等生成完成。
func (p *OpenAIProvider) CreateVideo(ctx context.Context, req VideoRequest) (VideoTask, error) {
	mode := resolveVideoMode(req.Mode, len(req.References))
	// size 与 resolution_name 在原版里恒有值（分别兜底 1280x720 与 720p），不能省略。
	size := normalizeVideoSize(req.Ratio, req.Resolution)
	if size == "" {
		size = "1280x720"
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", req.Model)
	_ = writer.WriteField("prompt", req.Prompt)
	_ = writer.WriteField("seconds", normalizeVideoSeconds(fmt.Sprint(req.Duration)))
	_ = writer.WriteField("size", size)
	_ = writer.WriteField("resolution_name", normalizeVideoResolution(req.Resolution))
	_ = writer.WriteField("generate_audio", fmt.Sprint(req.GenerateAudio))
	_ = writer.WriteField("watermark", fmt.Sprint(req.Watermark))
	_ = writer.WriteField("mode", mode)
	if mode == "frames" {
		if len(req.References) > 0 {
			_ = writeFilePart(writer, "first_frame", "first.png", req.References[0])
		}
		if len(req.References) > 1 {
			_ = writeFilePart(writer, "last_frame", "last.png", req.References[1])
		}
	} else {
		for index, ref := range req.References {
			if err := writeFilePart(writer, "image[]", fmt.Sprintf("ref-%d", index), ref); err != nil {
				return VideoTask{}, err
			}
		}
	}
	for index, ref := range req.VideoReferences {
		if err := writeFilePart(writer, "video[]", fmt.Sprintf("video-%d", index), ref); err != nil {
			return VideoTask{}, err
		}
	}
	for index, ref := range req.AudioReferences {
		if err := writeFilePart(writer, "audio[]", fmt.Sprintf("audio-%d", index), ref); err != nil {
			return VideoTask{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return VideoTask{}, err
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.endpoint("/videos"), body)
	if err != nil {
		return VideoTask{}, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return VideoTask{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return VideoTask{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return VideoTask{}, &ErrUpstream{Status: resp.StatusCode, Body: string(data)}
	}
	var created openAIVideoPayload
	if err := json.Unmarshal(unwrapJSONEnvelope(data), &created); err != nil || created.ID == "" {
		return VideoTask{}, fmt.Errorf("上游未返回视频任务 id")
	}
	return VideoTask{Provider: "openai", UpstreamTaskID: created.ID, PollAfterMs: 5000}, nil
}

// openAIVideoPayload 视频任务的状态与结果。结果 URL 的字段名各家不同，
// 原版按 video_url → result_url → url → content.video_url → content.url 依次兜底。
type openAIVideoPayload struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	VideoURL  string `json:"video_url"`
	ResultURL string `json:"result_url"`
	Content   *struct {
		VideoURL string `json:"video_url"`
		URL      string `json:"url"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (v openAIVideoPayload) resultURL() string {
	candidates := []string{v.VideoURL, v.ResultURL, v.URL}
	if v.Content != nil {
		candidates = append(candidates, v.Content.VideoURL, v.Content.URL)
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// unwrapJSONEnvelope 拆掉 {code,data} 信封：仅当 code 为 0 且 data 是对象时才拆。
func unwrapJSONEnvelope(raw []byte) []byte {
	var envelope struct {
		Code json.RawMessage `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return raw
	}
	code, ok := numericCode(envelope.Code)
	if !ok || code != 0 || len(envelope.Data) == 0 {
		return raw
	}
	if trimmed := bytes.TrimSpace(envelope.Data); len(trimmed) == 0 || trimmed[0] != '{' {
		return raw
	}
	return envelope.Data
}

// PollVideo 查询一次任务状态；上游返回结果 URL 时下载交给 handler。
func (p *OpenAIProvider) PollVideo(ctx context.Context, task VideoTask) (VideoState, error) {
	data, err := p.doJSON(ctx, http.MethodGet, "/videos/"+task.UpstreamTaskID, nil)
	if err != nil {
		return VideoState{}, err
	}
	var payload openAIVideoPayload
	if err := json.Unmarshal(unwrapJSONEnvelope(data), &payload); err != nil {
		return VideoState{}, fmt.Errorf("解析视频任务失败: %w", err)
	}
	if url := payload.resultURL(); url != "" {
		return VideoState{Status: "succeeded", Video: &GeneratedImage{URL: url, MimeType: "video/mp4"}}, nil
	}
	switch payload.Status {
	case "completed", "succeeded":
		return p.downloadVideoContent(ctx, task)
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

// downloadVideoContent 上游只给状态时走 /content 拿二进制。
func (p *OpenAIProvider) downloadVideoContent(ctx context.Context, task VideoTask) (VideoState, error) {
	req, err := p.newRequest(ctx, http.MethodGet, p.endpoint("/videos/"+task.UpstreamTaskID+"/content"), nil)
	if err != nil {
		return VideoState{}, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return VideoState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return VideoState{}, &ErrUpstream{Status: resp.StatusCode, Body: string(body)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return VideoState{}, err
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return VideoState{Status: "succeeded", Video: &GeneratedImage{Data: data, MimeType: mimeType}}, nil
}

// ChatStream 走 Responses 接口并把 SSE 翻译成统一回调。
func (p *OpenAIProvider) ChatStream(ctx context.Context, req ChatRequest, sink StreamSink) error {
	input, tools := toResponsesInput(req)
	payload := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": true,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		if req.ToolChoice != nil {
			payload["tool_choice"] = req.ToolChoice
		}
	}
	if req.ReasoningEffort != "" && req.ReasoningEffort != "auto" {
		payload["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.endpoint("/responses"), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return &ErrUpstream{Status: resp.StatusCode, Body: string(body)}
	}

	reader := newSSEReader(resp.Body)
	finishReason := "stop"
	var pendingCalls []ToolCall
	text := newStreamTextState()
	for {
		event, data, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if data == "" || data == "[DONE]" {
			continue
		}
		var payloadEvent map[string]any
		if err := json.Unmarshal([]byte(data), &payloadEvent); err != nil {
			continue
		}
		if message := responseErrorMessage(payloadEvent); message != "" {
			return &ErrUpstream{Status: http.StatusBadGateway, Body: message}
		}
		// 与原版一致：先看载荷里的 type；网关只发 event 名（载荷无 type）时再回退。
		// type 优先能覆盖「event 名是通用 message、语义在载荷里」这种形状，
		// 反过来则会把 delta 分错类并静默丢弃。
		kind, _ := payloadEvent["type"].(string)
		if kind == "" {
			kind = event
		}
		if kind == "" {
			continue
		}
		switch kind {
		case "response.output_text.delta":
			if delta, ok := payloadEvent["delta"].(string); ok && delta != "" {
				text.produced = true
				if err := sink.Delta(delta); err != nil {
					return err
				}
			}
		case "response.output_text.done":
			// 与原版一致：整条流只要已经产出过文本就不再补发，避免重复。原版用全局标志
			// 而不是按输出粒度判断，粒度差异会让「delta 带 item_id、done 不带」这类
			// 上游把同一段文本发两遍。
			if full, ok := payloadEvent["text"].(string); ok && full != "" && !text.produced {
				text.produced = true
				if err := sink.Delta(full); err != nil {
					return err
				}
			}
		case "response.completed":
			if response, ok := payloadEvent["response"].(map[string]any); ok {
				if calls := collectResponseToolCalls(response); len(calls) > 0 {
					pendingCalls = calls
					finishReason = "tool_calls"
				}
			}
		case "response.failed", "response.incomplete":
			return &ErrUpstream{Status: http.StatusBadGateway, Body: responseFailureMessage(kind, payloadEvent)}
		default:
			if calls := collectResponseToolCalls(payloadEvent); len(calls) > 0 {
				pendingCalls = calls
				finishReason = "tool_calls"
			}
		}
	}
	for _, call := range pendingCalls {
		if err := sink.ToolCall(call); err != nil {
			return err
		}
	}
	sink.Done(finishReason)
	return nil
}

func toResponsesInput(req ChatRequest) ([]map[string]any, []map[string]any) {
	input := make([]map[string]any, 0, len(req.Messages))
	for _, message := range req.Messages {
		switch message.Role {
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": message.ToolCallID,
				"output":  message.Content,
			})
		case "assistant":
			input = append(input, map[string]any{"role": "assistant", "content": toResponsesContent(message.Content)})
		default:
			// system 消息要原样保留 role，不能改写成 user，否则 systemPrompt 会变成普通用户输入。
			role := message.Role
			if role == "" {
				role = "user"
			}
			input = append(input, map[string]any{"role": role, "content": toResponsesContent(message.Content)})
		}
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	return input, tools
}

func toResponsesContent(content any) any {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch record["type"] {
			case "text":
				parts = append(parts, map[string]any{"type": "input_text", "text": record["text"]})
			case "image_url":
				urlRecord, _ := record["image_url"].(map[string]any)
				url, _ := urlRecord["url"].(string)
				parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
			}
		}
		return parts
	default:
		return fmt.Sprint(content)
	}
}

func collectResponseToolCalls(payload map[string]any) []ToolCall {
	output, ok := payload["output"].([]any)
	if !ok {
		return nil
	}
	var calls []ToolCall
	for _, item := range output {
		record, ok := item.(map[string]any)
		if !ok || record["type"] != "function_call" {
			continue
		}
		id, _ := record["call_id"].(string)
		if id == "" {
			id, _ = record["id"].(string)
		}
		name, _ := record["name"].(string)
		arguments, _ := record["arguments"].(string)
		if id == "" || name == "" {
			continue
		}
		calls = append(calls, ToolCall{ID: id, Name: name, Arguments: arguments})
	}
	return calls
}

func responseErrorMessage(payload map[string]any) string {
	if errorRecord, ok := payload["error"].(map[string]any); ok {
		if message, ok := errorRecord["message"].(string); ok && message != "" {
			return message
		}
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if errorRecord, ok := response["error"].(map[string]any); ok {
			if message, ok := errorRecord["message"].(string); ok && message != "" {
				return message
			}
		}
	}
	if message, ok := payload["msg"].(string); ok {
		return message
	}
	return ""
}

// responseFailureMessage 给 response.failed / response.incomplete 兜底出可读错误。
// 这两类事件没有 error 对象时会被当成普通事件忽略，请求以「空流成功」收场。
func responseFailureMessage(kind string, payload map[string]any) string {
	detail := ""
	if response, ok := payload["response"].(map[string]any); ok {
		if details, ok := response["incomplete_details"].(map[string]any); ok {
			detail, _ = details["reason"].(string)
		}
		if detail == "" {
			detail, _ = response["status"].(string)
		}
	}
	if detail == "" {
		detail = strings.TrimPrefix(kind, "response.")
	}
	return "上游响应失败: " + detail
}

// streamTextState 记录整条流是否已经产出过文本。
// 与原版一致：done 事件只在整条流还没产出过任何文本时才用它兜底，
// 不按输出粒度判断——粒度不一致的上游（delta 带 item_id、done 不带）
// 会把同一段文本发两遍给用户。
type streamTextState struct {
	produced bool
}

func newStreamTextState() *streamTextState {
	return &streamTextState{}
}

// sseReader 按行读取 SSE，不设行长上限（bufio.Scanner 默认 64KB 会静默截断）。
type sseReader struct {
	reader *bufioReader
	event  string
}

type bufioReader = stringsReader

// stringsReader 只是为了避免在 provider.go 里暴露 bufio 细节。
type stringsReader struct {
	source io.Reader
	buffer []byte
}

func newSSEReader(source io.Reader) *sseReader {
	return &sseReader{reader: &stringsReader{source: source}}
}

func (r *sseReader) Next() (string, string, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			return "", "", err
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			r.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			return r.event, data, nil
		case line == "":
			r.event = ""
		}
	}
}

func (r *sseReader) readLine() (string, error) {
	for {
		if index := bytes.IndexByte(r.reader.buffer, '\n'); index >= 0 {
			line := string(r.reader.buffer[:index])
			r.reader.buffer = r.reader.buffer[index+1:]
			return strings.TrimSuffix(line, "\r"), nil
		}
		chunk := make([]byte, 32*1024)
		n, err := r.reader.source.Read(chunk)
		if n > 0 {
			r.reader.buffer = append(r.reader.buffer, chunk[:n]...)
			continue
		}
		if err == io.EOF {
			if len(r.reader.buffer) > 0 {
				line := string(r.reader.buffer)
				r.reader.buffer = nil
				return line, nil
			}
			return "", io.EOF
		}
		if err != nil {
			return "", err
		}
	}
}

func audioMimeType(format string) string {
	switch strings.ToLower(format) {
	case "wav":
		return "audio/wav"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
