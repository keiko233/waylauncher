// Package icon draws Waylauncher's icons in code (a terminal prompt in a
// rounded window), so the repository carries no binary image assets.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

type seg struct{ x1, y1, x2, y2 float64 }

// The glyph lives in a 24x20 box, as in the design's menu bar icon.
var glyph = []seg{{7, 7.2, 10.6, 10}, {10.6, 10, 7, 12.8}, {12.4, 13, 17, 13}}

func distSeg(px, py float64, s seg) float64 {
	dx, dy := s.x2-s.x1, s.y2-s.y1
	t := ((px-s.x1)*dx + (py-s.y1)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(s.x1+t*dx), py-(s.y1+t*dy))
}

// sdRoundRect is the signed distance to a rounded rectangle (negative inside).
func sdRoundRect(px, py, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(px-cx) - (hw - r)
	qy := math.Abs(py-cy) - (hh - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

func render(w, h int, shade func(x, y float64) (color.NRGBA, bool)) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, renderImage(w, h, shade))
	return buf.Bytes()
}

// renderImage rasterises a shader with 4x4 supersampling.
func renderImage(w, h int, shade func(x, y float64) (color.NRGBA, bool)) *image.NRGBA {
	return renderImageSS(w, h, 4, shade)
}

func renderImageSS(w, h, ss int, shade func(x, y float64) (color.NRGBA, bool)) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c, ok := shade((float64(x)+(float64(sx)+.5)/float64(ss))/float64(w), (float64(y)+(float64(sy)+.5)/float64(ss))/float64(h))
					if ok {
						al := float64(c.A) / 255
						r += float64(c.R) * al
						g += float64(c.G) * al
						b += float64(c.B) * al
						a += al
					}
				}
			}
			if a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / float64(ss*ss))})
			}
		}
	}
	return img
}

// Tray is the menu bar icon: black on transparent, for use as a template
// image. scale 2 gives 44x36 pixels (22x18 points).
func Tray(scale int) []byte {
	w, h := 22*scale, 18*scale
	black := color.NRGBA{A: 255}
	return render(w, h, func(u, v float64) (color.NRGBA, bool) {
		// Map to the 24x20 glyph box.
		x, y := u*24, v*20
		if math.Abs(sdRoundRect(x, y, 12, 10, 10.2, 8.2, 3)) <= 0.9 {
			return black, true
		}
		for _, s := range glyph {
			if distSeg(x, y, s) <= 1.0 {
				return black, true
			}
		}
		return color.NRGBA{}, false
	})
}

// App is the application icon at size x size pixels: a dark rounded square
// holding a white prompt, inset the way macOS app icons are.
func App(size int) []byte {
	return render(size, size, func(u, v float64) (color.NRGBA, bool) {
		// Rounded square occupying 80% of the canvas.
		if sdRoundRect(u, v, .5, .5, .4, .4, .4*.2237*2.2) > 0 {
			return color.NRGBA{}, false
		}
		top := color.NRGBA{0x2B, 0x36, 0x52, 255}
		bot := color.NRGBA{0x14, 0x1B, 0x2C, 255}
		t := (v - .1) / .8
		bg := color.NRGBA{
			uint8(float64(top.R)*(1-t) + float64(bot.R)*t),
			uint8(float64(top.G)*(1-t) + float64(bot.G)*t),
			uint8(float64(top.B)*(1-t) + float64(bot.B)*t), 255}
		// Glyph box: 24x20 units scaled to 52% of the canvas width, centred.
		const gw = .52
		gx := (u - (.5 - gw/2)) / gw * 24
		gy := (v-.5)/gw*24 + 10
		white := color.NRGBA{255, 255, 255, 255}
		if math.Abs(sdRoundRect(gx, gy, 12, 10, 10.2, 8.2, 3)) <= 1.0 {
			return white, true
		}
		for _, s := range glyph {
			if distSeg(gx, gy, s) <= 1.15 {
				return white, true
			}
		}
		return bg, true
	})
}
