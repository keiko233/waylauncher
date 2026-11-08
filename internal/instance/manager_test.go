package instance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

type fakeComp struct {
	ensureErr error
	ensures   atomic.Int32
	activated atomic.Int32
}

func (f *fakeComp) Ensure(context.Context) error { f.ensures.Add(1); return f.ensureErr }
func (f *fakeComp) Status(context.Context) (compositor.Status, error) {
	return compositor.Status{}, nil
}
func (f *fakeComp) Runtime(context.Context) (compositor.Runtime, error) {
	return compositor.Runtime{XDGRuntimeDir: "/x", WaylandDisplay: "wayland-0"}, nil
}
func (f *fakeComp) Activate(context.Context, compositor.WindowHint) error {
	f.activated.Add(1)
	return nil
}

// fakeProber returns errs[i] for call i (nil once exhausted), or blocks until
// ctx is done when block is set.
type fakeProber struct {
	errs  []error
	block bool
	calls atomic.Int32
}

func (f *fakeProber) Probe(ctx context.Context, _ string) (remote.ProbeResult, error) {
	n := int(f.calls.Add(1)) - 1
	if f.block {
		<-ctx.Done()
		return remote.ProbeResult{}, ctx.Err()
	}
	if n < len(f.errs) {
		return remote.ProbeResult{}, f.errs[n]
	}
	return remote.ProbeResult{}, nil
}

// shellTransport runs the script with /bin/sh in place of waypipe.
type shellTransport struct {
	script string
	builds atomic.Int32
}

func (s *shellTransport) Name() string     { return "waypipe" }
func (s *shellTransport) Persistent() bool { return false }
func (s *shellTransport) Build(transport.Spec) (transport.Command, error) {
	s.builds.Add(1)
	return transport.Command{Path: "/bin/sh", Args: []string{"-c", s.script}, Env: os.Environ()}, nil
}
func (s *shellTransport) Attach(context.Context, transport.Spec) (transport.Command, error) {
	return transport.Command{}, transport.ErrUnsupported
}
func (s *shellTransport) Detach(context.Context, transport.Spec) error {
	return transport.ErrUnsupported
}

type harness struct {
	m        *Manager
	comp     *fakeComp
	prober   *fakeProber
	tr       *shellTransport
	settings *profile.Settings
	logDir   string

	mu     sync.Mutex
	events []Event
}

func newHarness(t *testing.T, script string) *harness {
	t.Helper()
	h := &harness{comp: &fakeComp{}, prober: &fakeProber{}, tr: &shellTransport{script: script}}
	s := profile.DefaultSettings()
	h.settings = &s
	h.logDir = filepath.Join(t.TempDir(), "logs")
	h.m = New(Config{
		Compositor:  h.comp,
		Transports:  map[string]transport.Transport{"waypipe": h.tr},
		Prober:      h.prober,
		Settings:    func() profile.Settings { return *h.settings },
		Logs:        &LogStore{Dir: h.logDir},
		RetryDelay:  time.Millisecond,
		StopTimeout: 150 * time.Millisecond,
	})
	h.m.Subscribe(func(e Event) {
		h.mu.Lock()
		h.events = append(h.events, e)
		h.mu.Unlock()
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.m.StopAll(ctx)
	})
	return h
}

func (h *harness) states(id string) []State {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []State
	for _, e := range h.events {
		if e.Instance.ID == id {
			out = append(out, e.Instance.State)
		}
	}
	return out
}

func app(id, instances string) (profile.App, profile.Host) {
	return profile.App{ID: id, Name: id, Host: "h", Command: []string{"x"}, Instances: instances,
			Transport: profile.Transport{MacOS: "waypipe"}},
		profile.Host{ID: "h", SSH: "u@h"}
}

