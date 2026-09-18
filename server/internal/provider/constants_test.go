package provider

import (
	"reflect"
	"testing"
)

// 原版常量防漂移快照：下方期望值全部为字面量，逐值对照自原版实现的 dump——
// 图像尺寸表 / 视频比例与时长取自 web/src/lib/media-size.ts（imageSizePresets、
// videoRatioOptions、VIDEO_SECONDS_MIN/MAX），质量表与 Gemini 映射取自提交
// eb30e48 之前版本 web/src/services/api/image.ts（QUALITY_BASE、QUALITY_ALIASES、
// IMAGE_OUTPUT_FORMAT、GEMINI_SUPPORTED_RATIOS、GEMINI_IMAGE_SIZE_BY_QUALITY），
// 语音白名单与语速钳制取自 web/src/lib/audio-generation.ts。
// 任何值不一致都说明实现已偏离原版：改动前必须先对照原版确认是有意调整，禁止凭直觉改。

// TestOriginalImageSizePresetsSnapshot 1K/2K/4K × 9 比例共 27 个像素值逐值比对。
func TestOriginalImageSizePresetsSnapshot(t *testing.T) {
	want := map[string]map[string]string{
		"1k": {
			"1:1": "1024x1024", "2:3": "1024x1536", "3:2": "1536x1024", "4:3": "1024x768",
			"3:4": "768x1024", "16:9": "1536x864", "9:16": "864x1536", "21:9": "2016x864", "9:21": "864x2016",
		},
		"2k": {
			"1:1": "2048x2048", "2:3": "1360x2048", "3:2": "2048x1360", "4:3": "2048x1536",
			"3:4": "1536x2048", "16:9": "2048x1152", "9:16": "1152x2048", "21:9": "2688x1152", "9:21": "1152x2688",
		},
		"4k": {
			"1:1": "2880x2880", "2:3": "2336x3520", "3:2": "3520x2336", "4:3": "3312x2480",
			"3:4": "2480x3312", "16:9": "3840x2160", "9:16": "2160x3840", "21:9": "3840x1648", "9:21": "1648x3840",
		},
	}
	if !reflect.DeepEqual(imageSizePresets, want) {
		t.Fatalf("imageSizePresets 偏离原版:\n got  %#v\n want %#v", imageSizePresets, want)
	}
}

// TestOriginalQualityTablesSnapshot 质量别名与各档位基准边长，以及产出文件格式。
func TestOriginalQualityTablesSnapshot(t *testing.T) {
	wantAliases := map[string]string{"1k": "low", "2k": "medium", "4k": "high"}
	if !reflect.DeepEqual(qualityAliases, wantAliases) {
		t.Fatalf("qualityAliases 偏离原版: got %#v", qualityAliases)
	}
	wantBase := map[string]int{"low": 1024, "medium": 2048, "high": 2880, "standard": 1024, "hd": 2048}
	if !reflect.DeepEqual(qualityBase, wantBase) {
		t.Fatalf("qualityBase 偏离原版: got %#v", qualityBase)
	}
	if imageOutputFormat != "png" {
		t.Fatalf("imageOutputFormat = %q, want png", imageOutputFormat)
	}
}

// TestOriginalVideoRatioAndSecondsSnapshot 视频比例表、时长上下界 4..30。
func TestOriginalVideoRatioAndSecondsSnapshot(t *testing.T) {
	want := []struct {
		value         string
		width, height float64
	}{
		{"1:1", 1, 1}, {"3:4", 3, 4}, {"4:3", 4, 3}, {"16:9", 16, 9},
		{"9:16", 9, 16}, {"21:9", 21, 9},
	}
	if !reflect.DeepEqual(videoRatioOptions, want) {
		t.Fatalf("videoRatioOptions 偏离原版: got %#v", videoRatioOptions)
	}
	if videoSecondsMin != 4 || videoSecondsMax != 30 {
		t.Fatalf("视频时长上下界 = %d..%d, want 4..30", videoSecondsMin, videoSecondsMax)
	}
	if got := normalizeVideoSeconds("3"); got != "4" {
		t.Fatalf("下界裁剪失效: normalizeVideoSeconds(3) = %q, want 4", got)
	}
	if got := normalizeVideoSeconds("31"); got != "30" {
		t.Fatalf("上界裁剪失效: normalizeVideoSeconds(31) = %q, want 30", got)
	}
}

// TestOriginalGeminiTablesSnapshot Gemini 比例白名单与 imageSize 质量映射（high 必须是 4K）。
func TestOriginalGeminiTablesSnapshot(t *testing.T) {
	wantRatios := []string{
		"1:1", "1:4", "1:8", "2:3", "3:2", "3:4", "4:1", "4:3", "4:5", "5:4", "8:1", "9:16", "16:9", "21:9",
	}
	if !reflect.DeepEqual(geminiSupportedRatios, wantRatios) {
		t.Fatalf("geminiSupportedRatios 偏离原版: got %#v", geminiSupportedRatios)
	}
	wantSizes := map[string]string{"low": "1K", "medium": "2K", "high": "4K", "standard": "1K", "hd": "2K"}
	if !reflect.DeepEqual(geminiImageSizeByQuality, wantSizes) {
		t.Fatalf("geminiImageSizeByQuality 偏离原版: got %#v", geminiImageSizeByQuality)
	}
	if got := geminiImageSizeByQuality["high"]; got != "4K" {
		t.Fatalf("Gemini high 档 imageSize = %q, want 4K", got)
	}
}

// TestOriginalSpeechWhitelistSnapshot 语音音色 / 格式白名单与语速上下界 0.25..4。
// 语速缺省回落 1 是本仓对原版的刻意修正（原版空值会被钳到 0.25），见 params.go 注释。
func TestOriginalSpeechWhitelistSnapshot(t *testing.T) {
	wantVoices := map[string]bool{
		"alloy": true, "ash": true, "ballad": true, "coral": true, "echo": true, "fable": true,
		"nova": true, "onyx": true, "sage": true, "shimmer": true, "verse": true, "marin": true, "cedar": true,
	}
	if !reflect.DeepEqual(speechVoices, wantVoices) {
		t.Fatalf("speechVoices 偏离原版: got %#v", speechVoices)
	}
	wantFormats := map[string]bool{"mp3": true, "wav": true, "opus": true, "aac": true, "flac": true, "pcm": true}
	if !reflect.DeepEqual(speechFormats, wantFormats) {
		t.Fatalf("speechFormats 偏离原版: got %#v", speechFormats)
	}
	cases := []struct {
		in   float64
		want float64
	}{
		{0.25, 0.25},
		{4, 4},
		{0.24, 0.25},
		{4.5, 4},
	}
	for _, tc := range cases {
		if got := normalizeSpeechSpeed(tc.in); got != tc.want {
			t.Errorf("normalizeSpeechSpeed(%v) = %v, want %v（语速上下界应保持 0.25..4）", tc.in, got, tc.want)
		}
	}
}
