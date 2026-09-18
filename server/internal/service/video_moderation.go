package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/moderation"
)

// videoSampleFPS 读取 MODERATION_VIDEO_SAMPLE_FPS（默认 1 fps）。
// 该配置此前是死配置（差异清单 #24），现在真正生效。
func videoSampleFPS() float64 {
	if raw := os.Getenv("MODERATION_VIDEO_SAMPLE_FPS"); raw != "" {
		if fps, err := strconv.ParseFloat(raw, 64); err == nil && fps > 0 {
			return fps
		}
	}
	return 1
}

// videoMaxFrames 读取 MODERATION_VIDEO_MAX_FRAMES（默认 100）。
// 原实现写死 24 帧，长视频只审前约 24 秒，存在漏检窗口。
func videoMaxFrames() int {
	if raw := os.Getenv("MODERATION_VIDEO_MAX_FRAMES"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 100
}

// moderateVideo 对视频抽帧后逐帧送审。v1 不做端到端视频模型：
// 按固定间隔抽帧，任一帧超阈值即整条拒绝。
func moderateVideo(ctx context.Context, moderationService *ModerationService, userID uuid.UUID, data []byte, mimeType string) (Verdict, error) {
	frames, err := extractVideoFrames(ctx, data, videoSampleFPS())
	if err != nil {
		return Verdict{}, err
	}
	if len(frames) == 0 {
		return Verdict{Decision: moderation.DecisionPassed}, nil
	}
	var last Verdict
	for _, frame := range frames {
		verdict, err := moderationService.Check(ctx, userID, moderation.StageArtifact, moderation.ContentVideo, "", frame, "image/jpeg")
		if err != nil {
			return verdict, err
		}
		last = verdict
	}
	return last, nil
}

// extractVideoFrames 调用 ffmpeg 抽帧。ffmpeg 不存在时返回 ErrModerationUnavailable，
// 由 fail mode 决定放行还是拒绝，绝不静默通过。
func extractVideoFrames(ctx context.Context, data []byte, fps float64) ([][]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, ErrModerationUnavailable
	}
	dir, err := os.MkdirTemp("", "ic-moderation-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input.bin")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		return nil, err
	}
	pattern := filepath.Join(dir, "frame-%03d.jpg")
	command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", input,
		"-vf", "fps="+strconv.FormatFloat(fps, 'f', -1, 64), "-frames:v", strconv.Itoa(videoMaxFrames()), pattern)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, errors.New("视频抽帧失败: " + stderr.String())
	}
	entries, err := filepath.Glob(filepath.Join(dir, "frame-*.jpg"))
	if err != nil {
		return nil, err
	}
	frames := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		frame, err := os.ReadFile(entry)
		if err != nil {
			continue
		}
		frames = append(frames, frame)
	}
	return frames, nil
}
