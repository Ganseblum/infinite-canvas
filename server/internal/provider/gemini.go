package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GeminiProvider 承接前端现有的 Gemini 适配：generateContent 生图、
// predictLongRunning 生视频、streamGenerateContent 流式对话。
type GeminiProvider struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func NewGemini(baseURL, apiKey string, client *http.Client) *GeminiProvider {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	return &GeminiProvider{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Client: client}
}

func (p *GeminiProvider) baseURL() string {
	lower := strings.ToLower(p.BaseURL)
	if strings.HasSuffix(lower, "/v1") || strings.HasSuffix(lower, "/v1beta") {
		return p.BaseURL
	}
	return p.BaseURL + "/v1beta"
}

func (p *GeminiProvider) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", p.APIKey)
	return req, nil
}

func (p *GeminiProvider) doJSON(ctx context.Context, method, url string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := p.newRequest(ctx, method, url, body)
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

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunction         `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunction struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPayload struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// Images 走 generateContent，请求体与前端现有实现一致。
func (p *GeminiProvider) Images(ctx context.Context, req ImageRequest) (ImageResult, error) {
	parts := []geminiPart{{Text: req.Prompt}}
	for _, ref := range req.References {
		parts = append(parts, geminiMediaPart(ref))
	}
	payload := map[string]any{
		"contents": []geminiContent{{Role: "user", Parts: parts}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
			"imageConfig":        geminiImageConfig(req),
		},
	}
	result := ImageResult{}
	for i := 0; i < maxInt(req.N, 1); i++ {
		data, err := p.doJSON(ctx, http.MethodPost, p.modelURL(req.Model, "generateContent"), payload)
		if err != nil {
			return ImageResult{}, err
		}
		var parsed geminiPayload
		if err := json.Unmarshal(data, &parsed); err != nil {
			return ImageResult{}, fmt.Errorf("解析 Gemini 响应失败: %w", err)
		}
		if message := geminiErrorMessage(parsed); message != "" {
			return ImageResult{}, &ErrUpstream{Status: http.StatusBadGateway, Body: message}
		}
		found := false
		for _, candidate := range parsed.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.InlineData == nil || part.InlineData.Data == "" {
					continue
				}
				decoded, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					return ImageResult{}, fmt.Errorf("解码 Gemini 图像失败: %w", err)
				}
				mimeType := part.InlineData.MimeType
				if mimeType == "" {
					mimeType = "image/png"
				}
				result.Images = append(result.Images, GeneratedImage{Data: decoded, MimeType: mimeType})
				found = true
			}
		}
		if !found {
			return ImageResult{}, errorsNoImage
		}
	}
	return result, nil
}

func geminiImageConfig(req ImageRequest) map[string]any {
	config := map[string]any{}
	if req.Size != "" {
		if strings.Contains(req.Size, ":") {
			config["aspectRatio"] = req.Size
		} else {
			config["imageSize"] = strings.ToUpper(req.Size)
		}
	}
	switch req.Quality {
	case "low":
		config["imageSize"] = "1K"
	case "high":
		config["imageSize"] = "2K"
	}
	return config
}

func geminiMediaPart(media InlineMedia) geminiPart {
	return geminiPart{InlineData: &geminiInlineData{
		MimeType: media.MimeType,
		Data:     base64.StdEncoding.EncodeToString(media.Data),
	}}
}

// Speech Gemini 不支持语音合成。
func (p *GeminiProvider) Speech(context.Context, SpeechRequest) (SpeechResult, error) {
	return SpeechResult{}, ErrCapabilityUnsupported
}

// CreateVideo 走 predictLongRunning，轮询用返回的 operation name。
func (p *GeminiProvider) CreateVideo(ctx context.Context, req VideoRequest) (VideoTask, error) {
	instance := map[string]any{"prompt": req.Prompt}
	if req.Mode == "frames" {
		if len(req.References) > 0 {
			instance["image"] = geminiInlinePayload(req.References[0])
		}
		if len(req.References) > 1 {
			instance["lastFrame"] = geminiInlinePayload(req.References[1])
		}
	} else if len(req.References) > 0 {
		references := make([]map[string]any, 0, len(req.References))
		for _, ref := range req.References {
			references = append(references, map[string]any{"image": geminiInlinePayload(ref), "referenceType": "asset"})
		}
		instance["referenceImages"] = references
	}
	payload := map[string]any{
		"instances": []map[string]any{instance},
		"parameters": map[string]any{
			"aspectRatio":     defaultString(req.Ratio, "16:9"),
			"durationSeconds": maxInt(req.Duration, 8),
			"resolution":      req.Resolution,
			"generateAudio":   req.GenerateAudio,
			"addWatermark":    req.Watermark,
		},
	}
	data, err := p.doJSON(ctx, http.MethodPost, p.modelURL(req.Model, "predictLongRunning"), payload)
	if err != nil {
		return VideoTask{}, err
	}
	var operation struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &operation); err != nil || operation.Name == "" {
		return VideoTask{}, fmt.Errorf("上游未返回视频任务 name")
	}
	return VideoTask{Provider: "gemini", UpstreamTaskID: operation.Name, PollAfterMs: 10000}, nil
}

func geminiInlinePayload(media InlineMedia) map[string]any {
	return map[string]any{
		"bytesBase64Encoded": base64.StdEncoding.EncodeToString(media.Data),
		"mimeType":           media.MimeType,
	}
}

// PollVideo 查询 operation，done 时返回视频 URL。
func (p *GeminiProvider) PollVideo(ctx context.Context, task VideoTask) (VideoState, error) {
	url := p.baseURL() + "/" + strings.TrimPrefix(task.UpstreamTaskID, "/")
	data, err := p.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return VideoState{}, err
	}
	var operation struct {
		Done  bool `json:"done"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Response *struct {
			GenerateVideoResponse *struct {
				GeneratedSamples []struct {
					Video struct {
						URI string `json:"uri"`
					} `json:"video"`
				} `json:"generatedSamples"`
			} `json:"generateVideoResponse"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &operation); err != nil {
		return VideoState{}, fmt.Errorf("解析 Gemini 任务失败: %w", err)
	}
	if operation.Error != nil && operation.Error.Message != "" {
		return VideoState{Status: "failed", Error: operation.Error.Message}, nil
	}
	if !operation.Done {
		return VideoState{Status: "pending"}, nil
	}
	if operation.Response == nil || operation.Response.GenerateVideoResponse == nil ||
		len(operation.Response.GenerateVideoResponse.GeneratedSamples) == 0 {
		return VideoState{Status: "failed", Error: "上游未返回可播放的视频"}, nil
	}
	uri := operation.Response.GenerateVideoResponse.GeneratedSamples[0].Video.URI
	if uri == "" {
		return VideoState{Status: "failed", Error: "上游未返回可播放的视频"}, nil
	}
	// 平台 Key 在服务端补上，URI 本身不带 key。
	download := uri
	if !strings.Contains(download, "key=") {
		separator := "?"
		if strings.Contains(download, "?") {
			separator = "&"
		}
		download = download + separator + "key=" + p.APIKey
	}
	return VideoState{Status: "succeeded", Video: &GeneratedImage{URL: download, MimeType: "video/mp4"}}, nil
}

// ChatStream 走 streamGenerateContent?alt=sse，翻译成统一回调。
func (p *GeminiProvider) ChatStream(ctx context.Context, req ChatRequest, sink StreamSink) error {
	contents, systemText := toGeminiContents(req.Messages)
	payload := map[string]any{
		"contents": contents,
	}
	if systemText != "" {
		payload["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": systemText}}}
	}
	if tools := toGeminiTools(req.Tools, req.ToolChoice); tools != nil {
		for key, value := range tools {
			payload[key] = value
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := p.modelURL(req.Model, "streamGenerateContent") + "?alt=sse"
	httpReq, err := p.newRequest(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
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
	var calls []ToolCall
	for {
		_, data, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if data == "" || data == "[DONE]" {
			continue
		}
		var parsed geminiPayload
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}
		if message := geminiErrorMessage(parsed); message != "" {
			return &ErrUpstream{Status: http.StatusBadGateway, Body: message}
		}
		for _, candidate := range parsed.Candidates {
			if candidate.FinishReason != "" {
				finishReason = strings.ToLower(candidate.FinishReason)
			}
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					if err := sink.Delta(part.Text); err != nil {
						return err
					}
				}
				if part.FunctionCall != nil && part.FunctionCall.Name != "" {
					arguments := "{}"
					if part.FunctionCall.Args != nil {
						encoded, _ := json.Marshal(part.FunctionCall.Args)
						arguments = string(encoded)
					}
					id := part.FunctionCall.ID
					if id == "" {
						id = fmt.Sprintf("call-%d", len(calls)+1)
					}
					calls = append(calls, ToolCall{ID: id, Name: part.FunctionCall.Name, Arguments: arguments})
				}
			}
		}
	}
	for _, call := range calls {
		if err := sink.ToolCall(call); err != nil {
			return err
		}
	}
	if len(calls) > 0 {
		finishReason = "tool_calls"
	}
	sink.Done(finishReason)
	return nil
}

func geminiErrorMessage(payload geminiPayload) string {
	if payload.Error != nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	if payload.PromptFeedback != nil && payload.PromptFeedback.BlockReason != "" {
		return "内容被上游安全策略拒绝：" + payload.PromptFeedback.BlockReason
	}
	for _, candidate := range payload.Candidates {
		if candidate.FinishReason == "SAFETY" || candidate.FinishReason == "PROHIBITED_CONTENT" {
			return "内容被上游安全策略拒绝：" + candidate.FinishReason
		}
	}
	return ""
}

func (p *GeminiProvider) modelURL(model, action string) string {
	clean := strings.TrimPrefix(model, "models/")
	return p.baseURL() + "/models/" + clean + ":" + action
}

func toGeminiContents(messages []ChatMessage) ([]geminiContent, string) {
	callNameByID := map[string]string{}
	contents := make([]geminiContent, 0, len(messages))
	systemParts := []string{}
	for _, message := range messages {
		if message.Role == "system" {
			systemParts = append(systemParts, geminiTextContent(message.Content))
			continue
		}
		if message.Role == "tool" {
			name := callNameByID[message.ToolCallID]
			if name == "" {
				name = "tool_result"
			}
			contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					ID:   message.ToolCallID,
					Name: name,
					Response: map[string]any{
						"result": message.Content,
					},
				},
			}}})
			continue
		}
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{Role: role, Parts: toGeminiParts(message.Content)})
	}
	return contents, strings.Join(systemParts, "\n\n")
}

func geminiTextContent(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if record, ok := item.(map[string]any); ok {
				if text, ok := record["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(content)
	}
}

func toGeminiParts(content any) []geminiPart {
	switch typed := content.(type) {
	case string:
		return []geminiPart{{Text: typed}}
	case []any:
		parts := make([]geminiPart, 0, len(typed))
		for _, item := range typed {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch record["type"] {
			case "text":
				if text, ok := record["text"].(string); ok {
					parts = append(parts, geminiPart{Text: text})
				}
			case "image_url":
				urlRecord, _ := record["image_url"].(map[string]any)
				url, _ := urlRecord["url"].(string)
				if mimeType, data, ok := parseDataURL(url); ok {
					parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: mimeType, Data: data}})
				}
			}
		}
		return parts
	default:
		return []geminiPart{{Text: fmt.Sprint(content)}}
	}
}

// parseDataURL 解析 data:image/png;base64,xxx 形式的内联素材。
func parseDataURL(url string) (string, string, bool) {
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(url, "data:")
	mimeType, encoded, ok := strings.Cut(rest, ";base64,")
	if !ok {
		return "", "", false
	}
	return mimeType, encoded, true
}

func toGeminiTools(tools []ChatTool, toolChoice any) map[string]any {
	if len(tools) == 0 {
		return nil
	}
	declarations := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		declarations = append(declarations, map[string]any{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	mode := "AUTO"
	allowed := []string{}
	switch typed := toolChoice.(type) {
	case string:
		if typed == "required" {
			mode = "ANY"
		}
	case map[string]any:
		mode = "ANY"
		if name, ok := typed["name"].(string); ok && name != "" {
			allowed = append(allowed, name)
		}
	}
	config := map[string]any{"mode": mode}
	if len(allowed) > 0 {
		config["allowedFunctionNames"] = allowed
	}
	return map[string]any{
		"tools":      []map[string]any{{"functionDeclarations": declarations}},
		"toolConfig": map[string]any{"functionCallingConfig": config},
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func maxInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
