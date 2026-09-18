package watermark

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/webp"
)

// jpegQuality 图片水印后 jpeg 重编码质量（计划批准的边界值）。
const jpegQuality = 90

// Image 对图片烧录平铺水印：jpeg/png 原格式重编码（jpeg 质量 90），
// webp 解码后转 PNG 输出（outMime = image/png）；gif 及其它格式一律返回
// ErrUnsupportedFormat。重编码从解码像素出发，EXIF 等元数据随之剥离。
func (s *Service) Image(src []byte, srcMime string) (out []byte, outMime string, err error) {
	var decoded image.Image
	switch srcMime {
	case "image/jpeg":
		decoded, err = jpeg.Decode(bytes.NewReader(src))
		if err != nil {
			return nil, "", fmt.Errorf("jpeg 解码失败: %w", err)
		}
	case "image/png":
		decoded, err = png.Decode(bytes.NewReader(src))
		if err != nil {
			return nil, "", fmt.Errorf("png 解码失败: %w", err)
		}
	case "image/webp":
		decoded, err = webp.Decode(bytes.NewReader(src))
		if err != nil {
			return nil, "", fmt.Errorf("webp 解码失败: %w", err)
		}
	default:
		return nil, "", ErrUnsupportedFormat
	}
	bounds := decoded.Bounds()
	// 先转到 NRGBA 画布（兼容 YCbCr/Paletted/Gray 等解码结果），再叠加水印层。
	base := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(base, base.Bounds(), decoded, bounds.Min, draw.Src)
	tile, err := s.renderTile(base.Bounds())
	if err != nil {
		return nil, "", err
	}
	draw.Draw(base, base.Bounds(), tile, image.Point{}, draw.Over)

	var buf bytes.Buffer
	switch srcMime {
	case "image/jpeg":
		if err := jpeg.Encode(&buf, base, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", fmt.Errorf("jpeg 编码失败: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	default: // image/png 与 image/webp 均按 PNG 输出
		if err := png.Encode(&buf, base); err != nil {
			return nil, "", fmt.Errorf("png 编码失败: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	}
}
