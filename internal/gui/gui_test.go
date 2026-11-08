package gui

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/launcher"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

type fakeComp struct{}

func (fakeComp) Ensure(context.Context) error { return nil }
func (fakeComp) Status(context.Context) (compositor.Status, error) {
	return compositor.Status{Version: "2.1.2", PID: 7, Socket: "/tmp/c.sock"}, nil
}
func (fakeComp) Runtime(context.Context) (compositor.Runtime, error) {
	return compositor.Runtime{XDGRuntimeDir: "/x", WaylandDisplay: "wayland-0"}, nil
}
func (fakeComp) Activate(context.Context, compositor.WindowHint) error { return nil }

type fakeProber struct{}

func (fakeProber) Probe(context.Context, string) (remote.ProbeResult, error) {
	return remote.ProbeResult{}, nil
}
func (fakeProber) CheckRemote(context.Context, string) (remote.Check, error) {
	return remote.Check{WaypipeFound: true, WrapperExecutable: true}, nil
}

type shellTransport struct{}

func (shellTransport) Name() string     { return "waypipe" }
func (shellTransport) Persistent() bool { return false }
func (shellTransport) Build(transport.Spec) (transport.Command, error) {
	return transport.Command{Path: "/bin/sh", Args: []string{"-c", "sleep 30"}, Env: os.Environ()}, nil
}
func (shellTransport) Attach(context.Context, transport.Spec) (transport.Command, error) {
	return transport.Command{}, transport.ErrUnsupported
}
func (shellTransport) Detach(context.Context, transport.Spec) error { return transport.ErrUnsupported }

const sample = `version = 2
[[hosts]]
id = "vm"
ssh = "keiko@10.0.0.9"
[[apps]]
id = "konsole"
name = "Linux Terminal"
host = "vm"
command = ["konsole", "--separate"]
instances = "multiple"
pinned = true
[[apps]]
id = "foot"
name = "foot"
host = "vm"
command = ["foot"]
`

// harness runs the view in a Tester and queues what the app posts to the main thread.
type harness struct {
	t   *testing.T
	app *App
	svc *launcher.Service
	tt  *ui.Tester
	mu  sync.Mutex
	q   []func()
	dir string
	cfg profile.Paths
}

func newHarness(t *testing.T, toml string, dark bool) *harness {
	t.Helper()
	d := t.TempDir()
	p := profile.Paths{ConfigDir: filepath.Join(d, "cfg"), LegacyDir: filepath.Join(d, "legacy"), BackupDir: filepath.Join(d, "bak"), CacheDir: filepath.Join(d, "cache")}
	if toml != "" {
		os.MkdirAll(p.ConfigDir, 0o700)
		os.WriteFile(p.ProfilesTOML(), []byte(toml), 0o600)
	}
	svc := launcher.New(launcher.Config{
		Version: "test", Paths: p, LogDir: filepath.Join(d, "logs"), Compositor: fakeComp{},
		Transports: map[string]transport.Transport{"waypipe": shellTransport{}},
		Prober:     fakeProber{}, CocoaSocket: "/tmp/c.sock",
		ScanHook: fakeScan, IconsHook: fakeIcons,
		LocalWaypipeVersion: func(context.Context) (string, string, error) { return "/opt/homebrew/bin/waypipe", "waypipe 0.10", nil },
	}, false)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.StopAll(ctx)
	})
	h := &harness{t: t, svc: svc, dir: d, cfg: p}
	h.app = New(svc, Options{CocoaIcons: []string{os.Getenv("WAYLAUNCHER_TEST_ICON")}})
	h.app.post = func(fn func()) { h.mu.Lock(); h.q = append(h.q, fn); h.mu.Unlock() }
	svc.Subscribe(h.app.Refresh)
	h.tt = ui.NewTesterWith(h.app.View, 1180, 760, ui.TesterOptions{Dark: dark})
	return h
}

// settle runs what the app posted, as the main loop would, and redraws.
func (h *harness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		q := h.q
		h.q = nil
		h.mu.Unlock()
		for _, fn := range q {
			fn()
		}
		h.tt.Frame()
		if len(q) == 0 || time.Now().After(deadline) {
			return
		}
	}
}

