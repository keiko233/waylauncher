package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

func TestIconsDecode(t *testing.T) {
	for name, data := range map[string][]byte{"tray": Tray(2), "app": App(64)} {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b := img.Bounds()
		if name == "tray" && (b.Dx() != 44 || b.Dy() != 36) {
			t.Fatalf("tray size %v", b)
		}
		if _, _, _, a := img.At(b.Dx()/2, b.Dy()/2).RGBA(); name == "app" && a == 0 {
			t.Fatal("app icon is transparent in the middle")
		}
	}
}

func TestTileAndICNS(t *testing.T) {
	for _, g := range []string{">_", "Ko", "?", "日本"} {
		data, err := Tile(g, "#1D5FD1", 128)
		if err != nil {
			t.Fatalf("%q: %v", g, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds().Dx() != 128 {
			t.Fatalf("%q: %v %v", g, err, img)
		}
		// The tile colour is there, and so is the white glyph.
		var white, blueish int
		for y := 0; y < 128; y++ {
			for x := 0; x < 128; x++ {
				r, gg, b, a := img.At(x, y).RGBA()
				if a == 0xffff && r > 0xf000 && gg > 0xf000 && b > 0xf000 {
					white++
				}
				if a == 0xffff && b > r+0x3000 {
					blueish++
				}
			}
		}
		if white < 20 || blueish < 3000 {
			t.Errorf("%q: white=%d blue=%d", g, white, blueish)
		}
	}
	if _, err := Tile("A", "nope", 64); err == nil {
		t.Error("bad colour accepted")
	}

	src, _ := Tile("A", "#23262E", 40) // not square below: make a wide PNG
	wide := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	var wb bytes.Buffer
	_ = png.Encode(&wb, wide)
	got, err := FromPNG(wb.Bytes(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if im, _ := png.Decode(bytes.NewReader(got)); im.Bounds().Dx() != 64 || im.Bounds().Dy() != 64 {
		t.Fatalf("FromPNG size %v", im.Bounds())
	}
	if _, err := FromPNG([]byte("not a png"), 64); err == nil {
		t.Error("garbage accepted")
	}
	_ = src

	icns, err := ICNS(func(n int) ([]byte, error) { return Tile("A", "#23262E", n) })
	if err != nil {
		t.Fatal(err)
	}
	if string(icns[:4]) != "icns" || int(binary.BigEndian.Uint32(icns[4:8])) != len(icns) {
		t.Fatalf("bad icns header %q len %d", icns[:8], len(icns))
	}
	// Walk the chunks.
	n, off := 0, 8
	for off < len(icns) {
		l := int(binary.BigEndian.Uint32(icns[off+4 : off+8]))
		if l < 8 || off+l > len(icns) {
			t.Fatalf("bad chunk at %d", off)
		}
		if _, err := png.Decode(bytes.NewReader(icns[off+8 : off+l])); err != nil {
			t.Fatalf("chunk %s: %v", icns[off:off+4], err)
		}
		off += l
		n++
	}
	if n != len(icnsTypes) {
		t.Fatalf("%d chunks", n)
	}
}
