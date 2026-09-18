package watermark

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

// 水印视觉参数（media-watermark 计划批准的边界值，执行中不得调整）。
const (
	// defaultText 默认水印文案（NewService 的 text 参数为空时使用）。
	defaultText = "infinite-canvas"
	// minFontSize 平铺文字最小字号（px）。
	minFontSize = 16
	// fontSizeDivisor 字号按帧宽折算的除数：字号 = max(minFontSize, 帧宽/fontSizeDivisor)。
	fontSizeDivisor = 28
	// tileSpacingFactor 平铺间距倍数：间距 = 字号 × tileSpacingFactor（横纵同值）。
	tileSpacingFactor = 4
	// tileRotateDeg 平铺文字倾斜角度（度）。负值为逆时针。
	tileRotateDeg = -30
	// outlinePx 文字描边宽度（px）。
	outlinePx = 1
	// tileAlpha 水印整体透明度。
	tileAlpha = 0.32
)

var (
	// textColor 白色文字。
	textColor = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	// outlineColor 深色描边。
	outlineColor = color.RGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff}
)

// TilePNG 渲染整帧平铺水印 PNG（width×height 全幅），供 Video 内部叠加与测试复用。
func (s *Service) TilePNG(width, height int) ([]byte, error) {
	canvas, err := s.renderTile(image.Rect(0, 0, width, height))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, fmt.Errorf("水印 PNG 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// renderTile 渲染覆盖 bounds 全幅的平铺水印层（透明底，整体透明度 tileAlpha）。
func (s *Service) renderTile(bounds image.Rectangle) (*image.RGBA, error) {
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("水印尺寸非法: %dx%d", width, height)
	}
	fontSize := max(minFontSize, width/fontSizeDivisor)
	tile, err := s.drawTextTile(fontSize)
	if err != nil {
		return nil, err
	}
	step := fontSize * tileSpacingFactor
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	rad := tileRotateDeg * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	// 以文字块中心为锚点平铺；锚点范围外扩一格，保证旋转后仍覆盖全帧。
	cx := float64(tile.Bounds().Dx()) / 2
	cy := float64(tile.Bounds().Dy()) / 2
	for anchorY := -step; anchorY <= height+step; anchorY += step {
		for anchorX := -step; anchorX <= width+step; anchorX += step {
			m := f64.Aff3{
				cos, -sin, float64(anchorX) - (cos*cx - sin*cy),
				sin, cos, float64(anchorY) - (sin*cx + cos*cy),
			}
			xdraw.NearestNeighbor.Transform(canvas, m, tile, tile.Bounds(), xdraw.Over, nil)
		}
	}
	applyAlpha(canvas, tileAlpha)
	return canvas, nil
}

// drawTextTile 渲染单个文字块（不透明）：白色文字 + 深色描边，
// 描边通过在 8 个方向各偏移 outlinePx 画一遍深色文字实现。
// 返回的文字块四周留 pad 像素余量容纳描边。
func (s *Service) drawTextTile(fontSize int) (*image.RGBA, error) {
	face, err := s.newFace(float64(fontSize))
	if err != nil {
		return nil, err
	}
	defer face.Close()
	pad := outlinePx + 1
	metrics := face.Metrics()
	baseline := pad + metrics.Ascent.Ceil()
	d := &font.Drawer{Face: face, Src: image.NewUniform(textColor)}
	width := d.MeasureString(s.text).Ceil()
	tile := image.NewRGBA(image.Rect(0, 0, width+2*pad, (metrics.Ascent+metrics.Descent).Ceil()+2*pad))
	d.Dst = tile
	d.Src = image.NewUniform(outlineColor)
	for dx := -outlinePx; dx <= outlinePx; dx++ {
		for dy := -outlinePx; dy <= outlinePx; dy++ {
			if dx == 0 && dy == 0 {
				continue
			}
			d.Dot = fixed.P(pad+dx, baseline+dy)
			d.DrawString(s.text)
		}
	}
	d.Src = image.NewUniform(textColor)
	d.Dot = fixed.P(pad, baseline)
	d.DrawString(s.text)
	return tile, nil
}

// applyAlpha 将预乘 RGBA 四个通道整体乘以 factor，实现水印整体透明度。
// RGB 与 A 同步缩放以保持预乘一致性，编码 PNG 时可正确还原为半透明白色。
func applyAlpha(img *image.RGBA, factor float64) {
	for i := 0; i < len(img.Pix); i++ {
		img.Pix[i] = uint8(float64(img.Pix[i]) * factor)
	}
}