func (h *harness) waitText(s string) {
	h.t.Helper()
	for i := 0; i < 300; i++ {
		h.settle()
		if h.tt.HasText(s) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("text %q never appeared; have %q", s, h.tt.Texts())
}

func (h *harness) click(label string) {
	h.t.Helper()
	if err := h.tt.Click(label); err != nil {
		h.t.Fatalf("click %q: %v", label, err)
	}
	h.settle()
}

func TestApplicationsPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		h := newHarness(t, sample, dark)
		for _, want := range []string{"Applications", "Linux Terminal", "foot", "Running", "Discover", "Settings", "Cocoa-Way not running", "Single instance", "Multiple instances"} {
			if !h.tt.HasText(want) {
				t.Errorf("dark=%v: missing %q in %q", dark, want, h.tt.Texts())
			}
		}
		if h.tt.HasText("window open") {
			t.Error("the UI must never claim a window is open")
		}
	}
}

func TestOpenShowsConnectedNotWindow(t *testing.T) {
	h := newHarness(t, sample, false)
	h.click("Open")
	h.waitText("1 connected")
	h.click("Running")
	h.waitText("Connected")
	h.waitText("Waypipe running · window not reported")
	if !h.tt.HasText("Focus window") {
		t.Errorf("no Focus window action: %q", h.tt.Texts())
	}
}

func TestStopNeedsConfirmation(t *testing.T) {
	h := newHarness(t, sample, false)
	h.click("Open")
	h.waitText("1 connected")
	h.click("Running")
	h.waitText("Selected instance")
	// The footer's Stop… asks first and stops nothing by itself.
	h.click("Stop…")
	h.settle()
	if !h.svc.HasLive() {
		t.Fatal("Stop… stopped the instance without confirmation")
	}
	if !h.tt.HasText("This ends konsole on vm. Unsaved work in it will be lost. It won’t be restarted.") {
		t.Errorf("confirmation text missing: %q", h.tt.Texts())
	}
	h.click("Cancel")
	if !h.svc.HasLive() {
		t.Fatal("Cancel stopped the instance")
	}
	h.click("Stop…")
	h.click("Stop")
	for i := 0; i < 300 && h.svc.HasLive(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if h.svc.HasLive() {
		t.Fatal("confirmed Stop did not stop the instance")
	}
}

func TestEditorValidatesAndSaves(t *testing.T) {
	h := newHarness(t, sample, false)
	h.click("New app")
	if !h.tt.HasText("New application") {
		t.Fatalf("editor not open: %q", h.tt.Texts())
	}
	// Nothing is shown as an error before the user touched a field, but Save is blocked.
	if !h.tt.HasText("to fix before saving") {
		t.Errorf("no problem count: %q", h.tt.Texts())
	}
	if err := h.tt.Click("Name"); err != nil {
		t.Fatal(err)
	}
	h.tt.Type("My Editor")
	h.settle()
	if err := h.tt.Click("ID"); err != nil {
		t.Fatal(err)
	}
	h.tt.Type("myed")
	h.settle()
	if err := h.tt.Click("Add argument"); err != nil {
		t.Fatal(err)
	}
	h.tt.Type("kate --new ")
	h.settle()
	if got := strings.Join(h.app.ed.m.Command, "|"); got != "kate|--new" {
		t.Fatalf("command = %q", got)
	}
	if !h.tt.HasText("waylauncher://launch/myed. Letters, digits and “-”.") {
		t.Errorf("ID help missing: %q", h.tt.Texts())
	}
	if !strings.Contains(h.app.ed.preview.Remote, "kate") {
		t.Errorf("preview = %+v", h.app.ed.preview)
	}
	if len(h.app.ed.problems) != 0 {
		t.Fatalf("problems: %+v", h.app.ed.problems)
	}
	h.click("Save")
	h.waitText("Added My Editor.")
	if h.app.ed != nil {
		t.Error("editor still open after save")
	}
	data, err := os.ReadFile(h.cfg.ProfilesTOML())
	if err != nil || !strings.Contains(string(data), `id = "myed"`) {
		t.Fatalf("not saved: %v %s", err, data)
	}
}

func TestDuplicateEnvShowsInlineError(t *testing.T) {
	h := newHarness(t, sample, false)
	h.app.openEditor("foot")
	h.settle()
	e := h.app.ed
	e.m.Env = []launcher.EnvVar{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}}
	h.settle()
	if !h.tt.HasText("Duplicate variable A.") {
		t.Errorf("no inline env error: %q", h.tt.Texts())
	}
	if !h.tt.HasText("1 problem to fix before saving") {
		t.Errorf("save not blocked: %q", h.tt.Texts())
	}
}

