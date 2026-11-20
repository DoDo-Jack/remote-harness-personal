package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	_ "image/gif"
	_ "image/jpeg"
)

// 把图片做成单色点阵（halftone）：只保留明暗，用主题色画点，背景透明
func halftone(src image.Image, w, h, cell int, tint color.RGBA) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	for cy := 0; cy < h; cy += cell {
		for cx := 0; cx < w; cx += cell {
			// 采样这一格的平均亮度
			var sum float64
			var n int
			for y := cy; y < cy+cell && y < h; y++ {
				for x := cx; x < cx+cell && x < w; x++ {
					sx := sb.Min.X + x*sw/w
					sy := sb.Min.Y + y*sh/h
					r, g, b, _ := src.At(sx, sy).RGBA()
					lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
					sum += lum
					n++
				}
			}
			if n == 0 {
				continue
			}
			lum := sum / float64(n)
			// 越暗点越大；亮的地方留空
			radius := (1 - lum) * float64(cell) * 0.62
			if radius < 0.6 {
				continue
			}
			ccx := float64(cx) + float64(cell)/2
			ccy := float64(cy) + float64(cell)/2
			r2 := radius * radius
			for y := int(ccy - radius); y <= int(ccy+radius); y++ {
				for x := int(ccx - radius); x <= int(ccx+radius); x++ {
					if x < 0 || y < 0 || x >= w || y >= h {
						continue
					}
					dx := float64(x) - ccx
					dy := float64(y) - ccy
					if dx*dx+dy*dy <= r2 {
						out.SetRGBA(x, y, tint)
					}
				}
			}
		}
	}
	return out
}

func writeHalftone(srcPath, dstPath string, w, h, cell int, tint color.RGBA) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	out := halftone(img, w, h, cell, tint)
	o, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer o.Close()
	return png.Encode(o, out)
}

var _ = math.Sqrt
