package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NSFWJSProvider 调用同机部署的 NSFWJS 推理服务（Node 侧进程）。
type NSFWJSProvider struct {
	Endpoint  string
	Threshold float64
	Client    *http.Client
}

func NewNSFWJS(endpoint string, threshold float64, timeout time.Duration) *NSFWJSProvider {
	return &NSFWJSProvider{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Threshold: threshold,
		Client:    &http.Client{Timeout: timeout},
	}
}

func (p *NSFWJSProvider) Name() string { return "nsfwjs" }

// ModerateImage 把图片字节以 base64 送审，解析五分类概率。
func (p *NSFWJSProvider) ModerateImage(ctx context.Context, req Request) (Result, error) {
	if len(req.Data) == 0 {
		return Result{}, fmt.Errorf("送审图片为空")
	}
	payload := map[string]any{
		"mimeType": req.MimeType,
		"data":     base64.StdEncoding.EncodeToString(req.Data),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint+"/classify", bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return Result{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("NSFWJS 返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		RequestID   string `json:"requestId"`
		Predictions []struct {
			ClassName   string  `json:"className"`
			Probability float64 `json:"probability"`
		} `json:"predictions"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Result{}, fmt.Errorf("%w: 解析 NSFWJS 响应失败", ErrUnavailable)
	}
	labels := []string{}
	summary := map[string]any{}
	for _, prediction := range parsed.Predictions {
		name := strings.ToLower(prediction.ClassName)
		summary[name] = prediction.Probability
		if isNSFWLabel(name) && prediction.Probability >= p.Threshold {
			labels = append(labels, "adult")
		}
	}
	decision := DecisionPassed
	if len(labels) > 0 {
		decision = DecisionRejected
	}
	return Result{
		Decision:          decision,
		RiskLabels:        dedupe(labels),
		ProviderRequestID: parsed.RequestID,
		Summary:           summary,
	}, nil
}

// Moderate 图片走图片模型；文本交回组合 provider 处理。
func (p *NSFWJSProvider) Moderate(ctx context.Context, req Request) (Result, error) {
	if req.ContentType != ContentImage && req.ContentType != ContentVideo {
		return Result{}, fmt.Errorf("NSFWJS 只处理图片与视频帧")
	}
	return p.ModerateImage(ctx, req)
}

// isNSFWLabel 判定 NSFWJS 五分类中的成人内容标签（normal、drawings 之外）。
func isNSFWLabel(name string) bool {
	switch name {
	case "porn", "hentai", "sexy":
		return true
	default:
		return false
	}
}

// DetoxifyProvider 调用同机部署的 Detoxify 推理服务（Python 侧进程）。
type DetoxifyProvider struct {
	Endpoint  string
	Threshold float64
	Client    *http.Client
}

func NewDetoxify(endpoint string, threshold float64, timeout time.Duration) *DetoxifyProvider {
	return &DetoxifyProvider{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Threshold: threshold,
		Client:    &http.Client{Timeout: timeout},
	}
}

func (p *DetoxifyProvider) Name() string { return "detoxify" }

// ModerateText 送审文本，解析毒性标签概率。
func (p *DetoxifyProvider) ModerateText(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Text) == "" {
		return Result{Decision: DecisionPassed, Summary: map[string]any{}}, nil
	}
	raw, err := json.Marshal(map[string]string{"text": req.Text})
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint+"/classify", bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return Result{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("Detoxify 返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		RequestID string             `json:"requestId"`
		Scores    map[string]float64 `json:"scores"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Result{}, fmt.Errorf("%w: 解析 Detoxify 响应失败", ErrUnavailable)
	}
	labels := []string{}
	for name, score := range parsed.Scores {
		if score >= p.Threshold {
			labels = append(labels, toxicityLabel(name))
		}
	}
	decision := DecisionPassed
	if len(labels) > 0 {
		decision = DecisionRejected
	}
	return Result{
		Decision:          decision,
		RiskLabels:        dedupe(labels),
		ProviderRequestID: parsed.RequestID,
		Summary:           toSummary(parsed.Scores),
	}, nil
}

// Moderate 仅接受文本送审，其余类型报错。
func (p *DetoxifyProvider) Moderate(ctx context.Context, req Request) (Result, error) {
	if req.ContentType != ContentText {
		return Result{}, fmt.Errorf("Detoxify 只处理文本")
	}
	return p.ModerateText(ctx, req)
}

// toxicityLabel 把 Detoxify 标签归一到项目风险标签；未识别的标签按 toxic 兜底（宁可误拒）。
func toxicityLabel(name string) string {
	switch name {
	case "threat":
		return "threat"
	case "identity_hate", "identity_attack":
		return "hate"
	case "severe_toxic", "obscene", "insult", "toxic", "toxicity":
		return "toxic"
	default:
		return "toxic"
	}
}

func toSummary(scores map[string]float64) map[string]any {
	summary := make(map[string]any, len(scores))
	for key, value := range scores {
		summary[key] = value
	}
	return summary
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// ParseProbability 把 JSON 数字统一读成 float64（兼容字符串数字）。
func ParseProbability(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
