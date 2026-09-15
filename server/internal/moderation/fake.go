package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// FakeProvider 是测试与本地开发的可控实现：按规则命中拒绝，也可以注入故障。
type FakeProvider struct {
	mu sync.Mutex
	// RejectTexts 命中的文本片段会返回拒绝。
	RejectTexts []string
	// RejectLabels 图片审核固定返回的风险标签，为空表示通过。
	RejectLabels []string
	// FailWith 非空时所有送审返回该错误（用于故障注入）。
	FailWith error
	// Calls 记录送审次数，供断言「被拒输入没有调用上游」等场景。
	Calls int
}

func NewFake() *FakeProvider { return &FakeProvider{} }

func (p *FakeProvider) Name() string { return "fake" }

func (p *FakeProvider) Moderate(ctx context.Context, req Request) (Result, error) {
	p.mu.Lock()
	p.Calls++
	p.mu.Unlock()
	if p.FailWith != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, p.FailWith)
	}
	summary := map[string]any{"provider": "fake"}
	switch req.ContentType {
	case ContentText:
		lower := strings.ToLower(req.Text)
		for _, needle := range p.RejectTexts {
			if needle != "" && strings.Contains(lower, strings.ToLower(needle)) {
				return Result{Decision: DecisionRejected, RiskLabels: []string{"toxic"}, ProviderRequestID: "fake-text", Summary: summary}, nil
			}
		}
		return Result{Decision: DecisionPassed, ProviderRequestID: "fake-text", Summary: summary}, nil
	case ContentImage, ContentVideo:
		labels := append([]string(nil), p.RejectLabels...)
		decision := DecisionPassed
		if len(labels) > 0 {
			decision = DecisionRejected
		}
		return Result{Decision: decision, RiskLabels: labels, ProviderRequestID: "fake-image", Summary: summary}, nil
	default:
		return Result{Decision: DecisionPassed, ProviderRequestID: "fake-other", Summary: summary}, nil
	}
}

// CallsCount 返回送审次数。
func (p *FakeProvider) CallsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Calls
}

// HashContent 计算内容 SHA-256，用于审核记录的追踪与去重。
func HashContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
