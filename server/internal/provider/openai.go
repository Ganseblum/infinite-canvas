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
	if len(req.References) == 0 && req.Mask == nil {
		payload := map[string]any{"model": req.Model, "prompt": req.Prompt, "n": req.N}
		if req.Size != "" {
			payload["size"] = req.Size
		}
		if req.Quality != "" {
			payload["quality"] = req.Quality
		}
		if req.Background != "" {
			payload["background"] = req.Background
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
	if req.Size != "" {
		_ = writer.WriteField("size", req.Size)
	}
	if req.Quality != "" {
		_ = writer.WriteField("quality", req.Quality)
	}
	if req.Background != "" {
		_ = writer.WriteField("background", req.Background)
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

type openAIImageResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func parseOpenAIImages(raw []byte) (ImageResult, error) {
	var payload openAIImageResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ImageResult{}, fmt.Errorf("解析图像响应失败: %w", err)
	}
	if payload.Error != nil && payload.Error.Message != "" {
		return ImageResult{}, &ErrUpstream{Status: http.StatusBadGateway, Body: payload.Error.Message}
	}
	result := ImageResult{}
	for _, item := range payload.Data {
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

// Speech 语音合成，上游直接返回二进制流。
func (p *OpenAIProvider) Speech(ctx context.Context, req SpeechRequest) (SpeechResult, error) {
	payload := map[string]any{
		"model":           req.Model,
		"input":           req.Input,
		"response_format": req.Format,
	}
	if req.Voice != "" {
		payload["voice"] = req.Voice
	}
	if req.Speed > 0 {
		payload["speed"] = req.Speed
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
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", req.Model)
	_ = writer.WriteField("prompt", req.Prompt)
	_ = writer.WriteField("seconds", fmt.Sprint(req.Duration))
	if req.Ratio != "" || req.Resolution != "" {
		_ = writer.WriteField("size", videoSize(req.Ratio, req.Resolution))
	}
	_ = writer.WriteField("resolution_name", req.Resolution)
	_ = writer.WriteField("generate_audio", fmt.Sprint(req.GenerateAudio))
	_ = writer.WriteField("watermark", fmt.Sprint(req.Watermark))
	mode := req.Mode
	if mode == "" {
		mode = "reference"
	}
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
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.ID == "" {
		return VideoTask{}, fmt.Errorf("上游未返回视频任务 id")
	}
	return VideoTask{Provider: "openai", UpstreamTaskID: created.ID, PollAfterMs: 5000}, nil
}

// PollVideo 查询一次任务状态；上游返回结果 URL 时下载交给 handler。
func (p *OpenAIProvider) PollVideo(ctx context.Context, task VideoTask) (VideoState, error) {
	data, err := p.doJSON(ctx, http.MethodGet, "/videos/"+task.UpstreamTaskID, nil)
	if err != nil {
		return VideoState{}, err
	}
	var payload struct {
		Status string `json:"status"`
		URL    string `json:"url"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return VideoState{}, fmt.Errorf("解析视频任务失败: %w", err)
	}
	if payload.URL != "" {
		return VideoState{Status: "succeeded", Video: &GeneratedImage{URL: payload.URL, MimeType: "video/mp4"}}, nil
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
		switch event {
		case "response.output_text.delta":
			if delta, ok := payloadEvent["delta"].(string); ok && delta != "" {
				if err := sink.Delta(delta); err != nil {
					return err
				}
			}
		case "response.output_text.done":
			// 全文事件在流式场景下已由 delta 覆盖，无需重复发送。
		case "response.completed":
			if response, ok := payloadEvent["response"].(map[string]any); ok {
				if calls := collectResponseToolCalls(response); len(calls) > 0 {
					pendingCalls = calls
					finishReason = "tool_calls"
				}
			}
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
		if message.Role == "system" {
			continue
		}
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
			input = append(input, map[string]any{"role": "user", "content": toResponsesContent(message.Content)})
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
	case "mp3", "mpeg":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "opus":
		return "audio/ogg"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	default:
		return "audio/mpeg"
	}
}

func videoSize(ratio, resolution string) string {
	width, height := 1280, 720
	switch ratio {
	case "9:16":
		width, height = 720, 1280
	case "1:1":
		width, height = 960, 960
	case "4:3":
		width, height = 1024, 768
	case "3:4":
		width, height = 768, 1024
	case "21:9":
		width, height = 1680, 720
	}
	switch resolution {
	case "480p":
		return fmt.Sprintf("%dx%d", width/2, height/2)
	case "1080p":
		return fmt.Sprintf("%dx%d", width*3/2, height*3/2)
	default:
		return fmt.Sprintf("%dx%d", width, height)
	}
}
