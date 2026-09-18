package watermark

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Video 对 mp4 视频烧录平铺水印：内部渲染整帧平铺水印 PNG 后用 ffmpeg
// overlay 重编码，返回 mp4 字节。仅支持 video/mp4；ffmpeg/ffprobe 缺失、
// 超时、转码失败都返回错误，绝不静默跳过。沿用 video_moderation.go
// extractVideoFrames 的 LookPath/MkdirTemp/exec.CommandContext 范式。
func (s *Service) Video(ctx context.Context, src []byte, srcMime string) ([]byte, error) {
	if srcMime != "video/mp4" {
		return nil, ErrUnsupportedFormat
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, errors.New("视频水印需要 ffprobe，但未在 PATH 找到")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("视频水印需要 ffmpeg，但未在 PATH 找到")
	}
	ctx, cancel := context.WithTimeout(ctx, watermarkTimeout())
	defer cancel()

	dir, err := os.MkdirTemp("", "ic-watermark-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	input := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(input, src, 0o600); err != nil {
		return nil, err
	}
	width, height, err := probeVideoSize(ctx, input)
	if err != nil {
		return nil, err
	}
	overlayPNG, err := s.TilePNG(width, height)
	if err != nil {
		return nil, err
	}
	overlayPath := filepath.Join(dir, "overlay.png")
	if err := os.WriteFile(overlayPath, overlayPNG, 0o600); err != nil {
		return nil, err
	}
	output := filepath.Join(dir, "out.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", input, "-i", overlayPath,
		"-filter_complex", "[1][0]scale2ref[wm][base];[base][wm]overlay=0:0",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart", "-c:a", "copy",
		output)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		slog.Warn("视频水印转码失败", "err", err, "stderr", stderr.String())
		return nil, fmt.Errorf("视频水印转码失败: %s", stderr.String())
	}
	return os.ReadFile(output)
}

// probeVideoSize 用 ffprobe 读取首个视频流的宽高。
func probeVideoSize(ctx context.Context, input string) (int, int, error) {
	command := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=width,height",
		"-of", "csv=s=x:p=0", input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return 0, 0, fmt.Errorf("ffprobe 读取视频宽高失败: %s", stderr.String())
	}
	parts := strings.Split(strings.TrimSpace(stdout.String()), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ffprobe 宽高输出无法解析: %q", stdout.String())
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe 宽度无法解析: %w", err)
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe 高度无法解析: %w", err)
	}
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("ffprobe 返回非法宽高: %dx%d", width, height)
	}
	return width, height, nil
}
