package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
)

const konsole = `# comment
[Desktop Entry]
Type=Application
Name=Konsole
Name[zh_CN]=终端
Name[de]=Konsole DE
Comment=Terminal emulator
Exec=konsole %U
Icon=utilities-terminal
Categories=Qt;KDE;System;TerminalEmulator;
Terminal=false

[Desktop Action NewWindow]
Name=New Window
Exec=konsole --new-tab
`

func entry(t *testing.T, content, id string, opt Options) (Entry, bool) {
	t.Helper()
	return entryFrom(id, parseDesktopFile([]byte(content)), opt)
}

func TestParseBasicAndLocalized(t *testing.T) {
	e, ok := entry(t, konsole, "org.kde.konsole", Options{})
	if !ok || e.Name != "Konsole" || e.Comment != "Terminal emulator" || e.Icon != "utilities-terminal" {
		t.Fatalf("%+v %v", e, ok)
	}
	if !reflect.DeepEqual(e.Command, []string{"konsole"}) {
		t.Fatalf("command %q", e.Command)
	}
	if !reflect.DeepEqual(e.Categories, []string{"Qt", "KDE", "System", "TerminalEmulator"}) {
		t.Fatalf("categories %q", e.Categories)
	}
	e, _ = entry(t, konsole, "k", Options{Locales: LocalesFor("zh-CN")})
	if e.Name != "终端" {
		t.Fatalf("zh name %q", e.Name)
	}
	e, _ = entry(t, konsole, "k", Options{Locales: []string{"zh_TW", "zh"}})
	if e.Name != "Konsole" { // no zh_TW/zh key: falls back to Name
		t.Fatalf("fallback %q", e.Name)
	}
	e, _ = entry(t, konsole, "k", Options{Locales: []string{"de_AT.UTF-8@euro"}})
	if e.Name != "Konsole DE" {
		t.Fatalf("de variant %q", e.Name)
	}
	if LocalesFor("system") != nil || !reflect.DeepEqual(LocalesFor("zh-TW"), []string{"zh_TW", "zh"}) {
		t.Fatal("LocalesFor")
	}
}

func TestSkipRules(t *testing.T) {
	base := "[Desktop Entry]\nName=A\nExec=a\nType=Application\n"
	cases := map[string]string{
		"nodisplay": base + "NoDisplay=true\n",
		"hidden":    base + "Hidden=true\n",
		"link":      strings.Replace(base, "Type=Application", "Type=Link", 1),
		"dir":       strings.Replace(base, "Type=Application", "Type=Directory", 1),
		"noexec":    "[Desktop Entry]\nName=A\nType=Application\n",
		"noname":    "[Desktop Entry]\nExec=a\nType=Application\n",
		"onlyshow":  base + "OnlyShowIn=GNOME;KDE;\n",
		"notshow":   base + "NotShowIn=KDE;\n",
	}
	for name, c := range cases {
		opt := Options{}
		if name == "notshow" {
			opt.Desktops = []string{"kde"}
		}
		if _, ok := entry(t, c, "x", opt); ok {
			t.Errorf("%s should be skipped", name)
		}
	}
	// Positive cases.
	if _, ok := entry(t, base+"NoDisplay=false\nNotShowIn=KDE;\n", "x", Options{}); !ok {
		t.Error("NotShowIn on a generic session shows the entry")
	}
	if _, ok := entry(t, base+"OnlyShowIn=GNOME;KDE;\n", "x", Options{Desktops: []string{"KDE"}}); !ok {
		t.Error("OnlyShowIn matching the session shows the entry")
	}
	if _, ok := entry(t, base+"OnlyShowIn=GNOME;\n", "x", Options{Desktops: []string{"KDE"}}); ok {
		t.Error("OnlyShowIn not matching hides the entry")
	}
}

