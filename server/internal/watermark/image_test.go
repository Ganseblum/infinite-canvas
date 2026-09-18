package watermark

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	_ "golang.org/x/image/webp" // 注册 webp 解码器，供 image.Decode 使用
)

// testWebPBase64 内嵌的 8x8 不透明纯色 VP8L 无损 webp 夹具
// （由 /tmp 生成器构造，避免依赖本机 webp 编码器）。
const testWebPBase64 = "UklGRjUAAABXRUJQVlA4TCkAAAAvB8ABEIiI/gcAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

// gradientImage 生成带渐变的测试底图。
func gradientImage(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 8 % 256),
				G: uint8(y * 8 % 256),
				B: uint8((x + y) * 4 % 256),
				A: 0xff,
			})
		}
	}
	return img
}

// countDiffPixels 统计两张图不同像素数，用于断言水印确实改变了像素。
func countDiffPixels(a, b image.Image) int {
	count := 0
	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				count++
			}
		}
	}
	return count
}

func TestImageJPEG(t *testing.T) {
	s := newTestService(t)
	var srcBuf bytes.Buffer
	if err := jpeg.Encode(&srcBuf, gradientImage(320, 240), nil); err != nil {
		t.Fatalf("生成 jpeg 夹具失败: %v", err)
	}
	srcDecoded, err := jpeg.Decode(bytes.NewReader(srcBuf.Bytes()))
	if err != nil {
		t.Fatalf("jpeg 夹具解码失败: %v", err)
	}
	out, outMime, err := s.Image(srcBuf.Bytes(), "image/jpeg")
	if err != nil {
		t.Fatalf("Image(jpeg) 失败: %v", err)
	}
	if outMime != "image/jpeg" {
		t.Errorf("outMime = %q, want image/jpeg", outMime)
	}
	if bytes.Equal(out, srcBuf.Bytes()) {
		t.Error("水印后字节未变化")
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("输出无法再解码: %v", err)
	}
	if n := countDiffPixels(srcDecoded, got); n == 0 {
		t.Error("水印后像素无变化")
	}
}

func TestImagePNG(t *testing.T) {
	s := newTestService(t)
	src := gradientImage(320, 240)
	var srcBuf bytes.Buffer
	if err := png.Encode(&srcBuf, src); err != nil {
		t.Fatalf("生成 png 夹具失败: %v", err)
	}
	out, outMime, err := s.Image(srcBuf.Bytes(), "image/png")
	if err != nil {
		t.Fatalf("Image(png) 失败: %v", err)
	}
	if outMime != "image/png" {
		t.Errorf("outMime = %q, want image/png", outMime)
	}
	if bytes.Equal(out, srcBuf.Bytes()) {
		t.Error("水印后字节未变化")
	}
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("输出无法再解码: %v", err)
	}
	if n := countDiffPixels(src, got); n == 0 {
		t.Error("水印后像素无变化")
	}
}

func TestImageWebP(t *testing.T) {
	s := newTestService(t)
	src, err := base64.StdEncoding.DecodeString(testWebPBase64)
	if err != nil {
		t.Fatalf("webp 夹具 base64 解码失败: %v", err)
	}
	fixtureImg, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("webp 夹具无法解码: %v", err)
	}
	out, outMime, err := s.Image(src, "image/webp")
	if err != nil {
		t.Fatalf("Image(webp) 失败: %v", err)
	}
	if outMime != "image/png" {
		t.Errorf("outMime = %q, want image/png（webp 转 PNG 输出）", outMime)
	}
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("输出无法按 PNG 解码: %v", err)
	}
	if got.Bounds() != fixtureImg.Bounds() {
		t.Errorf("输出尺寸 = %v, want %v", got.Bounds(), fixtureImg.Bounds())
	}
}

func TestImageUnsupportedFormat(t *testing.T) {
	// 格式闸门在解码前生效，不依赖字体。
	s := NewService("", "")
	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, gradientImage(32, 32), nil); err != nil {
		t.Fatalf("生成 gif 夹具失败: %v", err)
	}
	cases := []struct {
		name string
		src  []byte
		mime string
	}{
		{"gif", gifBuf.Bytes(), "image/gif"},
		{"bmp", []byte("BM"), "image/bmp"},
		{"empty mime", []byte("x"), ""},
	}
	for _, tc := range cases {
		_, _, err := s.Image(tc.src, tc.mime)
		if !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("%s: err = %v, want ErrUnsupportedFormat", tc.name, err)
		}
	}
}

func TestImageInvalidBytes(t *testing.T) {
	s := NewService("", "")
	cases := []struct {
		name string
		src  []byte
		mime string
	}{
		{"empty jpeg", nil, "image/jpeg"},
		{"garbage jpeg", []byte("not a jpeg"), "image/jpeg"},
		{"empty png", nil, "image/png"},
		{"garbage png", []byte{0x00, 0x01, 0x02}, "image/png"},
		{"garbage webp", []byte("RIFF????WEBP"), "image/webp"},
	}
	for _, tc := range cases {
		if _, _, err := s.Image(tc.src, tc.mime); err == nil {
			t.Errorf("%s: 期望报错，实际成功", tc.name)
		}
	}
}
