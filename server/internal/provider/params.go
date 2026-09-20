package provider

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 本文件是对原版前端参数归一逻辑的逐条移植（原版位置：web/src/services/api/image.ts
// 与 web/src/lib/media-size.ts，见提交 eb30e48 之前的版本）。
//
// 服务端转发上线后这些逻辑一度缺失，导致发给上游的参数与原版不一致：尺寸比例没有换算成
// 像素、质量别名没有归一、视频尺寸换算表不同、分辨率没有兜底等。这里按原版语义恢复，
// 常量取值与原版保持一致，不要凭直觉调整。
//
// URL 拼接刻意优于原版：原版「凡不以 /v1 结尾就补 /v1」会把 https://xxx/v1beta 拼成
// /v1beta/v1/...；本实现识别 /v1beta 后不再补（见 openai.go 的 endpoint）。这是刻意保留
// 的修正，不是漏项。

const (
	imageDefaultShortSide = 1024
	imageSizeStep         = 16
	imageMinPixels        = 655360
	imageMaxPixels        = 8294400
	imageMaxEdge          = 3840
	imageMaxRatio         = 3.0

	imageOutputFormat = "png"

	videoSecondsMin = 4
	videoSecondsMax = 30
)

// imageSizePresets 分辨率档位 × 宽高比 → 像素尺寸，取值与原版 imageSizePresets 一致。
var imageSizePresets = map[string]map[string]string{
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

// qualityBase 是各质量档位的基准边长，用于按比例反算像素尺寸。
var qualityBase = map[string]int{
	"low": 1024, "medium": 2048, "high": 2880, "standard": 1024, "hd": 2048,
}

// qualityAliases 把 1k/2k/4k 这类别名归一到标准档位。
var qualityAliases = map[string]string{"1k": "low", "2k": "medium", "4k": "high"}

// geminiSupportedRatios 是 Gemini 图像接受的宽高比白名单，其它比例要取最接近的一项。
var geminiSupportedRatios = []string{
	"1:1", "1:4", "1:8", "2:3", "3:2", "3:4", "4:1", "4:3", "4:5", "5:4", "8:1", "9:16", "16:9", "21:9",
}

// geminiImageSizeByQuality 是 Gemini imageSize 的质量映射。high 对应 4K，不要写成 2K。
var geminiImageSizeByQuality = map[string]string{
	"low": "1K", "medium": "2K", "high": "4K", "standard": "1K", "hd": "2K",
}

var videoRatioOptions = []struct {
	value         string
	width, height float64
}{
	{"1:1", 1, 1}, {"3:4", 3, 4}, {"4:3", 4, 3}, {"16:9", 16, 9},
	{"9:16", 9, 16}, {"21:9", 21, 9},
}

// normalizeQuality 归一质量取值：别名映射到标准档位，无法识别的返回空串（调用方应丢弃该字段）。
func normalizeQuality(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if alias, ok := qualityAliases[value]; ok {
		value = alias
	}
	if _, ok := qualityBase[value]; ok {
		return value
	}
	return ""
}

// normalizeBackground 只放行 transparent；其它取值（含空）都表示保持默认不透明背景。
func normalizeBackground(raw string) string {
	if strings.ToLower(strings.TrimSpace(raw)) == "transparent" {
		return "transparent"
	}
	return ""
}

func parsePixelSize(value string) (int, int, bool) {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(value)), "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func parseRatio(value string) (float64, float64, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	height, errH := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func validateImageSize(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("图像尺寸必须是正整数")
	}
	if width%imageSizeStep != 0 || height%imageSizeStep != 0 {
		return fmt.Errorf("图像尺寸必须是 %d 的整数倍", imageSizeStep)
	}
	longSide, shortSide := width, height
	if height > width {
		longSide, shortSide = height, width
	}
	if longSide > imageMaxEdge {
		return fmt.Errorf("图像最长边不能超过 %d", imageMaxEdge)
	}
	if float64(longSide)/float64(shortSide) > imageMaxRatio {
		return fmt.Errorf("图像宽高比不能超过 %d:1", int(imageMaxRatio))
	}
	if pixels := width * height; pixels < imageMinPixels || pixels > imageMaxPixels {
		return fmt.Errorf("图像总像素需在 %d 到 %d 之间", imageMinPixels, imageMaxPixels)
	}
	return nil
}