func TestExecParsing(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"konsole %U", []string{"konsole"}},
		{"app --file=%f -x %F %i %c %k", []string{"app", "--file=", "-x"}},
		{`env FOO=bar app %u`, []string{"env", "FOO=bar", "app"}},
		{`"/opt/My App/run" --name "a b" %f`, []string{"/opt/My App/run", "--name", "a b"}},
		{`app 100%% done`, []string{"app", "100%", "done"}},
		{`app "" x`, []string{"app", "", "x"}},
		{`app "%f"`, []string{"app", ""}},
		{"app\\sarg", []string{"app", "arg"}}, // string unescape runs first, so \s is a separator
		{`  spaced   args  `, []string{"spaced", "args"}},
		{`app $HOME;rm -rf x`, []string{"app", "$HOME;rm", "-rf", "x"}}, // never a shell
	}
	for _, c := range cases {
		if c.want == nil {
			continue
		}
		if got := parseExec(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseExec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Escapes inside quotes: \" \` \$ \\ (after the string-level unescape,
	// so the file spells the backslash four times for one backslash).
	got := parseExec(`sh -c "echo \\"hi\\" \\$HOME \\\\"`)
	want := []string{"sh", "-c", `echo "hi" $HOME \`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("quoted escapes = %q, want %q", got, want)
	}
}

func TestTerminalPathAndWaylandOnly(t *testing.T) {
	e, ok := entry(t, "[Desktop Entry]\nType=Application\nName=Htop\nExec=htop\nTerminal=true\nPath=/var/tmp\n", "htop", Options{})
	if !ok || !e.Terminal || e.Cwd != "/var/tmp" || !reflect.DeepEqual(e.Command, []string{"htop"}) {
		t.Fatalf("%+v", e)
	}
	e, _ = entry(t, "[Desktop Entry]\nType=Application\nName=H\nExec=h\nPath=relative\n", "h", Options{})
	if e.Cwd != "" {
		t.Fatal("relative Path must be ignored")
	}
	e, _ = entry(t, "[Desktop Entry]\nType=Application\nName=Foot\nExec=/usr/bin/foot\n", "foot", Options{})
	if !e.WaylandOnly {
		t.Fatal("foot is wayland only")
	}
	if e, _ = entry(t, konsole, "k", Options{}); e.WaylandOnly {
		t.Fatal("konsole is not")
	}
}

func TestProfileIDs(t *testing.T) {
	files := []rawFile{
		{ID: "org.kde.konsole", OK: true, Content: []byte(konsole)},
		{ID: "org-kde.konsole", OK: true, Content: []byte(konsole)},
		{ID: "org.kde-konsole", OK: true, Content: []byte(konsole)},
		{ID: "weird_id+x@y", OK: true, Content: []byte(konsole)},
		{ID: "...", OK: true, Content: []byte(konsole)},
	}
	es := buildEntries(files, Options{}, "")
	seen := map[string]bool{}
	for _, e := range es {
		if !profile.ValidID(e.ProfileID) {
			t.Errorf("invalid profile id %q", e.ProfileID)
		}
		if seen[e.ProfileID] {
			t.Errorf("duplicate profile id %q", e.ProfileID)
		}
		seen[e.ProfileID] = true
	}
	if len(es) != 5 {
		t.Fatalf("%d entries", len(es))
	}
	if !seen["org-kde-konsole"] || !seen["weird-id-x-y"] || !seen["app"] {
		t.Errorf("ids %v", seen)
	}
}

func TestBuildEntriesOverrideAndShadow(t *testing.T) {
	user := "[Desktop Entry]\nType=Application\nName=Mine\nExec=mine\n"
	sys := "[Desktop Entry]\nType=Application\nName=System\nExec=sys\n"
	hidden := "[Desktop Entry]\nType=Application\nName=H\nExec=h\nNoDisplay=true\n"
	files := []rawFile{
		{ID: "a", OK: true, Content: []byte(user)},
		{ID: "a", OK: true, Content: []byte(sys)}, // later dir: ignored
		{ID: "b", OK: true, Content: []byte(hidden)},
		{ID: "b", OK: true, Content: []byte(sys)}, // user hid it: stays hidden
		{ID: "c", OK: false},                      // TryExec failed: shadows, not listed
		{ID: "c", OK: true, Content: []byte(sys)},
	}
	es := buildEntries(files, Options{}, "")
	if len(es) != 1 || es[0].Name != "Mine" {
		t.Fatalf("%+v", es)
	}
}

func TestRemoteDesktopUsed(t *testing.T) {
	c := "[Desktop Entry]\nType=Application\nName=K\nExec=k\nOnlyShowIn=KDE;\n"
	files := []rawFile{{ID: "k", OK: true, Content: []byte(c)}}
	if n := len(buildEntries(files, Options{}, "")); n != 0 {
		t.Fatal("generic session hides OnlyShowIn")
	}
	if n := len(buildEntries(files, Options{}, "KDE")); n != 1 {
		t.Fatal("remote desktop should match")
	}
	if n := len(buildEntries(files, Options{Desktops: []string{"GNOME"}}, "KDE")); n != 0 {
		t.Fatal("explicit desktops win")
	}
}

func frame(ok int, id, content string) string {
	return fmt.Sprintf("\x01WLD %d %d %s\n%s\n", ok, len(content), id, content)
}

func TestParseScanStream(t *testing.T) {
	// Content may contain anything, including text that looks like a frame.
	evil := "[Desktop Entry]\n\x01WLEND 0\n\x01WLD 1 5 x\nName=E\n"
	out := "Welcome banner\n\x01WLENV KDE\n" + frame(1, "a", konsole) + frame(0, "b", "") + frame(1, "evil", evil) + "\x01WLEND 3\n"
	files, desk, err := parseScanStream([]byte(out))
	if err != nil || desk != "KDE" || len(files) != 3 {
		t.Fatalf("%v %q %d", err, desk, len(files))
	}
	if string(files[0].Content) != konsole || files[1].OK || string(files[2].Content) != evil {
		t.Fatal("content mismatch")
	}
	for _, bad := range []string{
		"\x01WLENV \n" + frame(1, "a", konsole), // no end marker
		"\x01WLENV \n\x01WLD 1 9999 a\nshort\n\x01WLEND 1\n",
		"\x01WLD 1 x a\n\x01WLEND 1\n",
		"",
	} {
		if _, _, err := parseScanStream([]byte(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

// fakeSSH returns an Exec that records the call and answers with out.
func fakeSSH(out string, code int, calls *[][]string) execx.Exec {
	return func(ctx context.Context, name string, args ...string) (execx.Result, error) {
		if calls != nil {
			*calls = append(*calls, append([]string{name}, args...))
		}
		res := execx.Result{Stdout: []byte(out), ExitCode: code}
		if code != 0 {
			res.Stderr = []byte("Host key verification failed.")
		}
		return res, nil
	}
}

func TestScanWithFakeExec(t *testing.T) {
	var calls [][]string
	out := "\x01WLENV \n" + frame(1, "org.kde.konsole", konsole) + frame(1, "b", "[Desktop Entry]\nType=Application\nName=Alpha\nExec=alpha\n") + "\x01WLEND 2\n"
	es, err := Scan(context.Background(), Options{Target: "vm", Exec: fakeSSH(out, 0, &calls), Locales: LocalesFor("zh-CN")})
	if err != nil || len(es) != 2 {
		t.Fatalf("%v %+v", err, es)
	}
	if es[0].Name != "Alpha" || es[1].Name != "终端" { // sorted by name
		t.Fatalf("order %q %q", es[0].Name, es[1].Name)
	}
	if len(calls) != 1 {
		t.Fatalf("%d ssh calls", len(calls))
	}
	c := calls[0]
	if c[0] != "/usr/bin/ssh" || !strings.Contains(strings.Join(c, " "), "BatchMode=yes") || c[len(c)-2] != "vm" ||
		!strings.HasPrefix(c[len(c)-1], "sh -c ") {
		t.Fatalf("ssh args %q", c)
	}
	// Errors are classified, bad targets rejected.
	_, err = Scan(context.Background(), Options{Target: "vm", Exec: fakeSSH("", 255, nil)})
	if remote.KindOf(err) != remote.KindHostKeyUnknown {
		t.Fatalf("err %v", err)
	}
	if _, err = Scan(context.Background(), Options{Target: "-oProxyCommand=x", Exec: fakeSSH("", 0, nil)}); err == nil {
		t.Fatal("option-like target must be rejected")
	}
}

func hasSh() bool { _, err := exec.LookPath("sh"); return err == nil }

// localExec runs the remote command string with a local sh under env, so the
// real helper scripts are exercised without ssh.
func localExec(env []string) execx.Exec {
	return func(ctx context.Context, name string, args ...string) (execx.Result, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", args[len(args)-1])
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		res := execx.Result{Stdout: o.Bytes(), Stderr: e.Bytes()}
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestRealScriptsLocally runs the remote scripts under a local sh on a fake
// home: override order, TryExec, size cap, symlinks, subdirectories, icon
// candidates, fetching, SVG rasterising and the cache.
func TestRealScriptsLocally(t *testing.T) {
	if !hasSh() {
		t.Skip("no sh")
	}
	root := t.TempDir()
	home, sys, sys2 := filepath.Join(root, "home"), filepath.Join(root, "sys"), filepath.Join(root, "sys2")
	app := func(name, extra string) string {
		return "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=" + strings.ToLower(name) + "\n" + extra
	}
	write(t, filepath.Join(home, ".local/share/applications/over.desktop"), app("UserOver", ""))
	write(t, filepath.Join(sys, "applications/over.desktop"), app("SysOver", ""))
	write(t, filepath.Join(sys, "applications/plain.desktop"), app("Plain", "Icon=plainicon\n"))
	write(t, filepath.Join(sys, "applications/okexec.desktop"), app("OkExec", "TryExec=sh\n"))
	write(t, filepath.Join(sys, "applications/badexec.desktop"), app("BadExec", "TryExec=definitely-not-installed-xyz\n"))
	write(t, filepath.Join(sys, "applications/badabs.desktop"), app("BadAbs", "TryExec=/no/such/bin\n"))
	write(t, filepath.Join(sys, "applications/huge.desktop"), app("Huge", "Comment="+strings.Repeat("x", 70000)+"\n"))
	write(t, filepath.Join(sys, "applications/sub/dir.desktop"), app("Sub", "Icon=/abs/icon.png\n"))
	write(t, filepath.Join(sys, "applications/not a name.desktop"), app("Spaced", ""))
	write(t, filepath.Join(sys2, "applications/second.desktop"), app("Second", ""))
	write(t, filepath.Join(sys2, "applications/plain.desktop"), app("PlainShadowed", ""))
	write(t, filepath.Join(root, "real/linked.desktop"), app("Linked", ""))
	if err := os.Symlink(filepath.Join(root, "real/linked.desktop"), filepath.Join(sys, "applications/linked.desktop")); err != nil {
		t.Fatal(err)
	}
	// Icons.
	small := testPNG(t, 48, 48, color.NRGBA{255, 0, 0, 255})
	big := testPNG(t, 256, 256, color.NRGBA{0, 255, 0, 255})
	write(t, filepath.Join(sys, "icons/hicolor/48x48/apps/plainicon.png"), string(small))
	write(t, filepath.Join(sys, "icons/hicolor/256x256/apps/plainicon.png"), string(big))
	write(t, filepath.Join(sys, "icons/hicolor/scalable/apps/plainicon.svg"), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#00f"/></svg>`)
	abs := filepath.Join(root, "abs/icon.png")
	write(t, abs, string(small))

	// The abs path above stands in for /abs/icon.png; point the entry at it.
	env := []string{"HOME=" + home, "XDG_DATA_DIRS=" + sys + ":" + sys2}
	opt := Options{Target: "vm", Exec: localExec(env)}
	es, err := Scan(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]Entry{}
	for _, e := range es {
		names[e.ID] = e
	}
	for _, id := range []string{"over", "plain", "okexec", "sub-dir", "second", "linked"} {
		if _, ok := names[id]; !ok {
			t.Errorf("missing %s (have %v)", id, keys(names))
		}
	}
	for _, id := range []string{"badexec", "badabs", "huge", "not a name"} {
		if _, ok := names[id]; ok {
			t.Errorf("%s must be skipped", id)
		}
	}
	if names["over"].Name != "UserOver" || names["plain"].Name != "Plain" {
		t.Errorf("override order: %q %q", names["over"].Name, names["plain"].Name)
	}

	// Icons: the 256 PNG beats the 48 PNG and the SVG.
	for i := range es {
		if es[i].ID == "sub-dir" {
			es[i].Icon = abs
		}
	}
	if err := LoadIcons(context.Background(), opt, es); err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		switch e.ID {
		case "plain":
			img, err := png.Decode(bytes.NewReader(e.IconPNG))
			if err != nil || img.Bounds().Dx() != IconSize || img.Bounds().Dy() != IconSize {
				t.Fatalf("plain icon: %v", err)
			}
			if r, g, _, _ := img.At(256, 256).RGBA(); r != 0 || g == 0 {
				t.Errorf("expected the green 256 icon, got r=%d g=%d", r, g)
			}
		case "sub-dir":
			if len(e.IconPNG) == 0 {
				t.Error("absolute icon not loaded")
			}
		case "over":
			if e.IconPNG != nil {
				t.Error("no icon key, no icon")
			}
		}
	}

	// Nothing was written anywhere under the fake remote tree by scanning.
	c := Cache{Dir: filepath.Join(root, "cache")}
	var plain Entry
	for _, e := range es {
		if e.ID == "plain" {
			plain = e
		}
	}
	p, err := c.Store("host1", plain.IconPNG)
	if err != nil || !strings.HasPrefix(p, filepath.Join(root, "cache", "host1")) {
		t.Fatalf("%v %s", err, p)
	}
}

