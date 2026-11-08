package gui

import (
	"os"

	"github.com/egoist/mygo/ui"
)

const maxIconBytes = 4 << 20

// loadCocoaIcon reads Cocoa-Way's icon once, from the first candidate path
// that decodes. Cocoa-Way is GPL-3.0 and its bundle has no icon file, so the
// image is never copied into this repository or the app; a missing or
// unreadable file just means the status dot is shown alone.
func (a *App) loadCocoaIcon() {
	if a.iconLoaded {
		return
	}
	a.iconLoaded = true
	for _, p := range a.iconPaths {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxIconBytes {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if img, err := ui.DecodeImage(data); err == nil {
			a.cocoaIcon = img
			return
		}
	}
}

// cocoaMark is Cocoa-Way's icon at size dips with a status dot on its corner,
// or the dot alone when there is no icon.
func (a *App) cocoaMark(size float32, dot ui.Color) {
	if a.cocoaIcon == nil {
		ui.Box(a.c).Size(8, 8).Radius(4).Shrink(0).Background(dot)
		return
	}
	ui.Box(a.c).Size(size, size).Shrink(0).Children(func() {
		ui.Image(a.c, a.cocoaIcon).Size(size, size).Fit(ui.Contain)
		ui.Box(a.c).Size(8, 8).Radius(4).Background(dot).Border(1.5, a.p.panel).Absolute().Right(-3).Bottom(-3)
	})
}
