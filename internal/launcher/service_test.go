package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/instance"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

type fakeComp struct{ activated atomic.Int32 }

func (f *fakeComp) Ensure(context.Context) error { return nil }
func (f *fakeComp) Status(context.Context) (compositor.Status, error) {
	return compositor.Status{Version: "2.1.2", PID: 7, Socket: "/tmp/c.sock"}, nil
}
func (f *fakeComp) Runtime(context.Context) (compositor.Runtime, error) {
	return compositor.Runtime{XDGRuntimeDir: "/x", WaylandDisplay: "wayland-0"}, nil
}
func (f *fakeComp) Activate(context.Context, compositor.WindowHint) error {
	f.activated.Add(1)
	return nil
}

type fakeProber struct {
	check remote.Check
	err   error
}

func (f fakeProber) Probe(context.Context, string) (remote.ProbeResult, error) {
	return remote.ProbeResult{}, f.err
}
func (f fakeProber) CheckRemote(context.Context, string) (remote.Check, error) { return f.check, f.err }

type shellTransport struct{ script string }

func (shellTransport) Name() string     { return "waypipe" }
func (shellTransport) Persistent() bool { return false }
func (s shellTransport) Build(transport.Spec) (transport.Command, error) {
	return transport.Command{Path: "/bin/sh", Args: []string{"-c", s.script}, Env: os.Environ()}, nil
}
func (shellTransport) Attach(context.Context, transport.Spec) (transport.Command, error) {
	return transport.Command{}, transport.ErrUnsupported
}
func (shellTransport) Detach(context.Context, transport.Spec) error { return transport.ErrUnsupported }

type env struct {
	s    *Service
	comp *fakeComp
	p    profile.Paths
}

const twoApps = `version = 2
[[hosts]]
id = "vm"
ssh = "keiko@10.0.0.9"
[[apps]]
id = "konsole"
name = "Linux Terminal"
host = "vm"
command = ["konsole", "--separate"]
instances = "multiple"
[[apps]]
id = "foot"
name = "foot"
host = "vm"
command = ["foot"]
`

