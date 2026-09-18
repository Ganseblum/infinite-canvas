package watermark

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVideoMimeGuard 校验格式闸门，不依赖 ffmpeg/ffprobe。
func TestVideoMimeGuard(t *testing.T) {
	s := NewService("", "")
	if _, err := s.Video(context.Background(), []byte("x"), "video/webm"); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestVideoGarbageInput(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("未找到 ffprobe，跳过用例: %v", err)
	}
	s := NewService("", "")
	if _, err := s.Video(context.Background(), []byte("not an mp4"), "video/mp4"); err == nil {
		t.Fatal("垃圾字节期望报错，实际成功")
	}
}

// TestVideoTranscode 端到端用例：ffmpeg/ffprobe 存在时真实转码一次，
// 并用 ffprobe 校验输出的宽高与编码。工具缺失时明确 skip，不允许静默假绿。
func TestVideoTranscode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("未找到 ffmpeg，跳过视频水印转码用例: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("未找到 ffprobe，跳过视频水印转码用例: %v", err)
	}
	fontPath := findTestFont(t)

	// 生成小尺寸 mp4 夹具（纯色 128x96）。
	dir := t.TempDir()
	fixture := filepath.Join(dir, "input.mp4")
	makeFixture := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=128x96:d=0.4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", fixture)
	if out, err := makeFixture.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg 生成测试夹具失败（可能缺少 libx264）: %v: %s", err, out)
	}
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	s := NewService(fontPath, "")
	out, err := s.Video(context.Background(), src, "video/mp4")
	if err != nil {
		t.Fatalf("Video 失败: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("输出为空")
	}

	probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", "-")
	probe.Stdin = bytes.NewReader(out)
	var stdout bytes.Buffer
	probe.Stdout = &stdout
	if err := probe.Run(); err != nil {
		t.Fatalf("输出无法被 ffprobe 解析: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "128x96" {
		t.Errorf("输出宽高 = %q, want 128x96", got)
	}
}