// resolveSizeByRatio 按比例换算像素尺寸：preset 表命中直接用；未命中按质量基准边反算，
// 长短边都对齐到 16 的整数倍后过总像素与长边校验。
func resolveSizeByRatio(quality, ratio string) (string, error) {
	ratioWidth, ratioHeight, ok := parseRatio(ratio)
	if !ok {
		return "", fmt.Errorf("图像比例格式无效")
	}
	ratioValue := math.Max(ratioWidth, ratioHeight) / math.Min(ratioWidth, ratioHeight)
	if ratioValue > imageMaxRatio {
		return "", fmt.Errorf("图像宽高比不能超过 %d:1", int(imageMaxRatio))
	}

	if preset, ok := imageSizePresets[scaleForQuality(quality)][ratio]; ok {
		return preset, nil
	}

	landscape := ratioWidth >= ratioHeight
	longRatio := ratioWidth / ratioHeight
	if !landscape {
		longRatio = ratioHeight / ratioWidth
	}
	var longSide, shortSide int
	if base, ok := qualityBase[quality]; ok {
		targetPixels := float64(base * base)
		longSide = int(math.Floor(math.Sqrt(targetPixels*longRatio)/imageSizeStep)) * imageSizeStep
		shortSide = int(math.Round(float64(longSide)/longRatio/float64(imageSizeStep))) * imageSizeStep
	} else {
		shortSide = imageDefaultShortSide
		longSide = int(math.Round(float64(shortSide)*longRatio/float64(imageSizeStep))) * imageSizeStep
	}

	width, height := longSide, shortSide
	if !landscape {
		width, height = shortSide, longSide
	}
	if err := validateImageSize(width, height); err != nil {
		return "", err
	}
	return fmt.Sprintf("%dx%d", width, height), nil
}

func scaleForQuality(quality string) string {
	switch quality {
	case "high":
		return "4k"
	case "medium", "hd":
		return "2k"
	default:
		return "1k"
	}
}

// resolveRequestSize 把模型参数里的尺寸归一成上游可用的像素值：
// 空或 auto 表示不带该字段；已是像素值则校验后原样返回；比例串按质量换算成像素。
func resolveRequestSize(quality, size string) (string, error) {
	value := strings.TrimSpace(size)
	if value == "" || strings.EqualFold(value, "auto") {
		return "", nil
	}
	if width, height, ok := parsePixelSize(value); ok {
		if err := validateImageSize(width, height); err != nil {
			return "", err
		}
		return fmt.Sprintf("%dx%d", width, height), nil
	}
	if strings.Contains(value, ":") {
		return resolveSizeByRatio(normalizeQuality(quality), value)
	}
	return "", fmt.Errorf("图像尺寸格式无效")
}

// supportsGeminiImageSize 只有 Gemini 3 系列接受 imageSize 参数，其它模型带上会被拒。
func supportsGeminiImageSize(model string) bool {
	value := strings.ToLower(model)
	return strings.Contains(value, "gemini-3") || strings.Contains(value, "3.1") || strings.Contains(value, "3-pro")
}

func closestGeminiAspectRatio(size string) string {
	width, height, ok := parsePixelSize(size)
	if !ok {
		ratioWidth, ratioHeight, okRatio := parseRatio(size)
		if !okRatio {
			return ""
		}
		width, height = int(ratioWidth), int(ratioHeight)
	}
	target := float64(width) / float64(height)
	best := geminiSupportedRatios[0]
	for _, item := range geminiSupportedRatios {
		currentWidth, currentHeight, okCurrent := parseRatio(item)
		bestWidth, bestHeight, okBest := parseRatio(best)
		if !okCurrent || !okBest {
			continue
		}
		if math.Abs(currentWidth/currentHeight-target) < math.Abs(bestWidth/bestHeight-target) {
			best = item
		}
	}
	return best
}

// geminiImageConfig 构造 Gemini 的 imageConfig：比例取白名单里最接近的一项，
// imageSize 仅在模型支持且能量化出档位时下发；两者都没有时不带 imageConfig。
func geminiImageConfig(req ImageRequest) map[string]any {
	config := map[string]any{}
	value := strings.TrimSpace(req.Size)
	if value != "" && !strings.EqualFold(value, "auto") {
		if ratio := closestGeminiAspectRatio(value); ratio != "" {
			config["aspectRatio"] = ratio
		}
	}
	if supportsGeminiImageSize(req.Model) {
		if size := resolveGeminiImageSize(req.Quality, value); size != "" {
			config["imageSize"] = size
		}
	}
	return config
}

// resolveGeminiImageSize 优先按质量档映射；无质量取值时按像素反查 preset 档位，
// 再查不到按长边分档兜底，保证高分辨率输入也能量化出一个 imageSize。
func resolveGeminiImageSize(quality, size string) string {
	if normalized := normalizeQuality(quality); normalized != "" {
		return geminiImageSizeByQuality[normalized]
	}
	width, height, ok := parsePixelSize(size)
	if !ok {
		return ""
	}
	for scale, presets := range imageSizePresets {
		for _, preset := range presets {
			if preset == fmt.Sprintf("%dx%d", width, height) {
				return strings.ToUpper(scale)
			}
		}
	}
	edge := width
	if height > edge {
		edge = height
	}
	switch {
	case edge <= 768:
		return "512"
	case edge <= 1536:
		return "1K"
	case edge <= 3072:
		return "2K"
	default:
		return "4K"
	}
}

// normalizeVideoResolution 任何输入都要产出非空的 `${n}p`，原版同样保证不为空。
func normalizeVideoResolution(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "low":
		return "480p"
	case "auto", "high", "medium", "":
		return "720p"
	}
	return strings.TrimSuffix(value, "p") + "p"
}