func keys(m map[string]Entry) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

func TestSVGOnlyIconRasterised(t *testing.T) {
	if !hasSh() {
		t.Skip("no sh")
	}
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	write(t, filepath.Join(sys, "icons/hicolor/scalable/apps/vec.svg"), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#f00"/></svg>`)
	write(t, filepath.Join(sys, "icons/hicolor/32x32/apps/vec.png"), string(testPNG(t, 32, 32, color.NRGBA{0, 0, 255, 255})))
	opt := Options{Target: "vm", Exec: localExec([]string{"HOME=" + root, "XDG_DATA_DIRS=" + sys})}
	es := []Entry{{ID: "v", Icon: "vec"}}
	if err := LoadIcons(context.Background(), opt, es); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(es[0].IconPNG))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, b, a := img.At(256, 256).RGBA(); r == 0 || b != 0 || a == 0 {
		t.Errorf("expected red from the SVG, got r=%d b=%d a=%d", r, b, a)
	}
}

func TestParseIconPath(t *testing.T) {
	cases := []struct {
		p    string
		kind iconKind
		size int
		ok   bool
	}{
		{"/usr/share/icons/hicolor/48x48/apps/a.png", kindPNG, 48, true},
		{"/usr/share/icons/hicolor/scalable/apps/a.svg", kindSVG, 0, true},
		{"/usr/share/icons/breeze/apps/64/a.png", kindPNG, 64, true},
		{"/usr/share/icons/Papirus/48x48@2/apps/a.png", kindPNG, 96, true},
		{"/usr/share/pixmaps/a.png", kindPNG, 0, true},
		{"/usr/share/pixmaps/a.xpm", 0, 0, false},
	}
	for _, c := range cases {
		got, ok := parseIconPath(c.p)
		if ok != c.ok || (ok && (got.Kind != c.kind || got.Size != c.size)) {
			t.Errorf("%s -> %+v %v", c.p, got, ok)
		}
	}
}

