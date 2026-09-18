package watermark

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// findTestFont 从本机常见路径探测可用 TTF 字体；找不到时跳过依赖字体的用例。
// 默认文案是 ASCII，任意字体可用；接口按 CJK 字体设计，部署时经
// WATERMARK_FONT_PATH 指定 CJK 字体文件。
func findTestFont(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	// 兜底：常见字体目录下任一 .ttf。
	for _, pattern := range []string{
		"/usr/share/fonts/*/*.ttf",
		"/usr/share/fonts/*/*/*.ttf",
		"/usr/share/fonts/*/*/*/*.ttf",
		"/System/Library/Fonts/Supplemental/*.ttf",
	} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return matches[0]
		}
	}
	t.Log("未找到可用 TTF 字体，跳过依赖字体的用例")
	t.Skip("no TTF font found")
	return ""
}

// newTestService 用探测到的字体构造 Service，字体缺失时跳过。
func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(findTestFont(t), "")
}

// resetEnabledForTest 重置 Enabled 的 sync.OnceValue 缓存，配合 t.Setenv 逐用例验证。
func resetEnabledForTest() {
	enabledOnce = sync.OnceValue(enabledFromEnv)
}

func TestParseEnabled(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"", false},
		{"0", false},
		{"false", false},
		{"True", false},
		{"yes", false},
	}
	for _, tc := range cases {
		if got := parseEnabled(tc.raw); got != tc.want {
			t.Errorf("parseEnabled(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestEnabled(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"", false},
		{"false", false},
	}
	for _, tc := range cases {
		resetEnabledForTest()
		t.Setenv("WATERMARK_ENABLED", tc.raw)
		if got := Enabled(); got != tc.want {
			t.Errorf("WATERMARK_ENABLED=%q: Enabled() = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestWatermarkTimeout(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", 120 * time.Second},
		{"30", 30 * time.Second},
		{"abc", 120 * time.Second},
		{"0", 120 * time.Second},
		{"-5", 120 * time.Second},
	}
	for _, tc := range cases {
		t.Setenv("WATERMARK_TIMEOUT", tc.raw)
		if got := watermarkTimeout(); got != tc.want {
			t.Errorf("WATERMARK_TIMEOUT=%q: watermarkTimeout() = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestNewServiceDefaultText 空文案应回退默认文案：两次渲染结果一致（PNG 编码确定性）。
func TestNewServiceDefaultText(t *testing.T) {
	fontPath := findTestFont(t)
	empty, err := NewService(fontPath, "").TilePNG(160, 120)
	if err != nil {
		t.Fatalf("TilePNG 失败: %v", err)
	}
	defaultTextRendered, err := NewService(fontPath, "infinite-canvas").TilePNG(160, 120)
	if err != nil {
		t.Fatalf("TilePNG 失败: %v", err)
	}
	if !bytes.Equal(empty, defaultTextRendered) {
		t.Error("空文案未回退默认文案")
	}
}
