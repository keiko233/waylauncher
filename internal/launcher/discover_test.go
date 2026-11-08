package launcher

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keiko233/waylauncher/internal/discovery"
	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/transport"
)

func testPNG(c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			img.Set(i, j, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// scanStream frames desktop files the way the remote scan helper does.
func scanStream(files map[string]string, order ...string) []byte {
	var b strings.Builder
	b.WriteString("\x01WLENV KDE\n")
	for _, id := range order {
		fmt.Fprintf(&b, "\x01WLD 1 %d %s\n%s\n", len(files[id]), id, files[id])
	}
	fmt.Fprintf(&b, "\x01WLEND %d\n", len(order))
	return []byte(b.String())
}

const (
	konsoleDesktop = "[Desktop Entry]\nType=Application\nName=Konsole\nName[zh_CN]=终端\nComment=Terminal\nExec=konsole --separate %U\nIcon=konsole\nPath=/home/keiko\n"
	kateDesktop    = "[Desktop Entry]\nType=Application\nName=Kate\nExec=kate %F\nIcon=kate\n"
)

type discEnv struct {
	s    *Service
	p    profile.Paths
	mu   sync.Mutex
	args [][]string // every ssh call
	out  []byte
	res  execx.Result
}

func newDiscEnv(t *testing.T, toml string) *discEnv {
	t.Helper()
	d := t.TempDir()
	e := &discEnv{p: profile.Paths{ConfigDir: filepath.Join(d, "cfg"), LegacyDir: filepath.Join(d, "legacy"),
		BackupDir: filepath.Join(d, "bak"), CacheDir: filepath.Join(d, "cache")}}
	if toml != "" {
		os.MkdirAll(e.p.ConfigDir, 0o700)
		os.WriteFile(e.p.ProfilesTOML(), []byte(toml), 0o600)
	}
	e.res = execx.Result{Stdout: scanStream(map[string]string{"org.kde.konsole": konsoleDesktop, "org.kde.kate": kateDesktop},
		"org.kde.konsole", "org.kde.kate")}
	e.s = New(Config{
		Version: "test", Paths: e.p, LogDir: filepath.Join(d, "logs"), Compositor: &fakeComp{},
		Transports: map[string]transport.Transport{"waypipe": shellTransport{"true"}},
		Prober:     fakeProber{}, CocoaSocket: "/tmp/c.sock", SSHPath: "/fake/ssh",
		Exec: func(ctx context.Context, name string, args ...string) (execx.Result, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.args = append(e.args, append([]string{name}, args...))
			return e.res, nil
		},
		IconsHook: func(_ context.Context, _ discovery.Options, entries []discovery.Entry) error {
			for i := range entries {
				if entries[i].ID == "org.kde.konsole" {
					entries[i].IconPNG = testPNG(color.NRGBA{200, 30, 30, 255})
				}
			}
			return nil
		},
	}, false)
	return e
}

func (e *discEnv) scan(t *testing.T) Discovery {
	t.Helper()
	if err := e.s.ScanHost(context.Background(), "vm"); err != nil {
		t.Fatal(err)
	}
	return e.s.DiscoveryOf("vm")
}

func TestScanListsEntriesReadOnly(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	before, _ := os.ReadFile(e.p.ProfilesTOML())
	d := e.scan(t)
	if !d.Scanned || len(d.Entries) != 2 || d.Entries[0].Name != "Kate" || d.Entries[1].Name != "Konsole" {
		t.Fatalf("%+v", d)
	}
	k := d.Entries[1]
	if strings.Join(k.Command, " ") != "konsole --separate" || k.Cwd != "/home/keiko" || k.Imported {
		t.Fatalf("%+v", k)
	}
	// One ssh call, BatchMode, to the host's own target; nothing else ran.
	if len(e.args) != 1 || e.args[0][0] != "/fake/ssh" || !strings.Contains(strings.Join(e.args[0], " "), "BatchMode=yes") ||
		!strings.Contains(strings.Join(e.args[0], " "), "keiko@10.0.0.9") {
		t.Fatalf("%q", e.args)
	}
	// A scan changes no file and starts no instance.
	after, _ := os.ReadFile(e.p.ProfilesTOML())
	if !bytes.Equal(before, after) || len(e.s.State().Instances) != 0 {
		t.Fatal("scan was not read-only")
	}
	if _, err := os.Stat(e.p.BackupDir); err == nil {
		t.Fatal("scan created a backup")
	}
}

func TestScanUsesLanguageSetting(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	st := e.s.Settings()
	st.Language = "zh-CN"
	if err := e.s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	d := e.scan(t)
	found := false
	for _, x := range d.Entries {
		found = found || x.Name == "终端"
	}
	if !found {
		t.Fatalf("name not localized: %+v", d.Entries)
	}
}

func TestScanHostKeyFailureHasCopyCommand(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	e.res = execx.Result{ExitCode: 255, Stderr: []byte("Host key verification failed.\n")}
	err := e.s.ScanHost(context.Background(), "vm")
	if err == nil {
		t.Fatal("expected an error")
	}
	d := e.s.DiscoveryOf("vm")
	if d.Scanned || d.Failure == nil || d.Failure.SSHCommand != "ssh keiko@10.0.0.9" || d.Error == "" {
		t.Fatalf("%+v", d)
	}
	if strings.Contains(d.Failure.SSHCommand, "StrictHostKeyChecking") {
		t.Fatal("must not disable host-key checking")
	}
	// A later success clears the failure.
	e.res = execx.Result{Stdout: scanStream(map[string]string{"org.kde.kate": kateDesktop}, "org.kde.kate")}
	if d := e.scan(t); d.Failure != nil || d.Error != "" || len(d.Entries) != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestFailedRescanKeepsEntries(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	e.scan(t)
	e.res = execx.Result{ExitCode: 255, Stderr: []byte("ssh: connect to host x port 22: Connection refused\n")}
	if err := e.s.ScanHost(context.Background(), "vm"); err == nil {
		t.Fatal("expected an error")
	}
	d := e.s.DiscoveryOf("vm")
	if len(d.Entries) != 2 || d.Error == "" || d.Failure == nil {
		t.Fatalf("%+v", d)
	}
}

func TestCancelledScanChangesNothing(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	e.scan(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.res = execx.Result{Stdout: scanStream(map[string]string{})}
	if err := e.s.ScanHost(ctx, "vm"); err == nil {
		t.Fatal("expected the context error")
	}
	if d := e.s.DiscoveryOf("vm"); len(d.Entries) != 2 || d.Error != "" {
		t.Fatalf("a cancelled scan changed the result: %+v", d)
	}
}

func TestScanUnknownHost(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	if err := e.s.ScanHost(context.Background(), "nope"); err == nil || len(e.args) != 0 {
		t.Fatalf("%v %v", err, e.args)
	}
}

func TestImportCreatesProfilesWithCachedIcon(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	e.scan(t)
	if err := e.s.LoadHostIcons(context.Background(), "vm"); err != nil {
		t.Fatal(err)
	}
	d := e.s.DiscoveryOf("vm")
	if d.Entries[1].Icon == "" || d.Entries[0].Icon != "" {
		t.Fatalf("icons: %+v", d.Entries)
	}
	res, err := e.s.ImportDiscovered("vm", []string{"org.kde.konsole"})
	if err != nil || len(res.Imported) != 1 || res.Imported[0] != "org-kde-konsole" {
		t.Fatalf("%+v %v", res, err)
	}
	f, _, err := profile.Parse(mustRead(t, e.p.ProfilesTOML()))
	if err != nil {
		t.Fatal(err)
	}
	a := f.AppByID("org-kde-konsole")
	if a == nil || a.Source != "discovered:org.kde.konsole" || a.Name != "Konsole" || a.Host != "vm" ||
		strings.Join(a.Command, " ") != "konsole --separate" || a.Cwd != "/home/keiko" || a.Instances != "single" {
		t.Fatalf("%+v", a)
	}
	if !strings.HasPrefix(a.Icon, e.p.CacheDir+string(filepath.Separator)+"vm"+string(filepath.Separator)) || !strings.HasSuffix(a.Icon, ".png") {
		t.Fatalf("icon %q is not under the cache dir", a.Icon)
	}
	if got := mustRead(t, a.Icon); !bytes.Equal(got, testPNG(color.NRGBA{200, 30, 30, 255})) {
		t.Fatal("cached icon differs from the scanned one")
	}
	if fi, _ := os.Stat(a.Icon); fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode %v", fi.Mode())
	}
	// The window state and the Dock input (State().Apps[].Icon) carry the same path.
	var info *AppInfo
	for _, ap := range e.s.State().Apps {
		if ap.ID == "org-kde-konsole" {
			ap := ap
			info = &ap
		}
	}
	if info == nil || info.Icon != a.Icon {
		t.Fatalf("%+v", info)
	}
	// The previous profiles.toml was backed up before the save.
	bak, _ := filepath.Glob(filepath.Join(e.p.BackupDir, "*", "profiles.toml"))
	if len(bak) != 1 {
		t.Fatalf("backups: %v", bak)
	}
	// The untouched entry stays unimported.
	if d := e.s.DiscoveryOf("vm"); !d.Entries[1].Imported || d.Entries[1].ImportedAs != "org-kde-konsole" || d.Entries[0].Imported {
		t.Fatalf("%+v", d.Entries)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRescanMarksImportedAndNeverOverwritesEdits(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	e.scan(t)
	if _, err := e.s.ImportDiscovered("vm", []string{"org.kde.konsole"}); err != nil {
		t.Fatal(err)
	}
	// The user edits the imported profile.
	ed, err := e.s.GetEdit("org-kde-konsole")
	if err != nil {
		t.Fatal(err)
	}
	ed.Name, ed.Command, ed.Instances = "My Konsole", []string{"konsole", "--profile", "mine"}, "multiple"
	if p, err := e.s.SaveEdit(ed); err != nil || len(p) > 0 {
		t.Fatalf("%v %v", p, err)
	}
	// The remote entry changes, and a re-scan runs.
	e.res = execx.Result{Stdout: scanStream(map[string]string{
		"org.kde.konsole": strings.Replace(konsoleDesktop, "Name=Konsole", "Name=Konsole 2", 1), "org.kde.kate": kateDesktop,
	}, "org.kde.konsole", "org.kde.kate")}
	d := e.scan(t)
	if !d.Entries[1].Imported || d.Entries[0].Imported {
		t.Fatalf("imported marks: %+v", d.Entries)
	}
	before := mustRead(t, e.p.ProfilesTOML())
	res, err := e.s.ImportDiscovered("vm", []string{"org.kde.konsole"})
	if err != nil || len(res.Imported) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if !bytes.Equal(before, mustRead(t, e.p.ProfilesTOML())) {
		t.Fatal("re-import rewrote the file")
	}
	for _, a := range e.s.State().Apps {
		if a.ID == "org-kde-konsole" && (a.Name != "My Konsole" || a.CommandText != "konsole --profile mine" || a.Instances != "multiple") {
			t.Fatalf("user edits were overwritten: %+v", a)
		}
	}
	// A new application can still be imported afterwards.
	if res, err := e.s.ImportDiscovered("vm", []string{"org.kde.kate", "org.kde.kate", "missing.id"}); err != nil || len(res.Imported) != 1 || len(res.Skipped) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestImportEditKeepsCachedIconUnchanged(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	e.scan(t)
	e.s.LoadHostIcons(context.Background(), "vm")
	e.s.ImportDiscovered("vm", []string{"org.kde.konsole"})
	icon := e.s.State().Apps[2].Icon
	if icon == "" {
		t.Fatal("no icon")
	}
	ed, _ := e.s.GetEdit("org-kde-konsole")
	if ed.Icon != icon {
		t.Fatalf("editor icon %q, want %q", ed.Icon, icon)
	}
	ed.Name = "Renamed"
	if p, err := e.s.SaveEdit(ed); err != nil || len(p) > 0 {
		t.Fatalf("%v %v", p, err)
	}
	if got := e.s.State().Apps[2].Icon; got != icon {
		t.Fatalf("icon changed to %q", got)
	}
}

func TestImportGivesUniqueProfileIDs(t *testing.T) {
	// A user profile already uses the ID the import would pick (any case).
	e := newDiscEnv(t, twoApps+"\n[[apps]]\nid = \"Org-Kde-Konsole\"\nname = \"Mine\"\nhost = \"vm\"\ncommand = [\"x\"]\n")
	e.scan(t)
	res, err := e.s.ImportDiscovered("vm", []string{"org.kde.konsole"})
	if err != nil || len(res.Imported) != 1 || res.Imported[0] != "org-kde-konsole-2" {
		t.Fatalf("%+v %v", res, err)
	}
	if !e.s.HasApp("Org-Kde-Konsole") || !e.s.HasApp("org-kde-konsole-2") {
		t.Fatal("profiles missing")
	}
}

func TestImportWithoutScanOrCacheDir(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	if res, err := e.s.ImportDiscovered("vm", []string{"org.kde.konsole"}); err != nil || len(res.Imported) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// Without a cache directory the icon is skipped, the import still works.
	e.s.cfg.Paths.CacheDir = ""
	e.scan(t)
	err := e.s.LoadHostIcons(context.Background(), "vm")
	if err == nil || e.s.DiscoveryOf("vm").IconError == "" {
		t.Fatalf("missing cache dir not reported: %v", err)
	}
	res, err := e.s.ImportDiscovered("vm", []string{"org.kde.konsole"})
	if err != nil || len(res.Imported) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, a := range e.s.State().Apps {
		if a.ID == "org-kde-konsole" && a.Icon != "" {
			t.Fatalf("icon %q without a cache", a.Icon)
		}
	}
}

func TestStaleScanIsDropped(t *testing.T) {
	e := newDiscEnv(t, twoApps)
	release := make(chan struct{})
	started := make(chan struct{})
	first := true
	e.s.cfg.ScanHook = func(ctx context.Context, opt discovery.Options) ([]discovery.Entry, error) {
		if first {
			first = false
			close(started)
			<-release
			return []discovery.Entry{{ID: "old", Name: "Old", Command: []string{"old"}}}, nil
		}
		return []discovery.Entry{{ID: "new", Name: "New", Command: []string{"new"}}}, nil
	}
	done := make(chan error, 1)
	go func() { done <- e.s.ScanHost(context.Background(), "vm") }()
	<-started
	if err := e.s.ScanHost(context.Background(), "vm"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("first scan never returned")
	}
	if d := e.s.DiscoveryOf("vm"); len(d.Entries) != 1 || d.Entries[0].ID != "new" {
		t.Fatalf("%+v", d.Entries)
	}
}
