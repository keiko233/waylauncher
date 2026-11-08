package dock

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeActions struct {
	mu     sync.Mutex
	opened []string
	focus  []string
	stops  []string
	ch     chan struct{}
}

func newActions() *fakeActions { return &fakeActions{ch: make(chan struct{}, 64)} }
func (f *fakeActions) note() {
	select {
	case f.ch <- struct{}{}:
	default:
	}
}
func (f *fakeActions) OpenApp(a string) error {
	f.mu.Lock()
	f.opened = append(f.opened, a)
	f.mu.Unlock()
	f.note()
	return nil
}
func (f *fakeActions) FocusApp(a string) error {
	f.mu.Lock()
	f.focus = append(f.focus, a)
	f.mu.Unlock()
	f.note()
	return nil
}
func (f *fakeActions) ConfirmStopApp(a string) {
	f.mu.Lock()
	f.stops = append(f.stops, a)
	f.mu.Unlock()
	f.note()
}
func (f *fakeActions) count(which *[]string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(*which)
}
func (f *fakeActions) await(t *testing.T, which *[]string, n int) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f.count(which) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d calls, got %d", n, f.count(which))
}

// ctrlEnv wires a Controller to in-process helpers: Spawn runs a real Helper
// (the same code the helper executable runs) instead of opening a bundle.
type ctrlEnv struct {
	c      *Controller
	act    *fakeActions
	sock   string
	mu     sync.Mutex
	spawns []string // bundle paths
	args   [][]string
	exited chan error
	hs     map[string]*Helper
	env    *bundleEnv
}

func newCtrlEnv(t *testing.T) *ctrlEnv {
	t.Helper()
	be := newBundleEnv(t)
	e := &ctrlEnv{act: newActions(), sock: SocketPath(filepath.Join(shortDir(t), "rt")), exited: make(chan error, 16), hs: map[string]*Helper{}, env: be}
	c, err := NewController(ControllerConfig{
		Bundles: be.cfg, SocketPath: e.sock, Actions: e.act,
		RespawnDelay: 20 * time.Millisecond, SpawnBackoff: time.Hour, Logf: t.Logf,
		Spawn: func(bundle string, args []string) error {
			e.mu.Lock()
			e.spawns = append(e.spawns, bundle)
			e.args = append(e.args, args)
			e.mu.Unlock()
			info := filepath.Base(bundle)
			app := map[string]string{"Linux Terminal.app": "konsole", "foot.app": "foot"}[info]
			cfg := fastCfg(e.sock, true)
			cfg.App = app
			h := NewHelper(cfg)
			e.mu.Lock()
			e.hs[app] = h
			e.mu.Unlock()
			go func() { e.exited <- h.Run(context.Background()) }()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	e.c = c
	return e
}

func (e *ctrlEnv) spawned() int { e.mu.Lock(); defer e.mu.Unlock(); return len(e.spawns) }

func (e *ctrlEnv) wantExit(t *testing.T, want error) {
	t.Helper()
	select {
	case err := <-e.exited:
		if err != want {
			t.Fatalf("helper ended with %v, want %v", err, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no helper exited (want %v)", want)
	}
}

func TestControllerHelperFollowsInstanceCount(t *testing.T) {
	e := newCtrlEnv(t)
	apps := []AppSpec{konsole(), foot()}

	// Nothing running: bundles exist, no helper.
	if err := e.c.Apply(Desired{Apps: apps, Counts: map[string]int{}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if e.spawned() != 0 {
		t.Fatal("helper started with nothing running")
	}
	if e.c.BundlePath("konsole") == "" {
		t.Fatal("no bundle for konsole")
	}

	// An instance starts: exactly one helper for the app, however many instances.
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 1}, Enabled: true})
	waitFor(t, func() bool { return e.c.Connected("konsole") })
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 2}, Enabled: true})
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 3}, Enabled: true})
	if e.spawned() != 1 {
		t.Fatalf("spawned %d helpers for one app", e.spawned())
	}
	args := e.args[0]
	if args[0] != "--started-by-waylauncher" || args[1] != "--socket" || args[2] != e.sock {
		t.Fatalf("helper args %v", args)
	}
	if filepath.Base(e.spawns[0]) != "Linux Terminal.app" {
		t.Fatalf("spawned %v", e.spawns)
	}

	// One of three ends: still running. All end: the helper exits.
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 1}, Enabled: true})
	select {
	case err := <-e.exited:
		t.Fatalf("helper left with an instance still live: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 0}, Enabled: true})
	e.wantExit(t, ErrNoInstances)
	waitFor(t, func() bool { return !e.c.Connected("konsole") })
	if e.spawned() != 1 {
		t.Fatal("a helper was restarted for a stopped app")
	}

	// The app starts again later: a new helper.
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 1}, Enabled: true})
	waitFor(t, func() bool { return e.spawned() == 2 && e.c.Connected("konsole") })
}