func (h *harness) launchAndWait(t *testing.T, script string) Snapshot {
	t.Helper()
	h.tr.script = script
	a, host := app("konsole", "single")
	res, err := h.m.Launch(a, host)
	if err != nil || res.Action != ActionStarted {
		t.Fatalf("launch: %+v %v", res, err)
	}
	return h.wait(t, res.Instance.ID)
}

func (h *harness) wait(t *testing.T, id string) Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := h.m.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestNormalExitAndEvents(t *testing.T) {
	h := newHarness(t, "")
	s := h.launchAndWait(t, "exit 0")
	if s.State != Exited || s.ExitCode == nil || *s.ExitCode != 0 || s.EndedAt == nil {
		t.Fatalf("%+v", s)
	}
	waitFor(t, "events", func() bool { return len(h.states(s.ID)) == 3 })
	got := h.states(s.ID)
	if got[0] != Connecting || got[1] != Connected || got[2] != Exited {
		t.Fatalf("states = %v", got)
	}
	if !strings.HasPrefix(s.ID, "konsole-") || len(s.ID) != len("konsole-")+4 {
		t.Fatalf("id = %s", s.ID)
	}
}

func TestAbnormalExitIsDisconnectedAndNeverRestarted(t *testing.T) {
	h := newHarness(t, "")
	s := h.launchAndWait(t, "echo boom >&2; exit 3")
	if s.State != Disconnected || *s.ExitCode != 3 || !strings.Contains(s.Reason, "3") {
		t.Fatalf("%+v", s)
	}
	time.Sleep(200 * time.Millisecond)
	if n := h.tr.builds.Load(); n != 1 {
		t.Fatalf("transport started %d times; remote apps must never restart automatically", n)
	}
	if h.prober.calls.Load() != 1 {
		t.Fatal("probe must not repeat after launch")
	}
	b, _ := os.ReadFile(s.LogPath)
	if !strings.Contains(string(b), "boom") {
		t.Fatalf("log = %q", b)
	}
}

func TestNoChildProcessesSetting(t *testing.T) {
	script := `cat testdata/waypipe-window-close.log >&2; exit 1`
	for _, tc := range []struct {
		treat bool
		want  State
	}{{true, Exited}, {false, Disconnected}} {
		h := newHarness(t, "")
		h.settings.TreatNoChildProcessesAsNormal = tc.treat
		if s := h.launchAndWait(t, script); s.State != tc.want {
			t.Errorf("treat=%v: state %s (%s), want %s", tc.treat, s.State, s.Reason, tc.want)
		}
	}
	// Same exit code without the marker is a disconnect even when the setting is on.
	h := newHarness(t, "")
	if s := h.launchAndWait(t, `cat testdata/waypipe-connection-lost.log >&2; exit 1`); s.State != Disconnected {
		t.Errorf("connection-lost fixture: %s", s.State)
	}
}

func TestLogFilePermissions(t *testing.T) {
	h := newHarness(t, "")
	s := h.launchAndWait(t, "exit 0")
	if st, err := os.Stat(s.LogPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log: %v %v", st, err)
	}
	if st, _ := os.Stat(h.logDir); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode())
	}
}

func TestSingleInstanceReuse(t *testing.T) {
	h := newHarness(t, "sleep 30")
	a, host := app("konsole", "single")
	r1, err := h.m.Launch(a, host)
	if err != nil || r1.Action != ActionStarted {
		t.Fatal(r1, err)
	}
	r2, err := h.m.Launch(a, host) // still connecting or connected
	if err != nil || r2.Action != ActionFocus || r2.Instance.ID != r1.Instance.ID {
		t.Fatalf("second launch: %+v %v", r2, err)
	}
	waitFor(t, "connected", func() bool { s, _ := h.m.Get(r1.Instance.ID); return s.State == Connected })
	r3, _ := h.m.Launch(a, host)
	if r3.Action != ActionFocus || r3.Instance.ID != r1.Instance.ID {
		t.Fatalf("third launch: %+v", r3)
	}
	if _, err := h.m.NewInstance(a, host); !errors.Is(err, ErrSingleInstance) {
		t.Fatalf("NewInstance on single app: %v", err)
	}
	if n := h.tr.builds.Load(); n != 1 {
		t.Fatalf("builds = %d", n)
	}
	if err := h.m.Focus(context.Background(), r1.Instance.ID); err != nil || h.comp.activated.Load() != 1 {
		t.Fatal("Focus should activate the compositor")
	}
}

