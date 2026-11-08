package gui

import (
	"bytes"
	"image"
	_ "image/png" // registers the PNG decoder for image.DecodeConfig
	"os"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/iconpath"
)

// maxIconPixels bounds the decoded size of a profile icon (a 512x512 PNG is
// the normal case); anything larger is not decoded and the glyph tile shows.
const maxIconPixels = 4096

// recheckEvery is how often the file of a shown icon is stat-ed for a change
// (a frame is not the time to touch the disk every time).
const recheckEvery = 2 * time.Second

type iconEntry struct {
	mod     time.Time
	size    int64
	img     ui.ImageSource // nil: unusable, the glyph tile is drawn
	checked time.Time
}

// profileIcons decodes profile icons once per path and modification time.
// It lives on the main (UI) thread.
type profileIcons struct {
	base string // directory for relative icon paths (the config directory)
	m    map[string]*iconEntry
}

// profileIcon returns the decoded icon of a profile, or nil when it has none
// or it cannot be used (missing, not a regular file, over iconpath.MaxBytes,
// not a PNG, too many pixels): the caller then draws the glyph tile, the
// same one the Dock bundle is generated from. The file is resolved exactly as
// the Dock bundle does (package iconpath): "~/" and relative paths work, and
// a cached icon path from Discover is used unchanged.
func (a *App) profileIcon(icon string) ui.ImageSource {
	if icon == "" {
		return nil
	}
	now := a.c.Now()
	pi := &a.icons
	if pi.m == nil || len(pi.m) > 1024 {
		pi.m = map[string]*iconEntry{}
	}
	e := pi.m[icon]
	if e != nil && now.Sub(e.checked) < recheckEvery {
		return e.img
	}
	p, fi, err := iconpath.Resolve(icon, pi.base)
	if err != nil {
		pi.m[icon] = &iconEntry{checked: now}
		return nil
	}
	if e != nil && e.mod.Equal(fi.ModTime()) && e.size == fi.Size() {
		e.checked = now
		return e.img
	}
	ne := &iconEntry{mod: fi.ModTime(), size: fi.Size(), checked: now}
	pi.m[icon] = ne
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width > maxIconPixels || cfg.Height > maxIconPixels {
		return nil
	}
	if img, err := ui.DecodeImage(data); err == nil {
		ne.img = img
	}
	return ne.img
}

// appIcon draws an application's icon at size: its profile icon when one
// loads, else the glyph tile.
func (a *App) appIcon(icon, glyph, tile string, size, radius, fs float32) {
	if img := a.profileIcon(icon); img != nil {
		ui.Image(a.c, img).Size(size, size).Shrink(0).Fit(ui.Contain)
		return
	}
	a.tile(glyph, tile, size, radius, fs)
}