// normalizeVideoSize 与 gin 原版一致：auto 不带值，像素值原样，比例按分辨率换算出偶数尺寸。
func normalizeVideoSize(size, resolution string) string {
	value := strings.TrimSpace(size)
	if strings.EqualFold(value, "auto") {
		return ""
	}
	if width, height, ok := parsePixelSize(value); ok {
		return fmt.Sprintf("%dx%d", width, height)
	}
	ratio := inferVideoRatio(value)
	if ratio == "" {
		return ""
	}
	return computeVideoSize(resolution, ratio)
}

// inferVideoRatio 把任意 size 输入归到白名单比例：完全无法解析时回落 16:9，
// 与原版「永不丢弃比例信息」的行为一致。
func inferVideoRatio(size string) string {
	value := strings.TrimSpace(size)
	if value == "" || strings.EqualFold(value, "auto") {
		return ""
	}
	for _, option := range videoRatioOptions {
		if option.value == value {
			return value
		}
	}
	var target float64
	if width, height, ok := parsePixelSize(value); ok {
		target = float64(width) / float64(height)
	} else if width, height, ok := parseRatio(value); ok {
		target = width / height
	} else {
		return "16:9"
	}
	best, bestDiff := "16:9", math.MaxFloat64
	for _, option := range videoRatioOptions {
		current := option.width / option.height
		if diff := math.Abs(current - target); diff < bestDiff {
			best, bestDiff = option.value, diff
		}
	}
	return best
}

// computeVideoSize 按原版公式换算：长边取分辨率的 p 值，短边按比例折算，都取偶数。
func computeVideoSize(resolution, ratio string) string {
	ratioWidth, ratioHeight, ok := parseRatio(ratio)
	if !ok {
		return ""
	}
	p := videoResolutionNumber(resolution)
	// 与原版一致：长边与短边都要取偶；只对长边取偶会让自定义 p（如 1081）留下奇数边。
	landscape := ratioWidth >= ratioHeight
	var width, height int
	if landscape {
		width = evenRound(float64(p) * ratioWidth / ratioHeight)
		height = evenRound(float64(p))
	} else {
		width = evenRound(float64(p))
		height = evenRound(float64(p) * ratioHeight / ratioWidth)
	}
	return fmt.Sprintf("%dx%d", width, height)
}

// videoResolutionNumber 从 `${n}p` 或 low/high 等别名解析分辨率数值，非法回落 720。
func videoResolutionNumber(raw string) int {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "low":
		return 480
	case "auto", "high", "medium", "":
		return 720
	}
	number, err := strconv.Atoi(strings.TrimSuffix(value, "p"))
	if err != nil || number <= 0 {
		return 720
	}
	return number
}

// evenRound 取最接近的偶数，最小 2（视频编码要求宽高为偶数）。
func evenRound(value float64) int {
	return max(2, int(math.Round(value/2))*2)
}

// resolveVideoMode 与原版一致：显式 reference 或参考图超过 2 张走 reference，否则首尾帧。
func resolveVideoMode(mode string, imageCount int) string {
	if strings.TrimSpace(mode) == "reference" || imageCount > 2 {
		return "reference"
	}
	return "frames"
}

// normalizeVideoSeconds 把时长归一到 4..30 的整数字符串，非法值回落 6。
func normalizeVideoSeconds(value string) string {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		seconds = 6
	}
	if seconds < videoSecondsMin {
		seconds = videoSecondsMin
	}
	if seconds > videoSecondsMax {
		seconds = videoSecondsMax
	}
	return strconv.Itoa(seconds)
}

// speechVoices 与 speechFormats 是原版的取值白名单，白名单外的取值分别回落 alloy 与 mp3。
var speechVoices = map[string]bool{
	"alloy": true, "ash": true, "ballad": true, "coral": true, "echo": true, "fable": true,
	"nova": true, "onyx": true, "sage": true, "shimmer": true, "verse": true, "marin": true, "cedar": true,
}

var speechFormats = map[string]bool{
	"mp3": true, "wav": true, "opus": true, "aac": true, "flac": true, "pcm": true,
}

// normalizeSpeechVoice 原版恒发 voice 字段，白名单外（含空）一律回落 alloy。
func normalizeSpeechVoice(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if speechVoices[value] {
		return value
	}
	return "alloy"
}

// normalizeSpeechFormat 原版恒发 response_format，白名单外（含空）一律回落 mp3。
func normalizeSpeechFormat(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if speechFormats[value] {
		return value
	}
	return "mp3"
}

// normalizeSpeechSpeed 速度裁剪到 0.25..4 并保留两位小数。原版对空值会因 Number("") 得 0
// 而被钳到 0.25，这里按缺省处理回落 1（与 OpenAI 的默认语速一致），避免未传值时发出慢速语音。
func normalizeSpeechSpeed(speed float64) float64 {
	if speed <= 0 {
		return 1
	}
	if speed < 0.25 {
		speed = 0.25
	}
	if speed > 4 {
		speed = 4
	}
	return math.Round(speed*100) / 100
}
