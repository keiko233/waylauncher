package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestScreenshots renders every page and sheet, light and dark, to PNG files
// for visual review. It runs only with WAYLAUNCHER_SHOTS=<directory>.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("WAYLAUNCHER_SHOTS")
	if dir == "" {
		t.Skip("set WAYLAUNCHER_SHOTS to a directory to write screenshots")
	}
	os.MkdirAll(dir, 0o755)
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		h := newHarness(t, sample, dark)
		h.tt.SetScale(1)
		shot := func(n string) {
			h.settle()
			f, err := os.Create(filepath.Join(dir, name+"-"+n+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, h.tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		shot("apps")
		h.click("Open")
		h.waitText("1 connected")
		h.click("Running")
		h.waitText("Selected instance")
		time.Sleep(1100 * time.Millisecond)
		shot("running")
		h.app.Navigate(Nav{View: PageHost})
		shot("host-unchecked")
		h.click("Run checks")
		h.waitText("Remote waypipe")
		shot("host")
		h.click("Settings")
		shot("settings")
		h.app.Navigate(Nav{View: PageApps})
		h.settle()
		h.click("New app")
		h.tt.Click("Name")
		h.tt.Type("Kate")
		h.tt.Click("Add argument")
		h.tt.Type("kate --new")
		h.tt.Key(0, ui.KeyEnter)
		h.settle()
		shot("editor")
		h.app.ed = nil
		h.app.openHost("")
		shot("hostsheet")
		h.app.host = nil
		h.settle()
		h.app.Navigate(Nav{View: PageRunning})
		h.settle()
		h.click("Stop…")
		shot("confirm")
	}
}