func TestChooseIcon(t *testing.T) {
	mk := func(ps ...string) []iconCand {
		var cs []iconCand
		for _, p := range ps {
			c, _ := parseIconPath(p)
			cs = append(cs, c)
		}
		return cs
	}
	pick := func(svgOK bool, ps ...string) string {
		c, ok := chooseIcon(mk(ps...), svgOK)
		if !ok {
			return ""
		}
		return c.Path
	}
	p := func(s string) string { return "/i/hicolor/" + s }
	// Largest PNG not above 512.
	if got := pick(true, p("48x48/apps/a.png"), p("256x256/apps/a.png"), p("128x128/apps/a.png"), p("scalable/apps/a.svg")); got != p("256x256/apps/a.png") {
		t.Errorf("got %s", got)
	}
	if got := pick(true, p("1024x1024/apps/a.png"), p("256x256/apps/a.png"), p("512x512/apps/a.png")); got != p("512x512/apps/a.png") {
		t.Errorf("got %s", got)
	}
	// Only above 512: the smallest of those.
	if got := pick(true, p("2048x2048/apps/a.png"), p("1024x1024/apps/a.png")); got != p("1024x1024/apps/a.png") {
		t.Errorf("got %s", got)
	}
	// Only small PNGs: SVG wins if supported, else the largest small PNG.
	if got := pick(true, p("48x48/apps/a.png"), p("scalable/apps/a.svg")); got != p("scalable/apps/a.svg") {
		t.Errorf("got %s", got)
	}
	if got := pick(false, p("32x32/apps/a.png"), p("48x48/apps/a.png"), p("scalable/apps/a.svg")); got != p("48x48/apps/a.png") {
		t.Errorf("got %s", got)
	}
	if got := pick(false, p("scalable/apps/a.svg")); got != "" {
		t.Errorf("svg without support: %s", got)
	}
	if got := pick(true); got != "" {
		t.Errorf("empty: %s", got)
	}
	if got := pick(true, "/usr/share/pixmaps/a.png"); got != "/usr/share/pixmaps/a.png" {
		t.Errorf("pixmap: %s", got)
	}
}

