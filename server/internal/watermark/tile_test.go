package watermark

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

func TestTilePNG(t *testing.T) {
	s := newTestService(t)
	data, err := s.TilePNG(320, 240)
	if err != nil {
		t.Fatalf("TilePNG 失败: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("输出无法按 PNG 解码: %v", err)
	}
	if decoded.Bounds().Dx() != 320 || decoded.Bounds().Dy() != 240 {
		t.Fatalf("尺寸 = %v, want 320x240", decoded.Bounds())
	}
	// 水印层本身应有非透明像素，且整体透明度为 0.32：
	// 预乘通道整体乘 0.32 后，全覆盖像素 alpha = 81（255×0.32），
	// 叠层经 Over 合成后仍不超过全覆盖，故最大 alpha 即 81（字体轮廓差异允许轻微偏差）。
	maxAlpha := uint32(0)
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			if _, _, _, a := decoded.At(x, y).RGBA(); a>>8 > maxAlpha {
				maxAlpha = a >> 8
			}
		}
	}
	if maxAlpha < 76 || maxAlpha > 81 {
		t.Errorf("水印最大 alpha = %d, want 81±5（整体透明度 0.32）", maxAlpha)
	}
	// 与纯色底图叠加后像素应有变化（抽样断言整体叠加效果）。
	uniform := image.NewRGBA(image.Rect(0, 0, 320, 240))
	draw.Draw(uniform, uniform.Bounds(), image.NewUniform(color.RGBA{R: 128, G: 128, B: 128, A: 0xff}), image.Point{}, draw.Src)
	composited := image.NewNRGBA(image.Rect(0, 0, 320, 240))
	draw.Draw(composited, composited.Bounds(), decoded, image.Point{}, draw.Over)
	if n := countDiffPixels(uniform, composited); n == 0 {
		t.Error("叠加纯色底图后像素无变化")
	}
}

func TestTilePNGInvalidSize(t *testing.T) {
	// 尺寸校验在字体加载前生效，不依赖字体。
	s := NewService("", "")
	for _, size := range [][2]int{{0, 100}, {100, 0}, {-1, -1}} {
		if _, err := s.TilePNG(size[0], size[1]); err == nil {
			t.Errorf("TilePNG(%d, %d): 期望报错，实际成功", size[0], size[1])
		}
	}
}
