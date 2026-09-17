package provider

import "testing"

func TestNormalizeQuality(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"auto", ""},
		{" 1K ", "low"},
		{"2k", "medium"},
		{"4K", "high"},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"standard", "standard"},
		{"hd", "hd"},
		{"ultra", ""},
	}
	for _, tc := range cases {
		if got := normalizeQuality(tc.in); got != tc.want {
			t.Errorf("normalizeQuality(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeBackground(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"transparent", "transparent"},
		{" TRANSPARENT ", "transparent"},
		{"opaque", ""},
		{"white", ""},
	}
	for _, tc := range cases {
		if got := normalizeBackground(tc.in); got != tc.want {
			t.Errorf("normalizeBackground(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveRequestSize(t *testing.T) {
	cases := []struct {
		name    string
		quality string
		size    string
		want    string
		wantErr bool
	}{
		{"空尺寸不带字段", "high", "", "", false},
		{"auto 不带字段", "high", "auto", "", false},
		{"auto 忽略大小写", "high", "AUTO", "", false},
		{"合法像素原样返回", "high", " 1024x1024 ", "1024x1024", false},
		{"比例按 4k 档位换算", "high", "16:9", "3840x2160", false},
		{"别名质量参与换算", "4k", "16:9", "3840x2160", false},
		{"比例按 2k 档位换算", "medium", "3:4", "1536x2048", false},
		{"缺省质量按 1k 档位换算", "", "1:1", "1024x1024", false},
		{"无预设比例按质量反算", "low", "5:4", "1136x912", false},
		{"非法质量回落到默认短边", "ultra", "5:4", "1280x1024", false},
		{"步长不符报错", "high", "1023x1024", "", true},
		{"像素低于下限报错", "high", "100x100", "", true},
		{"像素超过上限报错", "high", "4096x4096", "", true},
		{"最长边超限报错", "high", "5120x1024", "", true},
		{"比例超过 3:1 报错", "high", "10:1", "", true},
		{"比例分母为 0 报错", "high", "16:0", "", true},
		{"非尺寸非比例报错", "high", "not-a-size", "", true},
		{"纯数字报错", "high", "1024", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRequestSize(tc.quality, tc.size)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveRequestSize(%q, %q) = %q, want error", tc.quality, tc.size, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRequestSize(%q, %q) 返回错误: %v", tc.quality, tc.size, err)
			}
			if got != tc.want {
				t.Fatalf("resolveRequestSize(%q, %q) = %q, want %q", tc.quality, tc.size, got, tc.want)
			}
		})
	}
}

func TestNormalizeVideoResolution(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "720p"},
		{"auto", "720p"},
		{"low", "480p"},
		{"high", "720p"},
		{"medium", "720p"},
		{"1080p", "1080p"},
		{"1080", "1080p"},
		{"720P", "720p"},
	}
	for _, tc := range cases {
		if got := normalizeVideoResolution(tc.in); got != tc.want {
			t.Errorf("normalizeVideoResolution(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeVideoSize(t *testing.T) {
	cases := []struct {
		name       string
		size       string
		resolution string
		want       string
	}{
		{"auto 不带尺寸", "auto", "720p", ""},
		{"空值不带尺寸", "", "720p", ""},
		{"像素值原样返回", "1280x720", "720p", "1280x720"},
		{"横版比例换算", "16:9", "720p", "1280x720"},
		{"竖版比例换算", "9:16", "720p", "720x1280"},
		{"缺省分辨率按 720 换算", "4:3", "", "960x720"},
		{"相近比例取最接近项", "7:5", "720p", "960x720"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeVideoSize(tc.size, tc.resolution); got != tc.want {
				t.Fatalf("normalizeVideoSize(%q, %q) = %q, want %q", tc.size, tc.resolution, got, tc.want)
			}
		})
	}
}

func TestComputeVideoSize(t *testing.T) {
	cases := []struct {
		resolution string
		ratio      string
		want       string
	}{
		{"720p", "1:1", "720x720"},
		{"720p", "4:3", "960x720"},
		{"720p", "9:16", "720x1280"},
		{"720p", "21:9", "1680x720"},
		{"1080p", "16:9", "1920x1080"},
		{"480p", "9:16", "480x854"},
		{"", "1:1", "720x720"},
		{"bad", "1:1", "720x720"},
	}
	for _, tc := range cases {
		if got := computeVideoSize(tc.resolution, tc.ratio); got != tc.want {
			t.Errorf("computeVideoSize(%q, %q) = %q, want %q", tc.resolution, tc.ratio, got, tc.want)
		}
	}
}

func TestResolveVideoMode(t *testing.T) {
	cases := []struct {
		mode       string
		imageCount int
		want       string
	}{
		{"", 0, "frames"},
		{"", 2, "frames"},
		{"", 3, "reference"},
		{"reference", 0, "reference"},
		{" frames ", 1, "frames"},
		{"frames", 5, "reference"},
	}
	for _, tc := range cases {
		if got := resolveVideoMode(tc.mode, tc.imageCount); got != tc.want {
			t.Errorf("resolveVideoMode(%q, %d) = %q, want %q", tc.mode, tc.imageCount, got, tc.want)
		}
	}
}

func TestNormalizeVideoSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "6"},
		{"abc", "6"},
		{"6", "6"},
		{"12", "12"},
		{"0", "4"},
		{"3", "4"},
		{"4", "4"},
		{"30", "30"},
		{"40", "30"},
	}
	for _, tc := range cases {
		if got := normalizeVideoSeconds(tc.in); got != tc.want {
			t.Errorf("normalizeVideoSeconds(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeSpeechFields(t *testing.T) {
	voices := []struct {
		in   string
		want string
	}{
		{"alloy", "alloy"},
		{"Nova", "nova"},
		{" marin ", "marin"},
		{"unknown", "alloy"},
		{"", "alloy"},
	}
	for _, tc := range voices {
		if got := normalizeSpeechVoice(tc.in); got != tc.want {
			t.Errorf("normalizeSpeechVoice(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	formats := []struct {
		in   string
		want string
	}{
		{"wav", "wav"},
		{"MP3", "mp3"},
		{"pcm", "pcm"},
		{"ogg", "mp3"},
		{"", "mp3"},
	}
	for _, tc := range formats {
		if got := normalizeSpeechFormat(tc.in); got != tc.want {
			t.Errorf("normalizeSpeechFormat(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeSpeechSpeed(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 1},
		{-1, 1},
		{0.1, 0.25},
		{0.25, 0.25},
		{1, 1},
		{1.234, 1.23},
		{3.456, 3.46},
		{4, 4},
		{5, 4},
	}
	for _, tc := range cases {
		if got := normalizeSpeechSpeed(tc.in); got != tc.want {
			t.Errorf("normalizeSpeechSpeed(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestClosestGeminiAspectRatio(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1024x1024", "1:1"},
		{"1920x1080", "16:9"},
		{"2048x1536", "4:3"},
		{"1536x2048", "3:4"},
		{"16:9", "16:9"},
		{"9:16", "9:16"},
		{"1:4", "1:4"},
		{"abc", ""},
	}
	for _, tc := range cases {
		if got := closestGeminiAspectRatio(tc.in); got != tc.want {
			t.Errorf("closestGeminiAspectRatio(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveGeminiImageSize(t *testing.T) {
	cases := []struct {
		quality string
		size    string
		want    string
	}{
		{"high", "", "4K"},
		{"4k", "", "4K"},
		{"2k", "", "2K"},
		{"low", "", "1K"},
		{"", "1024x1024", "1K"},
		{"", "2048x2048", "2K"},
		{"", "3000x3000", "2K"},
		{"", "4096x4096", "4K"},
		{"", "512x512", "512"},
		{"unknown", "2048x2048", "2K"},
		{"", "abc", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := resolveGeminiImageSize(tc.quality, tc.size); got != tc.want {
			t.Errorf("resolveGeminiImageSize(%q, %q) = %q, want %q", tc.quality, tc.size, got, tc.want)
		}
	}
}

func TestSupportsGeminiImageSize(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gemini-3-pro-image-preview", true},
		{"gemini-3.1-flash-image-preview", true},
		{"GEMINI-3-PRO-IMAGE", true},
		{"gemini-2.5-flash-image", false},
		{"nano-banana", false},
	}
	for _, tc := range cases {
		if got := supportsGeminiImageSize(tc.model); got != tc.want {
			t.Errorf("supportsGeminiImageSize(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
