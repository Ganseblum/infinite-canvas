// Package moderation 封装内容审核适配层：只管把内容送进模型并解析结论，
// 不含扣点、存储与业务状态机。换 provider 只改这一层。
package moderation

import (
	"context"
	"errors"
)

// Stage 是审核发生的链路阶段。
type Stage string

const (
	StagePrompt    Stage = "prompt"
	StageReference Stage = "reference"
	StageArtifact  Stage = "artifact"
	StageUpload    Stage = "upload"
)

// ContentType 是送审内容类型。
type ContentType string

const (
	ContentText  ContentType = "text"
	ContentImage ContentType = "image"
	ContentVideo ContentType = "video"
	ContentAudio ContentType = "audio"
)

// 决策结论。
const (
	DecisionPassed   = "passed"
	DecisionRejected = "rejected"
	DecisionError    = "error"
)

// ErrUnavailable 表示审核服务不可用（超时、限流、5xx、模型加载失败）。
var ErrUnavailable = errors.New("审核服务不可用")

// Request 是一次送审请求。
type Request struct {
	Stage       Stage
	ContentType ContentType
	Text        string
	Data        []byte
	MimeType    string
	ContentHash string
}

// Result 是审核结论。
type Result struct {
	Decision          string
	RiskLabels        []string
	ProviderRequestID string
	Summary           map[string]any
}

// Provider 把内容送进具体模型并返回结论。
type Provider interface {
	Name() string
	// Moderate 送审。服务不可用时返回 ErrUnavailable 包装的错误。
	Moderate(ctx context.Context, req Request) (Result, error)
}

// TextProvider 与 ImageProvider 让组合 provider 各取所需。
type TextProvider interface {
	ModerateText(ctx context.Context, req Request) (Result, error)
}

type ImageProvider interface {
	ModerateImage(ctx context.Context, req Request) (Result, error)
}