func newEnv(t *testing.T, toml, script string, prober fakeProber) *env {
	t.Helper()
	d := t.TempDir()
	p := profile.Paths{ConfigDir: filepath.Join(d, "cfg"), LegacyDir: filepath.Join(d, "legacy"), BackupDir: filepath.Join(d, "bak")}
	if toml != "" {
		os.MkdirAll(p.ConfigDir, 0o700)
		os.WriteFile(p.ProfilesTOML(), []byte(toml), 0o600)
	}
	comp := &fakeComp{}
	s := New(Config{
		Version: "test", Paths: p, LogDir: filepath.Join(d, "logs"), Compositor: comp,
		Transports: map[string]transport.Transport{"waypipe": shellTransport{script}},
		Prober:     prober, CocoaSocket: "/tmp/c.sock",
		LocalWaypipeVersion: func(context.Context) (string, string, error) { return "/opt/homebrew/bin/waypipe", "waypipe 0.10", nil },
	}, false)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.StopAll(ctx)
	})
	return &env{s, comp, p}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestOpenFocusAndNewWindow(t *testing.T) {
	e := newEnv(t, twoApps, "sleep 30", fakeProber{})
	res, err := e.s.Open("foot")
	if err != nil || res.Action != "started" {
		t.Fatalf("%+v %v", res, err)
	}
	waitFor(t, "connected", func() bool {
		for _, a := range e.s.State().Apps {
			if a.ID == "foot" && a.StatusLabel == "1 connected" {
				return true
			}
		}
		return false
	})
	// Opening a running single-instance app focuses; it never starts a second copy.
	res, err = e.s.Open("foot")
	if err != nil || res.Action != "focused" || res.Note == "" || e.comp.activated.Load() != 1 {
		t.Fatalf("%+v %v activated=%d", res, err, e.comp.activated.Load())
	}
	if _, err := e.s.NewWindow("foot"); !errors.Is(err, instance.ErrSingleInstance) {
		t.Fatalf("single-instance new window: %v", err)
	}
	// A multiple-instance app starts another with NewWindow.
	if _, err := e.s.Open("konsole"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.NewWindow("konsole"); err != nil {
		t.Fatal(err)
	}
	st := e.s.State()
	if st.Live != 3 {
		t.Fatalf("live = %d", st.Live)
	}
	for _, a := range st.Apps {
		if a.ID == "konsole" && (a.Live != 2 || a.Action != "new-window") {
			t.Fatalf("konsole = %+v", a)
		}
	}
	if _, err := e.s.Open("../../etc/passwd"); err == nil {
		t.Fatal("unknown id accepted")
	}
}

func TestDisconnectIsNeverRestartedAndStartAgainIsNew(t *testing.T) {
	e := newEnv(t, twoApps, "echo boom; exit 3", fakeProber{})
	res, _ := e.s.Open("foot")
	first := res.InstanceID
	waitFor(t, "disconnected", func() bool {
		for _, a := range e.s.State().Apps {
			if a.ID == "foot" && a.Action == "start-again" {
				return true
			}
		}
		return false
	})
	st := e.s.State()
	if len(st.Instances) != 1 || st.Instances[0].Label != "Disconnected" || !strings.Contains(st.Apps[1].StatusLabel, "exit 3") {
		t.Fatalf("%+v", st)
	}
	time.Sleep(200 * time.Millisecond)
	if len(e.s.State().Instances) != 1 {
		t.Fatal("an instance was restarted automatically")
	}
	if tail := e.s.LogTail(first); !strings.Contains(tail, "boom") {
		t.Fatalf("tail = %q", tail)
	}
	res2, err := e.s.StartAgain(first)
	if err != nil || res2.Action != "started" || res2.InstanceID == first {
		t.Fatalf("%+v %v", res2, err)
	}
	if p, err := e.s.LastLogPath("foot"); err != nil || !strings.HasSuffix(p, ".log") {
		t.Fatalf("last log %q %v", p, err)
	}
}

func TestStopDismissClear(t *testing.T) {
	e := newEnv(t, twoApps, "sleep 30", fakeProber{})
	res, _ := e.s.Open("foot")
	waitFor(t, "connected", func() bool { return e.s.State().Instances[0].State == "connected" })
	if err := e.s.Dismiss(res.InstanceID); !errors.Is(err, instance.ErrStillLive) {
		t.Fatalf("dismiss live: %v", err)
	}
	if err := e.s.Stop(res.InstanceID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped", func() bool { return e.s.State().Instances[0].Label == "Stopped" })
	if st := e.s.State(); st.Ended != 1 || st.Live != 0 {
		t.Fatalf("%+v", st)
	}
	if n := e.s.ClearEnded(); n != 1 || len(e.s.State().Instances) != 0 {
		t.Fatalf("cleared %d", n)
	}
}

func TestProbeFailureShowsFailed(t *testing.T) {
	e := newEnv(t, twoApps, "sleep 30", fakeProber{err: &remote.Error{Kind: remote.KindHostKeyUnknown, Target: "x"}})
	e.s.Open("foot")
	waitFor(t, "failed", func() bool { return e.s.State().Instances[0].State == "failed" })
	st := e.s.State()
	if st.Apps[1].StatusLabel != "Failed to start" || !strings.Contains(st.Instances[0].Detail, "host key") {
		t.Fatalf("%+v", st.Apps[1])
	}
}

func TestEditorValidationSaveAndBackup(t *testing.T) {
	e := newEnv(t, twoApps, "true", fakeProber{})
	ed := e.s.NewEdit()
	ed.ID, ed.Name, ed.Command = "bad id", "", []string{}
	ed.Env = []EnvVar{{Name: "1LANG", Value: "x"}, {Name: "OK", Value: "1"}, {Name: "OK", Value: "2"}}
	fields := map[string]bool{}
	for _, p := range e.s.ValidateEdit(ed) {
		fields[p.Field] = true
	}
	for _, want := range []string{"id", "name", "command", "env.0", "env.2"} {
		if !fields[want] {
			t.Errorf("missing problem for %s: %v", want, fields)
		}
	}
	if ps, err := e.s.SaveEdit(ed); err != nil || len(ps) == 0 {
		t.Fatalf("invalid save: %v %v", ps, err)
	}
	before, _ := os.ReadFile(e.p.ProfilesTOML())

	ed = AppEdit{IsNew: true, ID: "kate", Name: "Kate", Host: "vm", Command: []string{"kate", "-n"}, Cwd: "/home/keiko",
		Env: []EnvVar{{Name: "QT_QPA_PLATFORM", Value: "wayland"}}, Instances: "multiple"}
	if ps, err := e.s.SaveEdit(ed); err != nil || len(ps) != 0 {
		t.Fatalf("save: %v %v", ps, err)
	}
	if a := e.s.State().Apps; len(a) != 3 || a[2].ID != "kate" || a[2].Env["QT_QPA_PLATFORM"] != "wayland" {
		t.Fatalf("%+v", a)
	}
	entries, err := os.ReadDir(e.p.BackupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backup: %v %v", entries, err)
	}
	if got, _ := os.ReadFile(filepath.Join(e.p.BackupDir, entries[0].Name(), "profiles.toml")); string(got) != string(before) {
		t.Fatal("backup is not the previous file")
	}
	// Duplicate ID of a new app is refused with an id problem.
	dup := ed
	if ps, _ := e.s.SaveEdit(dup); len(ps) == 0 || ps[0].Field != "id" {
		t.Fatalf("dup: %v", ps)
	}
	// Editing keeps fields the editor does not show.
	got, _ := e.s.GetEdit("kate")
	got.Name = "Kate 2"
	if ps, err := e.s.SaveEdit(got); err != nil || len(ps) != 0 {
		t.Fatalf("edit: %v %v", ps, err)
	}
	if e.s.State().Apps[2].Name != "Kate 2" {
		t.Fatal("edit not applied")
	}
}

func TestPreviewUsesRealCommandBuilder(t *testing.T) {
	e := newEnv(t, twoApps, "true", fakeProber{})
	p := e.s.Preview(AppEdit{Host: "vm", Command: []string{"konsole", "--separate", "a b"}, Cwd: "/home/keiko",
		Env: []EnvVar{{Name: "QT_QPA_PLATFORM", Value: "wayland"}}})
	if p.Remote != "/usr/local/libexec/vm200-waypipe-app env -C /home/keiko QT_QPA_PLATFORM=wayland -- konsole --separate 'a b'" {
		t.Fatalf("remote = %q", p.Remote)
	}
	if !strings.HasPrefix(p.Local, "waypipe --no-gpu --compress=lz4") || !strings.Contains(p.Local, "keiko@10.0.0.9") {
		t.Fatalf("local = %q", p.Local)
	}
}

func TestPinRemoveAndHosts(t *testing.T) {
	e := newEnv(t, twoApps, "sleep 30", fakeProber{})
	if err := e.s.SetPinned("foot", true); err != nil || !e.s.State().Apps[1].Pinned {
		t.Fatalf("pin: %v", err)
	}
	if err := e.s.RemoveHost("vm"); err == nil {
		t.Fatal("removed a host that apps use")
	}
	e.s.Open("foot")
	waitFor(t, "connected", func() bool { return e.s.State().Live == 1 })
	if err := e.s.RemoveApp("foot"); err == nil {
		t.Fatal("removed a running app")
	}
	if err := e.s.RemoveApp("konsole"); err != nil {
		t.Fatal(err)
	}
	if ps, err := e.s.SaveHost(HostEdit{IsNew: true, ID: "build", SSH: "-oProxyCommand=x"}); err != nil || len(ps) == 0 {
		t.Fatalf("bad ssh accepted: %v %v", ps, err)
	}
	if ps, err := e.s.SaveHost(HostEdit{IsNew: true, ID: "build", SSH: "keiko@build.example.org"}); err != nil || len(ps) != 0 {
		t.Fatalf("host: %v %v", ps, err)
	}
	if h := e.s.State().Hosts; len(h) != 2 || h[1].SSH != "keiko@build.example.org" {
		t.Fatalf("%+v", h)
	}
}

func TestRunChecksHostKeyFailure(t *testing.T) {
	e := newEnv(t, twoApps, "true", fakeProber{err: &remote.Error{Kind: remote.KindHostKeyUnknown, Target: "keiko@10.0.0.9", Detail: "Host key verification failed."}})
	rep, err := e.s.RunChecks("vm")
	if err != nil || rep.Failure == nil || rep.Failure.SSHCommand != "ssh keiko@10.0.0.9" {
		t.Fatalf("%+v %v", rep, err)
	}
	if st := e.s.State(); !st.Hosts[0].Checked || st.Hosts[0].Reachable {
		t.Fatalf("%+v", st.Hosts[0])
	}
	// The ssh command never contains host-key overrides.
	if strings.Contains(rep.Failure.SSHCommand, "StrictHostKeyChecking") {
		t.Fatal("must not disable host-key checking")
	}
}

func TestRunChecksOK(t *testing.T) {
	e := newEnv(t, twoApps, "true", fakeProber{check: remote.Check{WaypipeFound: true, WrapperExecutable: false, Latency: 41 * time.Millisecond}})
	rep, err := e.s.RunChecks("vm")
	if err != nil || rep.Failure != nil {
		t.Fatalf("%+v %v", rep, err)
	}
	kinds := map[string]Kind{}
	for _, c := range rep.Checks {
		kinds[c.ID] = c.Kind
	}
	if kinds["ssh"] != KindOK || kinds["waypipe"] != KindOK || kinds["wrapper"] != KindErr || kinds["local-waypipe"] != KindOK {
		t.Fatalf("%v", kinds)
	}
	if !rep.Cocoa.Reachable || rep.Cocoa.Display != "wayland-0" {
		t.Fatalf("%+v", rep.Cocoa)
	}
}

func TestMigratesLegacyOnFirstStart(t *testing.T) {
	d := t.TempDir()
	p := profile.Paths{ConfigDir: filepath.Join(d, "cfg"), LegacyDir: filepath.Join(d, "legacy"), BackupDir: filepath.Join(d, "bak")}
	os.MkdirAll(p.LegacyDir, 0o700)
	legacy := `[{"id":"konsole","name":"Linux Terminal","target":"keiko@10.0.0.9","arguments":["konsole"]}]`
	os.WriteFile(p.ProfilesJSON(), []byte(legacy), 0o600)
	s := New(Config{Paths: p, LogDir: filepath.Join(d, "logs"), Compositor: &fakeComp{}, Prober: fakeProber{}}, true)
	st := s.State()
	if len(st.Apps) != 1 || st.Profiles.Source != "json-legacy" || !strings.Contains(st.Profiles.Notice, "left in place") {
		t.Fatalf("%+v", st.Profiles)
	}
	if got, _ := os.ReadFile(p.ProfilesJSON()); string(got) != legacy {
		t.Fatal("legacy file modified")
	}
}

func TestEmptyConfigHasNoApps(t *testing.T) {
	e := newEnv(t, "", "true", fakeProber{})
	st := e.s.State()
	if st.Profiles.Source != "none" || len(st.Apps) != 0 || st.Hosts == nil {
		t.Fatalf("%+v", st)
	}
	if ps, err := e.s.SaveHost(HostEdit{IsNew: true, ID: "vm", SSH: "keiko@10.0.0.9"}); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
	if _, err := os.Stat(e.p.ProfilesTOML()); err != nil {
		t.Fatal(err)
	}
}

func TestFocusAppAndStopApp(t *testing.T) {
	e := newEnv(t, twoApps, "sleep 30", fakeProber{})
	if err := e.s.FocusApp("konsole"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("focus of an app that is not running: %v", err)
	}
	if e.s.LiveCount("konsole") != 0 {
		t.Fatal("count")
	}
	if _, err := e.s.Open("konsole"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.NewWindow("konsole"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two connected", func() bool { return e.s.LiveCount("konsole") == 2 })

	// A Dock click focuses; it never starts an instance.
	before := e.comp.activated.Load()
	if err := e.s.FocusApp("konsole"); err != nil {
		t.Fatal(err)
	}
	if e.comp.activated.Load() != before+1 || e.s.LiveCount("konsole") != 2 {
		t.Fatalf("activated %d, live %d", e.comp.activated.Load(), e.s.LiveCount("konsole"))
	}

	// Stopping the app stops all of its instances and nothing else.
	if _, err := e.s.Open("foot"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "foot live", func() bool { return e.s.LiveCount("foot") == 1 })
	n, err := e.s.StopApp("konsole")
	if err != nil || n != 2 {
		t.Fatalf("stopped %d: %v", n, err)
	}
	waitFor(t, "konsole ended", func() bool { return e.s.LiveCount("konsole") == 0 })
	if e.s.LiveCount("foot") != 1 {
		t.Fatal("stopping konsole touched foot")
	}
	for _, in := range e.s.State().Instances {
		if in.AppID == "konsole" && in.State == "disconnected" {
			t.Fatalf("a user stop must not look like a disconnect: %+v", in)
		}
	}
}