func TestMultipleInstancesFollowSetting(t *testing.T) {
	h := newHarness(t, "sleep 30")
	a, host := app("konsole", "multiple")
	r1, _ := h.m.Launch(a, host)

	r2, _ := h.m.Launch(a, host) // default focus-recent
	if r2.Action != ActionFocus || r2.Instance.ID != r1.Instance.ID {
		t.Fatalf("focus-recent: %+v", r2)
	}
	h.settings.MultiInstanceReopen = "new"
	r3, _ := h.m.Launch(a, host)
	if r3.Action != ActionStarted || r3.Instance.ID == r1.Instance.ID {
		t.Fatalf("new: %+v", r3)
	}
	h.settings.MultiInstanceReopen = "focus-recent"
	r4, _ := h.m.Launch(a, host)
	if r4.Action != ActionFocus || r4.Instance.ID != r3.Instance.ID {
		t.Fatalf("should focus the newest: %+v", r4)
	}
	// Explicit NewInstance always starts one, whatever the setting.
	s5, err := h.m.NewInstance(a, host)
	if err != nil || s5.ID == r3.Instance.ID || s5.ID == r1.Instance.ID {
		t.Fatalf("NewInstance: %+v %v", s5, err)
	}
	if len(h.m.List()) != 3 {
		t.Fatalf("list = %d", len(h.m.List()))
	}
}

func TestProbeRetries(t *testing.T) {
	unreachable := &remote.Error{Kind: remote.KindUnreachable, Target: "h"}
	hostKey := &remote.Error{Kind: remote.KindHostKeyUnknown, Target: "h"}
	tests := []struct {
		name  string
		errs  []error
		state State
		calls int32
	}{
		{"succeeds on third attempt", []error{unreachable, unreachable}, Exited, 3},
		{"gives up after three", []error{unreachable, unreachable, unreachable, unreachable}, Failed, 3},
		{"host key problem is not retried", []error{hostKey}, Failed, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.prober.errs = tc.errs
			s := h.launchAndWait(t, "exit 0")
			if s.State != tc.state || h.prober.calls.Load() != tc.calls {
				t.Fatalf("state %s calls %d (%s)", s.State, h.prober.calls.Load(), s.Reason)
			}
			if tc.state == Failed {
				if h.tr.builds.Load() != 0 || s.ExitCode != nil {
					t.Fatal("transport must not start after a failed pre-check")
				}
				b, _ := os.ReadFile(s.LogPath)
				if !strings.Contains(string(b), "pre-launch check failed") {
					t.Fatalf("log = %q", b)
				}
			}
		})
	}
}

func TestCompositorFailure(t *testing.T) {
	h := newHarness(t, "")
	h.comp.ensureErr = errors.New("nope")
	s := h.launchAndWait(t, "exit 0")
	if s.State != Failed || !strings.Contains(s.Reason, "compositor not ready") || h.prober.calls.Load() != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestStopKillsProcessGroup(t *testing.T) {
	h := newHarness(t, "")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	h.tr.script = `sleep 300 & echo $! > ` + pidFile + `; wait`
	a, host := app("konsole", "single")
	r, _ := h.m.Launch(a, host)
	var childPID int
	waitFor(t, "child pid", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil || len(b) == 0 {
			return false
		}
		childPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return childPID > 0
	})
	if err := h.m.Stop(r.Instance.ID); err != nil {
		t.Fatal(err)
	}
	s := h.wait(t, r.Instance.ID)
	if s.State != Exited || s.Reason != "stopped by user" {
		t.Fatalf("%+v", s)
	}
	waitFor(t, "child to die", func() bool { return syscall.Kill(childPID, 0) != nil })
	waitFor(t, "events", func() bool { return len(h.states(s.ID)) == 4 })
	want := []State{Connecting, Connected, Stopping, Exited}
	for i, st := range h.states(s.ID) {
		if st != want[i] {
			t.Fatalf("states = %v", h.states(s.ID))
		}
	}
	if err := h.m.Stop(s.ID); !errors.Is(err, ErrNotLive) {
		t.Fatalf("second stop: %v", err)
	}
	if err := h.m.Stop("nope"); !errors.Is(err, ErrUnknownInstance) {
		t.Fatal(err)
	}
}

