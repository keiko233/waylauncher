package icon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// ParseHex reads "#RRGGBB".
func ParseHex(s string) (color.NRGBA, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.NRGBA{}, fmt.Errorf("bad colour %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("bad colour %q", s)
	}
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, nil
}

var (
	fontOnce sync.Once
	boldFont *opentype.Font
	fontErr  error
)

func loadFont() (*opentype.Font, error) {
	fontOnce.Do(func() { boldFont, fontErr = opentype.Parse(gobold.TTF) })
	return boldFont, fontErr
}

// Tile draws the generated icon of an application without an icon of its
// own: a rounded square in the application's tile colour with its glyph
// (one or two letters, or a terminal prompt) in white, inset the way macOS
// app icons are. It is the same glyph and colour the Waylauncher window shows.
func Tile(glyph, tileHex string, size int) ([]byte, error) {
	base, err := ParseHex(tileHex)
	if err != nil {
		return nil, err
	}
	ss := 4
	if size >= 256 {
		ss = 2 // large icons need fewer samples per pixel
	}
	img := renderImageSS(size, size, ss, func(u, v float64) (color.NRGBA, bool) {
		if sdRoundRect(u, v, .5, .5, .4, .4, .4*.2237*2.2) > 0 {
			return color.NRGBA{}, false
		}
		// A soft top highlight over the tile colour.
		t := (v - .1) / .8
		k := 1.12 - .24*t
		sh := func(c uint8) uint8 { return uint8(min(255, float64(c)*k)) }
		return color.NRGBA{sh(base.R), sh(base.G), sh(base.B), 255}, true
	})
	f, err := loadFont()
	if err != nil {
		return nil, err
	}
	glyph = usableGlyph(f, glyph)
	// Letters take roughly 40% of the canvas; the prompt is wider.
	px := float64(size) * .40
	if len([]rune(glyph)) > 1 {
		px = float64(size) * .34
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.White), Face: face}
	w := d.MeasureString(glyph)
	m := face.Metrics()
	x := fixed.I(size)/2 - w/2
	y := fixed.I(size)/2 + (m.Ascent-m.Descent)/2
	d.Dot = fixed.Point26_6{X: x, Y: y}
	d.DrawString(glyph)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// usableGlyph keeps the glyph when the font can draw it, else its first
// ASCII letter or digit, else "?".
func usableGlyph(f *opentype.Font, g string) string {
	var b sfnt.Buffer
	ok := func(s string) bool {
		for _, r := range s {
			if i, err := f.GlyphIndex(&b, r); err != nil || i == 0 {
				return false
			}
		}
		return s != ""
	}
	if ok(g) {
		return g
	}
	for _, r := range g {
		if r < 128 && ok(string(r)) {
			return string(r)
		}
	}
	return "?"
}

// FromPNG fits a PNG into a transparent size x size square, keeping its
// aspect ratio. It is used for the "icon" of a profile.
func FromPNG(data []byte, size int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a PNG image: %w", err)
	}
	sb := src.Bounds()
	if sb.Dx() < 1 || sb.Dy() < 1 {
		return nil, errors.New("empty image")
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	w, h := size, size
	if sb.Dx() > sb.Dy() {
		h = max(1, size*sb.Dy()/sb.Dx())
	} else {
		w = max(1, size*sb.Dx()/sb.Dy())
	}
	r := image.Rect((size-w)/2, (size-h)/2, (size-w)/2+w, (size-h)/2+h)
	xdraw.CatmullRom.Scale(dst, r, src, sb, draw.Over, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// icnsTypes lists the PNG-carrying chunk types of the .icns format.
var icnsTypes = []struct {
	tag  string
	size int
}{
	{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
	{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
}

// ICNS builds an .icns file from a function that returns the PNG for a
// given pixel size (16 to 1024). It needs no iconutil.
func ICNS(pngAt func(size int) ([]byte, error)) ([]byte, error) {
	cache := map[int][]byte{}
	var body bytes.Buffer
	for _, t := range icnsTypes {
		p, ok := cache[t.size]
		if !ok {
			var err error
			if p, err = pngAt(t.size); err != nil {
				return nil, err
			}
			cache[t.size] = p
		}
		body.WriteString(t.tag)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(p)))
		body.Write(p)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes(), nil
}
