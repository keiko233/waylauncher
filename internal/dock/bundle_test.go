package dock

import (
	"bytes"
	"encoding/binary"
	"github.com/keiko233/waylauncher/internal/discovery"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type bundleEnv struct {
	cfg  BundleConfig
	dir  string
	bak  string
	sign []string // bundles signed, in order
}

func newBundleEnv(t *testing.T) *bundleEnv {
	t.Helper()
	root := t.TempDir()
	e := &bundleEnv{dir: filepath.Join(root, "Waylauncher Apps"), bak: filepath.Join(root, "Backups")}
	helper := filepath.Join(root, "helper-bin")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	n := 0
	e.cfg = BundleConfig{
		Dir: e.dir, BackupDir: e.bak, HelperBinary: helper, IconBase: root, Version: "9.9",
		WaylauncherApp: "/Applications/Waylauncher.app", Socket: "/var/t/waylauncher/helper.sock",
		Sign: func(b string) error { e.sign = append(e.sign, b); return nil },
		Now:  func() time.Time { n++; return time.Date(2026, 1, 2, 3, 4, 5, n*1e6, time.UTC) },
	}
	return e
}

func konsole() AppSpec {
	return AppSpec{ID: "konsole", Name: "Linux Terminal", Glyph: ">_", Tile: "#23262E"}
}
func foot() AppSpec { return AppSpec{ID: "foot", Name: "foot", Glyph: "Fo", Tile: "#1D5FD1"} }

func list(t *testing.T, dir string) []string {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestSyncCreatesBundleWithInfoPlistAndIcon(t *testing.T) {
	e := newBundleEnv(t)
	res, err := SyncBundles(e.cfg, []AppSpec{konsole()})
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(e.dir, "Linux Terminal.app")
	if res.Paths["konsole"] != b || len(res.Written) != 1 || len(e.sign) != 1 || filepath.Base(e.sign[0]) != "Linux Terminal.app" {
		t.Fatalf("%+v signed=%v", res, e.sign)
	}
	plist, err := os.ReadFile(filepath.Join(b, "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>CFBundleIdentifier</key>\n\t<string>com.keiko233.waylauncher.app.konsole</string>",
		"<key>CFBundleName</key>\n\t<string>Linux Terminal</string>",
		"<key>CFBundleExecutable</key>\n\t<string>waylauncher-dock-helper</string>",
		"<key>CFBundleIconFile</key>\n\t<string>AppIcon</string>",
		"<key>CFBundlePackageType</key>\n\t<string>APPL</string>",
		"<key>LSMultipleInstancesProhibited</key>\n\t<true/>",
		"<key>WaylauncherAppID</key>\n\t<string>konsole</string>",
		"<key>WaylauncherApp</key>\n\t<string>/Applications/Waylauncher.app</string>",
		"<key>WaylauncherSocket</key>\n\t<string>/var/t/waylauncher/helper.sock</string>",
	} {
		if !strings.Contains(string(plist), want) {
			t.Errorf("Info.plist lacks %q\n%s", want, plist)
		}
	}
	// A regular (Dock-visible) app: neither agent nor background-only.
	if strings.Contains(string(plist), "LSUIElement") || strings.Contains(string(plist), "LSBackgroundOnly") {
		t.Error("bundle would not show in the Dock")
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("plutil", "-lint", filepath.Join(b, "Contents", "Info.plist")).CombinedOutput(); err != nil {
			t.Errorf("plutil: %s", out)
		}
	}
	exe := filepath.Join(b, "Contents", "MacOS", HelperExecutable)
	if fi, err := os.Stat(exe); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable: %v %v", fi, err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "#!/bin/sh\nexit 0\n" {
		t.Error("executable is not a copy of the helper")
	}
	icns, err := os.ReadFile(filepath.Join(b, "Contents", "Resources", "AppIcon.icns"))
	if err != nil || string(icns[:4]) != "icns" {
		t.Fatalf("icns: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, ".staging-*")); len(left) != 0 {
		t.Errorf("staging left behind: %v", left)
	}
	info, err := ReadBundleInfo(exe)
	if err != nil || info.AppID != "konsole" || info.Name != "Linux Terminal" || info.WaylauncherApp != "/Applications/Waylauncher.app" || info.Socket != "/var/t/waylauncher/helper.sock" {
		t.Fatalf("ReadBundleInfo: %+v %v", info, err)
	}
}

func TestSyncIsIdempotentAndBacksUpOnChange(t *testing.T) {
	e := newBundleEnv(t)
	if _, err := SyncBundles(e.cfg, []AppSpec{konsole(), foot()}); err != nil {
		t.Fatal(err)
	}
	e.sign = nil
	res, err := SyncBundles(e.cfg, []AppSpec{konsole(), foot()})
	if err != nil || len(res.Written) != 0 || len(res.Kept) != 2 || len(res.BackedUp) != 0 || len(e.sign) != 0 {
		t.Fatalf("second sync rebuilt: %+v %v signed=%v", res, err, e.sign)
	}
	if _, err := os.Stat(e.bak); err == nil {
		t.Fatal("an unchanged sync created a backup")
	}

	// Changing the generated icon colour regenerates and backs up the old bundle.
	f2 := foot()
	f2.Tile = "#A44A00"
	res, err = SyncBundles(e.cfg, []AppSpec{konsole(), f2})
	if err != nil || len(res.Written) != 1 || len(res.BackedUp) != 1 || len(res.Kept) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	old := res.BackedUp[0]
	if !strings.HasPrefix(old, e.bak) || !strings.Contains(old, BundleSubdir) {
		t.Fatalf("backup location %s", old)
	}
	oldPlist, err := os.ReadFile(filepath.Join(old, "Contents", "Info.plist"))
	if err != nil || !strings.Contains(string(oldPlist), BundleIDPrefix+"foot") {
		t.Fatalf("backup is not the old foot bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "foot.app", "Contents", "Info.plist")); err != nil {
		t.Fatal("new bundle missing")
	}
}

func TestSyncRenameAndRemoveMoveBundlesToBackup(t *testing.T) {
	e := newBundleEnv(t)
	SyncBundles(e.cfg, []AppSpec{konsole(), foot()})

	k := konsole()
	k.Name = "Konsole"
	res, err := SyncBundles(e.cfg, []AppSpec{k, foot()})
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, e.dir); strings.Join(got, ",") != "Konsole.app,foot.app" {
		t.Fatalf("after rename: %v", got)
	}
	if len(res.BackedUp) != 1 || filepath.Base(res.BackedUp[0]) != "Linux Terminal.app" {
		t.Fatalf("renamed bundle not backed up: %v", res.BackedUp)
	}

	// Removing an app moves its bundle out of the way, it is not deleted.
	res, err = SyncBundles(e.cfg, []AppSpec{k})
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, e.dir); strings.Join(got, ",") != "Konsole.app" {
		t.Fatalf("after remove: %v", got)
	}
	if len(res.BackedUp) != 1 || filepath.Base(res.BackedUp[0]) != "foot.app" {
		t.Fatalf("%v", res.BackedUp)
	}
	if _, err := os.Stat(filepath.Join(res.BackedUp[0], "Contents", "MacOS", HelperExecutable)); err != nil {
		t.Fatalf("backed-up bundle is not intact: %v", err)
	}
}

func TestSyncLeavesForeignBundlesAndBacksUpNameClashes(t *testing.T) {
	e := newBundleEnv(t)
	os.MkdirAll(e.dir, 0o755)
	mk := func(name, id string) {
		p := filepath.Join(e.dir, name, "Contents")
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "Info.plist"), []byte(`<plist><dict><key>CFBundleIdentifier</key><string>`+id+`</string></dict></plist>`), 0o644)
	}
	mk("Mine.app", "org.example.mine")
	mk("foot.app", "org.example.other-foot") // sits on a name we need
	os.WriteFile(filepath.Join(e.dir, "notes.txt"), []byte("x"), 0o644)

	res, err := SyncBundles(e.cfg, []AppSpec{foot()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.BackedUp) != 1 {
		t.Fatalf("%v", res.BackedUp)
	}
	data, _ := os.ReadFile(filepath.Join(res.BackedUp[0], "Contents", "Info.plist"))
	if !strings.Contains(string(data), "org.example.other-foot") {
		t.Fatal("the clashing foreign bundle was not the one backed up")
	}
	got := list(t, e.dir)
	if strings.Join(got, ",") != "Mine.app,foot.app,notes.txt" {
		t.Fatalf("%v", got)
	}
	// Foreign bundles survive further syncs (including removal of all apps).
	if _, err := SyncBundles(e.cfg, nil); err != nil {
		t.Fatal(err)
	}
	if got := list(t, e.dir); strings.Join(got, ",") != "Mine.app,notes.txt" {
		t.Fatalf("after clearing: %v", got)
	}
}

func TestBundleNames(t *testing.T) {
	names := BundleNames([]AppSpec{
		{ID: "a", Name: "Text Editor"}, {ID: "b", Name: "text editor"}, // clash, case-insensitively
		{ID: "c", Name: "a/b:c"}, {ID: "d", Name: ".hidden"}, {ID: "e", Name: "   "},
	})
	want := map[string]string{"a": "Text Editor.app", "b": "text editor (b).app", "c": "a-b-c.app", "d": "hidden.app", "e": "e.app"}
	for id, w := range want {
		if names[id] != w {
			t.Errorf("%s: %q, want %q", id, names[id], w)
		}
	}
}

func TestProfileIconPNGAndFallback(t *testing.T) {
	e := newBundleEnv(t)
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.NRGBA{200, 30, 30, 255})
		}
	}
	var pb bytes.Buffer
	png.Encode(&pb, img)
	iconPath := filepath.Join(e.cfg.IconBase, "k.png")
	os.WriteFile(iconPath, pb.Bytes(), 0o644)

	withIcon := konsole()
	withIcon.Icon = iconPath
	broken := foot()
	broken.Icon = filepath.Join(e.cfg.IconBase, "missing.png")
	res, err := SyncBundles(e.cfg, []AppSpec{withIcon, broken})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "foot") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	read := func(app string) []byte {
		b, err := os.ReadFile(filepath.Join(res.Paths[app], "Contents", "Resources", "AppIcon.icns"))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// The ic07 chunk (128 px) of the profile icon is its red pixel colour.
	chunk := func(icns []byte, tag string) image.Image {
		for off := 8; off < len(icns); {
			l := int(binary.BigEndian.Uint32(icns[off+4 : off+8]))
			if string(icns[off:off+4]) == tag {
				im, err := png.Decode(bytes.NewReader(icns[off+8 : off+l]))
				if err != nil {
					t.Fatal(err)
				}
				return im
			}
			off += l
		}
		t.Fatalf("no %s chunk", tag)
		return nil
	}
	r, g, _, _ := chunk(read("konsole"), "ic07").At(64, 64).RGBA()
	if r < 0xc000 || g > 0x4000 {
		t.Errorf("profile icon not used: r=%x g=%x", r, g)
	}
	// A relative path resolves against IconBase and changing the file regenerates.
	withIcon.Icon = "k.png"
	res2, _ := SyncBundles(e.cfg, []AppSpec{withIcon, broken})
	if len(res2.Written) != 0 && len(res2.Written) != 1 { // path text is not part of the fingerprint
		t.Fatalf("%+v", res2)
	}
	os.WriteFile(iconPath, append([]byte{}, pb.Bytes()...), 0o644)
	img.Set(0, 0, color.NRGBA{0, 0, 255, 255})
	pb.Reset()
	png.Encode(&pb, img)
	os.WriteFile(iconPath, pb.Bytes(), 0o644)
	res3, _ := SyncBundles(e.cfg, []AppSpec{withIcon, broken})
	found := false
	for _, w := range res3.Written {
		found = found || strings.HasSuffix(w, "Linux Terminal.app")
	}
	if !found {
		t.Fatalf("a changed icon file did not regenerate the bundle: %+v", res3)
	}
}