func TestStopEscalatesToSIGKILL(t *testing.T) {
	h := newHarness(t, "")
	h.tr.script = `trap '' TERM; echo ready; while :; do sleep 0.05; done`
	a, host := app("konsole", "single")
	r, _ := h.m.Launch(a, host)
	waitFor(t, "connected", func() bool { s, _ := h.m.Get(r.Instance.ID); return s.State == Connected })
	time.Sleep(100 * time.Millisecond) // let the shell install its trap
	start := time.Now()
	h.m.Stop(r.Instance.ID)
	s := h.wait(t, r.Instance.ID)
	if s.State != Exited || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("state %s after %v", s.State, time.Since(start))
	}
}

func TestStopDuringPreChecks(t *testing.T) {
	h := newHarness(t, "sleep 30")
	h.prober.block = true
	a, host := app("konsole", "single")
	r, _ := h.m.Launch(a, host)
	waitFor(t, "probe started", func() bool { return h.prober.calls.Load() == 1 })
	if err := h.m.Stop(r.Instance.ID); err != nil {
		t.Fatal(err)
	}
	s := h.wait(t, r.Instance.ID)
	if s.State != Exited || h.tr.builds.Load() != 0 {
		t.Fatalf("%+v builds=%d", s, h.tr.builds.Load())
	}
}

func TestStopAll(t *testing.T) {
	h := newHarness(t, "sleep 30")
	a, host := app("a", "single")
	b, _ := app("b", "single")
	h.m.Launch(a, host)
	h.m.Launch(b, host)
	waitFor(t, "both connected", func() bool {
		n := 0
		for _, s := range h.m.List() {
			if s.State == Connected {
				n++
			}
		}
		return n == 2
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.m.StopAll(ctx); err != nil {
		t.Fatal(err)
	}
	if h.m.HasLive() {
		t.Fatal("instances still live after StopAll")
	}
}

func TestDrainDeliversFinalEvent(t *testing.T) {
	h := newHarness(t, "")
	s := h.launchAndWait(t, "exit 0")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h.m.Drain(ctx)
	if got := h.states(s.ID); len(got) != 3 || got[2] != Exited {
		t.Fatalf("states after Drain = %v", got)
	}
}

func TestRemoveAndClearFinished(t *testing.T) {
	m := New(Config{})
	m.mu.Lock()
	for _, st := range []State{Exited, Disconnected, Connected} {
		id := string(st) + "-1"
		m.insts[id] = &inst{snap: Snapshot{ID: id, AppID: "a", State: st}}
		m.order = append(m.order, id)
	}
	m.mu.Unlock()
	if err := m.Remove("connected-1"); err != ErrStillLive {
		t.Fatalf("live remove: %v", err)
	}
	if err := m.Remove("nope"); err != ErrUnknownInstance {
		t.Fatalf("unknown remove: %v", err)
	}
	if err := m.Remove("exited-1"); err != nil {
		t.Fatal(err)
	}
	if n := m.ClearFinished(); n != 1 {
		t.Fatalf("cleared %d", n)
	}
	if l := m.List(); len(l) != 1 || l[0].ID != "connected-1" {
		t.Fatalf("list = %+v", l)
	}
}