func TestControllerSettingOffAndOn(t *testing.T) {
	e := newCtrlEnv(t)
	apps := []AppSpec{konsole()}
	run := map[string]int{"konsole": 1}

	e.c.Apply(Desired{Apps: apps, Counts: run, Enabled: false})
	if e.spawned() != 0 {
		t.Fatal("helper started with the setting off")
	}
	e.c.Apply(Desired{Apps: apps, Counts: run, Enabled: true})
	waitFor(t, func() bool { return e.c.Connected("konsole") })

	// Turning it off while running makes the helper exit; the app keeps running.
	e.c.Apply(Desired{Apps: apps, Counts: run, Enabled: false})
	e.wantExit(t, ErrAppExit)
}

func TestControllerRemovedAppAndShutdown(t *testing.T) {
	e := newCtrlEnv(t)
	apps := []AppSpec{konsole(), foot()}
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 1, "foot": 1}, Enabled: true})
	waitFor(t, func() bool { return e.c.Connected("konsole") && e.c.Connected("foot") })

	e.c.Apply(Desired{Apps: []AppSpec{foot()}, Counts: map[string]int{"foot": 1}, Enabled: true})
	e.wantExit(t, ErrAppExit) // konsole's profile is gone

	// Waylauncher quitting tells the remaining helpers to exit.
	e.c.Close()
	e.wantExit(t, ErrAppExit)
}

func TestControllerHelperRequests(t *testing.T) {
	e := newCtrlEnv(t)
	apps := []AppSpec{konsole()}
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 1}, Enabled: true})
	waitFor(t, func() bool { return e.c.Connected("konsole") })

	h := e.hs["konsole"]
	time.Sleep(80 * time.Millisecond) // past the helper's startup quiet period
	h.Reopen()
	e.act.await(t, &e.act.focus, 1)
	if e.act.count(&e.act.opened) != 0 {
		t.Fatal("a Dock click started an instance")
	}
	if h.QuitRequested() {
		t.Fatal("helper terminated itself instead of asking")
	}
	e.act.await(t, &e.act.stops, 1)
	// Cancel in the dialog: nothing changes, the helper stays.
	select {
	case err := <-e.exited:
		t.Fatalf("helper left after a cancelled quit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// Confirm: the instances end, the count reaches zero.
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{"konsole": 0}, Enabled: true})
	e.wantExit(t, ErrNoInstances)
}

func TestControllerHelloFromBundleClickLaunches(t *testing.T) {
	e := newCtrlEnv(t)
	apps := []AppSpec{konsole()}
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{}, Enabled: true})

	// The user opened the bundle: a helper without --started-by-waylauncher.
	cfg := fastCfg(e.sock, false)
	cfg.App = "konsole"
	go NewHelper(cfg).Run(context.Background())
	e.act.await(t, &e.act.opened, 1)

	// With the Dock setting off the app still launches, and the helper leaves.
	e.c.Apply(Desired{Apps: apps, Counts: map[string]int{}, Enabled: false})
	cfg2 := fastCfg(e.sock, false)
	cfg2.App = "konsole"
	done := make(chan error, 1)
	go func() { done <- NewHelper(cfg2).Run(context.Background()) }()
	e.act.await(t, &e.act.opened, 2)
	select {
	case err := <-done:
		if err != ErrAppExit {
			t.Fatalf("%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper stayed although the setting is off")
	}

	// An application that is not in the profiles is refused and not launched.
	cfg3 := fastCfg(e.sock, false)
	cfg3.App = "ghost"
	if err := NewHelper(cfg3).Run(context.Background()); err != ErrAppExit {
		t.Fatalf("%v", err)
	}
	if e.act.count(&e.act.opened) != 2 {
		t.Fatal("unknown app was launched")
	}
}

func TestControllerHelperBeforeFirstUpdateWaits(t *testing.T) {
	e := newCtrlEnv(t)
	cfg := fastCfg(e.sock, false)
	cfg.App = "konsole"
	done := make(chan error, 1)
	go func() { done <- NewHelper(cfg).Run(context.Background()) }()
	time.Sleep(100 * time.Millisecond) // connected, but profiles are not loaded yet
	e.c.Update(Desired{Apps: []AppSpec{konsole()}, Counts: map[string]int{"konsole": 1}, Enabled: true})
	e.act.await(t, &e.act.opened, 1)
	waitFor(t, func() bool { return e.c.Connected("konsole") })
	select {
	case err := <-done:
		t.Fatalf("helper rejected during startup: %v", err)
	default:
	}
}