func TestSyncSignFailureKeepsOldBundle(t *testing.T) {
	e := newBundleEnv(t)
	SyncBundles(e.cfg, []AppSpec{foot()})
	e.cfg.Sign = func(string) error { return os.ErrPermission }
	f := foot()
	f.Tile = "#A44A00"
	res, err := SyncBundles(e.cfg, []AppSpec{f})
	if err == nil {
		t.Fatal("sign failure not reported")
	}
	if len(res.BackedUp) != 0 {
		t.Fatalf("old bundle moved although the new one failed: %v", res.BackedUp)
	}
	if res.Paths["foot"] == "" {
		t.Fatal("old bundle path lost")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "foot.app", "Contents", "Info.plist")); err != nil {
		t.Fatal("old bundle gone")
	}
}

// An icon cached by Discover (a PNG under the cache directory, recorded as
// the profile's icon path) becomes the bundle's .icns unchanged; an app
// without a usable icon keeps the generated glyph tile that the window draws
// from the same Glyph/Tile values.
func TestCachedDiscoverIconBuildsICNS(t *testing.T) {
	e := newBundleEnv(t)
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			img.Set(x, y, color.NRGBA{20, 40, 220, 255})
		}
	}
	var pb bytes.Buffer
	png.Encode(&pb, img)
	cache := discovery.Cache{Dir: filepath.Join(t.TempDir(), "Caches", "Waylauncher")}
	cached, err := cache.Store("vm", pb.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	imp := AppSpec{ID: "org-kde-kate", Name: "Kate", Glyph: "Ka", Tile: "#1D5FD1", Icon: cached}
	plain := AppSpec{ID: "org-x", Name: "X", Glyph: "X", Tile: "#B0306A"}
	res, err := SyncBundles(e.cfg, []AppSpec{imp, plain})
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("%v %v", err, res.Warnings)
	}
	icns := func(app string) []byte {
		b, err := os.ReadFile(filepath.Join(res.Paths[app], "Contents", "Resources", "AppIcon.icns"))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	px := func(b []byte) (r, g, bl uint32) {
		for off := 8; off < len(b); {
			l := int(binary.BigEndian.Uint32(b[off+4 : off+8]))
			if string(b[off:off+4]) == "ic07" {
				im, err := png.Decode(bytes.NewReader(b[off+8 : off+l]))
				if err != nil {
					t.Fatal(err)
				}
				r, g, bl, _ = im.At(64, 64).RGBA()
				return
			}
			off += l
		}
		t.Fatal("no ic07 chunk")
		return
	}
	if r, g, b := px(icns("org-kde-kate")); b < 0xc000 || r > 0x4000 || g > 0x6000 {
		t.Errorf("cached PNG not used: %x %x %x", r, g, b)
	}
	// The fallback is the tile in the colour the window uses for the same app.
	if r, _, b := px(icns("org-x")); r < 0x9000 || b < 0x4000 || r < b {
		t.Errorf("fallback tile colour unexpected: r=%x b=%x", r, b)
	}
}