func TestSettingsPageSaves(t *testing.T) {
	h := newHarness(t, sample, false)
	h.click("Settings")
	for _, want := range []string{"Theme", "Glass", "Show in menu bar", "Show running Linux apps in the Dock", "Quit right away (default)", "Ask first", "Notify when an app disconnects", "Keep logs per app"} {
		if !h.tt.HasText(want) {
			t.Errorf("missing %q", want)
		}
	}
	if !h.svc.Settings().ShowRunningInDock {
		t.Error("Dock icons should default to on")
	}
	h.click("Ask first")
	for i := 0; i < 200 && h.svc.Settings().QuitBehavior != "ask"; i++ {
		h.settle()
		time.Sleep(10 * time.Millisecond)
	}
	if h.svc.Settings().QuitBehavior != "ask" {
		t.Error("quit behaviour not saved")
	}
}

func TestEmptyStateWithoutHosts(t *testing.T) {
	h := newHarness(t, "", false)
	if !h.tt.HasText("Add a host to get started") {
		t.Errorf("texts: %q", h.tt.Texts())
	}
	h.click("Add host…")
	if !h.tt.HasText("The machine your applications run on") {
		t.Errorf("host sheet missing: %q", h.tt.Texts())
	}
}

func TestNavigateFromOutside(t *testing.T) {
	h := newHarness(t, sample, false)
	h.app.Navigate(Nav{View: PageHost})
	h.settle()
	if !h.tt.HasText("Run checks") || !h.tt.HasText("Connection checks") {
		t.Errorf("host page: %q", h.tt.Texts())
	}
	h.app.Navigate(Nav{View: "new-app"})
	h.settle()
	if !h.tt.HasText("New application") {
		t.Errorf("editor: %q", h.tt.Texts())
	}
}

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		`konsole --separate`: {"konsole", "--separate"},
		`sh -c "echo hi"`:    {"sh", "-c", "echo hi"},
		`a\ b 'c d'`:         {"a b", "c d"},
		`""`:                 {""},
	}
	for in, want := range cases {
		got, _ := splitArgs(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
	if _, pending := splitArgs(`"open`); !pending {
		t.Error("unclosed quote not reported")
	}
}

func TestCommandChipsEditAndRemove(t *testing.T) {
	h := newHarness(t, sample, false)
	h.app.openEditor("konsole")
	h.settle()
	e := h.app.ed
	if got := strings.Join(e.m.Command, "|"); got != "konsole|--separate" {
		t.Fatalf("command = %q", got)
	}
	h.click("Remove argument --separate")
	if got := strings.Join(e.m.Command, "|"); got != "konsole" {
		t.Fatalf("after remove: %q", got)
	}
	h.click("Edit argument konsole")
	if len(e.m.Command) != 0 || e.draft != "konsole" {
		t.Fatalf("edit: command %q draft %q", e.m.Command, e.draft)
	}
	if len(e.problems) == 0 {
		t.Error("an empty command must be a problem")
	}
}

func TestNoAutoRestartWording(t *testing.T) {
	h := newHarness(t, sample, false)
	h.click("Running")
	for _, bad := range []string{"Reconnect", "Restarting", "Retrying"} {
		if h.tt.HasText(bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
	if !h.tt.HasText("Nothing is running. Open an application from Applications.") {
		t.Errorf("empty running view: %q", h.tt.Texts())
	}
}

func TestCocoaIconLoadsAtRuntimeOrFallsBack(t *testing.T) {
	h := newHarness(t, sample, false)
	h.app.iconPaths = []string{"", filepath.Join(h.dir, "missing.png")}
	h.app.iconLoaded = false
	h.tt.Frame()
	if h.app.cocoaIcon != nil {
		t.Fatal("a missing icon must fall back to the status dot")
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	f, err := os.Create(filepath.Join(h.dir, "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
	h.app.iconPaths = []string{filepath.Join(h.dir, "icon.png")}
	h.app.iconLoaded = false
	h.tt.Frame()
	if h.app.cocoaIcon == nil {
		t.Fatal("icon not loaded from a configured path")
	}
	// A file that is not an image is skipped, not fatal.
	os.WriteFile(filepath.Join(h.dir, "bad.png"), []byte("nope"), 0o600)
	h.app.iconPaths = []string{filepath.Join(h.dir, "bad.png")}
	h.app.iconLoaded, h.app.cocoaIcon = false, nil
	h.tt.Frame()
	if h.app.cocoaIcon != nil {
		t.Fatal("garbage decoded as an icon")
	}
}
