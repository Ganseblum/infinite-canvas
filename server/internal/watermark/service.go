// Package watermark 提供服务端媒体水印渲染：对免费用户生成的图片/视频
// 烧录平铺文字水印，展示与下载均为水印版。图片为 Go 内重编码烧录，
// 视频为渲染整帧水印 PNG 后由 ffmpeg overlay 重编码。
package watermark

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// ErrUnsupportedFormat 表示该媒体格式不支持烧录水印（如 gif、非 mp4 视频）。
var ErrUnsupportedFormat = errors.New("watermark: unsupported media format")

// Service 渲染平铺文字水印。构造时不读字体文件，
// 首次渲染时惰性加载并缓存（见 loadFont）。
type Service struct {
	fontPath string
	text     string

	once    sync.Once
	parsed  *opentype.Font
	fontErr error
}

// NewService 创建水印服务。fontPath 指向 TTF/OTF 字体文件（接口按 CJK 字体设计，
// ASCII 文案任意字体可用）；text 为水印文案，空串时使用默认文案。
func NewService(fontPath, text string) *Service {
	if text == "" {
		text = defaultText
	}
	return &Service{fontPath: fontPath, text: text}
}

// loadFont 惰性加载并解析字体文件，结果（含失败）进程内缓存：
// 首次渲染时触发，之后同一 Service 的渲染不再读盘。
func (s *Service) loadFont() (*opentype.Font, error) {
	s.once.Do(func() {
		data, err := os.ReadFile(s.fontPath)
		if err != nil {
			s.fontErr = fmt.Errorf("读取水印字体失败: %w", err)
			return
		}
		parsed, err := opentype.Parse(data)
		if err != nil {
			s.fontErr = fmt.Errorf("解析水印字体失败: %w", err)
			return
		}
		s.parsed = parsed
	})
	return s.parsed, s.fontErr
}

// newFace 按字号创建 face。face 非并发安全，按渲染调用临时创建。
func (s *Service) newFace(size float64) (font.Face, error) {
	parsed, err := s.loadFont()
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// enabledFromEnv 解析 WATERMARK_ENABLED，供 sync.OnceValue 缓存。
func enabledFromEnv() bool {
	return parseEnabled(os.Getenv("WATERMARK_ENABLED"))
}

// parseEnabled 判断开关取值："1"/"true"/"TRUE" 视为开，其余（含未设置）默认关。
func parseEnabled(raw string) bool {
	return raw == "1" || raw == "true" || raw == "TRUE"
}

// enabledOnce 缓存开关解析结果（进程内只读一次）。
var enabledOnce = sync.OnceValue(enabledFromEnv)

// Enabled 报告水印功能开关（WATERMARK_ENABLED，默认关）。flag 关闭即全量回滚点，
// 供生成挂钩（后续任务）判断是否烧录水印；不经 config，由调用方在构造 Service 时
// 通过参数传入字体路径与文案。
func Enabled() bool {
	return enabledOnce()
}

// watermarkTimeout 读取 WATERMARK_TIMEOUT（默认 120s，非法值回退默认），
// 供 Video() 内部 context.WithTimeout 使用。对齐 video_moderation.go 的
// videoSampleFPS 先例：直接读 env，不经 config。
func watermarkTimeout() time.Duration {
	const defaultTimeout = 120 * time.Second
	if raw := os.Getenv("WATERMARK_TIMEOUT"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultTimeout
}
