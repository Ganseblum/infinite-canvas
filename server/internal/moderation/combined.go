package moderation

import (
	"context"
	"fmt"
)

// CombinedProvider 把文本与图片审核组合成一个 Provider：
// 文本交给 Detoxify，图片与视频帧交给 NSFWJS。两者都失败才算不可用。
type CombinedProvider struct {
	text  TextProvider
	image ImageProvider
}

func NewCombined(image ImageProvider, text TextProvider) *CombinedProvider {
	return &CombinedProvider{text: text, image: image}
}

func (p *CombinedProvider) Name() string { return "nsfwjs+detoxify" }

func (p *CombinedProvider) Moderate(ctx context.Context, req Request) (Result, error) {
	switch req.ContentType {
	case ContentText:
		if p.text == nil {
			return Result{}, fmt.Errorf("%w: 未配置文本审核", ErrUnavailable)
		}
		return p.text.ModerateText(ctx, req)
	case ContentImage, ContentVideo:
		if p.image == nil {
			return Result{}, fmt.Errorf("%w: 未配置图片审核", ErrUnavailable)
		}
		return p.image.ModerateImage(ctx, req)
	default:
		return Result{Decision: DecisionPassed, Summary: map[string]any{"skipped": true}}, nil
	}
}
