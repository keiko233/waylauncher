package gui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keiko233/waylauncher/internal/discovery"
	"github.com/keiko233/waylauncher/internal/remote"
)

var (
	scanFail  error         // set by a test to make the next scans fail
	scanBlock chan struct{} // non-nil: scans wait for the context to end
)

func fakeScan(ctx context.Context, _ discovery.Options) ([]discovery.Entry, error) {
	if scanBlock != nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if scanFail != nil {
		return nil, scanFail
	}
	return []discovery.Entry{
		{ID: "org.kde.kate", ProfileID: "org-kde-kate", Name: "Kate", Comment: "Advanced text editor", Command: []string{"kate"}, Icon: "kate"},
		{ID: "org.kde.konsole", ProfileID: "org-kde-konsole", Name: "Konsole", Comment: "Terminal", Command: []string{"konsole", "--separate"}, Icon: "konsole", Cwd: "/home/keiko"},
		{ID: "org.example.Wl", ProfileID: "org-example-Wl", Name: "Wayland Thing", Command: []string{"wl-thing"}, WaylandOnly: true, Terminal: true},
	}, nil
}

func tilePNG(c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func fakeIcons(_ context.Context, _ discovery.Options, entries []discovery.Entry) error {
	for i := range entries {
		if entries[i].ID == "org.kde.kate" {
			entries[i].IconPNG = tilePNG(color.NRGBA{230, 60, 60, 255})
		}
	}
	return nil
}

func TestDiscoverPageScansOnOpenAndImports(t *testing.T) {
	scanFail = nil
	for _, dark := range []bool{false, true} {
		h := newHarness(t, sample, dark)
		h.click("Discover")
		h.waitText("Kate")
		for _, want := range []string{"Konsole", "Advanced text editor", "Wayland only", "Needs a terminal", "Import selected", "0 selected", "Scan again", "Read-only. "} {
			if !h.tt.HasText(want) && !h.tt.HasText(strings.TrimSpace(want)) {
				t.Errorf("dark=%v: missing %q in %q", dark, want, h.tt.Texts())
			}
		}
		if h.tt.HasText("Coming soon") {
			t.Error("Coming soon is still shown")
		}
		// Icons arrive after the entries and are cached.
		for i := 0; i < 300 && h.app.scanOf("vm").iconsBusy; i++ {
			h.settle()
		}
		if d := h.svc.DiscoveryOf("vm"); d.Entries[0].Icon == "" || !strings.HasPrefix(d.Entries[0].Icon, h.cfg.CacheDir) {
			t.Fatalf("icon not cached: %+v", d.Entries[0])
		}
		if h.app.profileIcon(h.svc.DiscoveryOf("vm").Entries[0].Icon) == nil {
			t.Error("cached icon does not load in the window")
		}
		h.click("Select Kate")
		h.settle()
		if !h.tt.HasText("1 selected") {
			t.Fatalf("selection: %q", h.tt.Texts())
		}
		h.click("Import selected")
		h.waitText("Imported 1 application.")
		h.waitText("Imported")
		data, _ := os.ReadFile(h.cfg.ProfilesTOML())
		if !strings.Contains(string(data), `source = "discovered:org.kde.kate"`) || !strings.Contains(string(data), h.cfg.CacheDir) {
			t.Fatalf("profile not saved with source and cached icon:\n%s", data)
		}
		// The Applications card draws the cached icon, not the glyph tile.
		h.click("Applications")
		var kate string
		for _, ap := range h.svc.State().Apps {
			if ap.ID == "org-kde-kate" {
				kate = ap.Icon
			}
		}
		if kate == "" || h.app.profileIcon(kate) == nil {
			t.Fatalf("imported app has no loadable icon: %q", kate)
		}
	}
}

func TestDiscoverScanFailureOffersCopySSH(t *testing.T) {
	scanFail = &remote.Error{Kind: remote.KindHostKeyUnknown, Target: "keiko@10.0.0.9", Detail: "Host key verification failed."}
	defer func() { scanFail = nil }()
	h := newHarness(t, sample, false)
	h.click("Discover")
	h.waitText("Copy ssh command")
	if !h.tt.HasText("Scan") {
		t.Errorf("no Scan button: %q", h.tt.Texts())
	}
	// Only the page's first opening scans by itself.
	scanFail = errors.New("second")
	h.app.Navigate(Nav{View: PageApps})
	h.settle()
	h.app.Navigate(Nav{View: PageDiscover})
	h.settle()
	if e := h.svc.DiscoveryOf("vm").Error; strings.Contains(e, "second") {
		t.Error("reopening the page scanned again")
	}
}

func TestDiscoverWithoutHostsAndCancel(t *testing.T) {
	h := newHarness(t, "", false)
	h.app.Navigate(Nav{View: PageDiscover})
	h.settle()
	if !h.tt.HasText("Add a host to discover applications") {
		t.Errorf("%q", h.tt.Texts())
	}
}

func TestDiscoverCancelLeavesPageUsable(t *testing.T) {
	scanBlock = make(chan struct{})
	defer func() { scanBlock = nil }()
	h := newHarness(t, sample, false)
	h.click("Discover")
	h.waitText("Scanning vm…")
	before, _ := os.ReadFile(h.cfg.ProfilesTOML())
	h.click("Cancel")
	h.waitText("Scan cancelled")
	if d := h.svc.DiscoveryOf("vm"); d.Scanned || len(d.Entries) != 0 || d.Error != "" {
		t.Fatalf("%+v", d)
	}
	after, _ := os.ReadFile(h.cfg.ProfilesTOML())
	if !bytes.Equal(before, after) {
		t.Fatal("a cancelled scan changed the profiles")
	}
	// Scan can be started again.
	scanBlock = nil
	h.click("Scan")
	h.waitText("Kate")
}

func TestProfileIconFallsBackAndReloads(t *testing.T) {
	h := newHarness(t, sample, false)
	h.tt.Frame()
	a := h.app
	dir := t.TempDir()
	good := filepath.Join(dir, "a.png")
	os.WriteFile(good, tilePNG(color.NRGBA{1, 2, 3, 255}), 0o600)
	if a.profileIcon(good) == nil {
		t.Fatal("PNG not loaded")
	}
	for name, ic := range map[string]string{"missing": filepath.Join(dir, "none.png"), "empty": ""} {
		if a.profileIcon(ic) != nil {
			t.Errorf("%s: expected the glyph fallback", name)
		}
	}
	bad := filepath.Join(dir, "bad.png")
	os.WriteFile(bad, []byte("not a png"), 0o600)
	if a.profileIcon(bad) != nil {
		t.Error("garbage decoded")
	}
	a.icons.base = dir
	if a.profileIcon("a.png") == nil {
		t.Error("relative path not resolved against the base")
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(home); err == nil {
		// "~/" expands to the home directory (missing file there: fallback).
		if a.profileIcon("~/definitely-not-here-waylauncher.png") != nil {
			t.Error("~ path")
		}
	}
}