func TestParseCandidatesAndFetch(t *testing.T) {
	out := "\x01WLC 1 /i/hicolor/48x48/apps/a.png\n\x01WLC 1 /i/hicolor/48x48/apps/a.png\n\x01WLC 2 /i/pixmaps/b.xpm\n\x01WLC 9 /x/c.png\n\x01WLC x /y.png\ngarbage\n\x01WLC 2 relative.png\n"
	got := parseCandidates([]byte(out), 2)
	if len(got) != 1 || len(got[1]) != 1 {
		t.Fatalf("%+v", got)
	}
	data := []byte("hello")
	b := base64.StdEncoding.EncodeToString(data)
	fo := fmt.Sprintf("noise\x01WLI 1 ok %d\n%s\n\x01WLI 2 big 0\n\n\x01WLI 3 missing 0\n\n\x01WLI 4 ok 4\n!!!!\n", len(b), b)
	r, err := parseFetch([]byte(fo))
	if err != nil || string(r[1].data) != "hello" || r[2].status != "big" || r[3].status != "missing" || r[4].status != "missing" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := parseFetch([]byte("\x01WLI 1 ok 100\nabc\n")); err == nil {
		t.Fatal("truncated output must error")
	}
}

func TestIconName(t *testing.T) {
	for k, ok := range map[string]bool{"firefox": true, "org.kde.konsole": true, "a-b_c": true, "foo.png": true,
		"../etc/passwd": false, "a b": false, "a;b": false, "$(x)": false, "-rf": false, "": false} {
		if _, got := iconName(k); got != ok {
			t.Errorf("iconName(%q) = %v", k, got)
		}
	}
}

func TestCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	c := Cache{Dir: dir}
	data := testPNG(t, 8, 8, color.NRGBA{1, 2, 3, 255})
	p, err := c.Store("host-1", data)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Join(dir, "host-1") || !strings.HasSuffix(p, ".png") || len(filepath.Base(p)) != 16+4 {
		t.Fatalf("path %s", p)
	}
	st, _ := os.Stat(p)
	dst, _ := os.Stat(filepath.Dir(p))
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
		t.Errorf("modes %v %v", st.Mode().Perm(), dst.Mode().Perm())
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, data) {
		t.Fatal("content")
	}
	p2, err := c.Store("host-1", data)
	if err != nil || p2 != p {
		t.Fatalf("idempotent: %v %s", err, p2)
	}
	other, _ := c.Store("host-1", append([]byte{}, data[:len(data)-1]...))
	if other == p {
		t.Fatal("different bytes need a different file")
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp %s", e.Name())
		}
	}
	for _, bad := range []string{"", "..", "a/b", "../x", "a b"} {
		if _, err := c.Store(bad, data); err == nil {
			t.Errorf("host id %q accepted", bad)
		}
	}
	if _, err := c.Store("h", nil); err == nil {
		t.Error("empty icon accepted")
	}
}
