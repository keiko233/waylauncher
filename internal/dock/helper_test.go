package dock

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startHelper(t *testing.T, h *Helper) <-chan error {
	t.Helper()
	ch := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { ch <- h.Run(ctx) }()
	return ch
}

func wantExit(t *testing.T, ch <-chan error, want error) {
	t.Helper()
	select {
	case err := <-ch:
		if !errors.Is(err, want) {
			t.Fatalf("helper ended with %v, want %v", err, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("helper still running, want exit with %v", want)
	}
}

func wantRunning(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("helper exited early: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
}

func fastCfg(sock string, started bool) HelperConfig {
	return HelperConfig{App: "konsole", Socket: sock, StartedByApp: started,
		Grace: 400 * time.Millisecond, ConnectWait: time.Second, StartupQuiet: 50 * time.Millisecond, PollEvery: 10 * time.Millisecond}
}

func TestHelperExitsWhenCountReachesZero(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	rec := newRecorder()
	srv, _ := Listen(sock, rec)
	defer srv.Close()
	srv.SetCount("konsole", 1)

	ch := startHelper(t, NewHelper(fastCfg(sock, true)))
	wantRunning(t, ch)
	srv.SetCount("konsole", 3)
	wantRunning(t, ch)
	srv.SetCount("konsole", 1) // one of three exited: stays
	wantRunning(t, ch)
	srv.SetCount("konsole", 0) // exited, disconnected, failed or stopped: leaves
	wantExit(t, ch, ErrNoInstances)
}

func TestHelperIgnoresZeroUntilFirstInstanceThenGivesUp(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	srv, _ := Listen(sock, newRecorder())
	defer srv.Close()

	// Zero at hello is not "the app ended": the launch may be in progress.
	ch := startHelper(t, NewHelper(fastCfg(sock, false)))
	wantRunning(t, ch)
	// A launch that never produces an instance ends after the grace period.
	wantExit(t, ch, ErrNoLaunch)

	// ... but an instance that appears in time keeps the helper alive.
	ch2 := startHelper(t, NewHelper(fastCfg(sock, false)))
	time.Sleep(50 * time.Millisecond)
	srv.SetCount("konsole", 1)
	time.Sleep(500 * time.Millisecond) // beyond the grace period
	select {
	case err := <-ch2:
		t.Fatalf("helper left although an instance is live: %v", err)
	default:
	}
	srv.SetCount("konsole", 0)
	wantExit(t, ch2, ErrNoInstances)
}

func TestHelperFollowsExitAndConnectionLoss(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	srv, _ := Listen(sock, newRecorder())
	srv.SetCount("konsole", 1)
	ch := startHelper(t, NewHelper(fastCfg(sock, true)))
	wantRunning(t, ch)
	srv.Exit("konsole")
	wantExit(t, ch, ErrAppExit)

	// Waylauncher quitting or crashing closes the socket: the helper leaves.
	srv.SetCount("konsole", 1)
	ch = startHelper(t, NewHelper(fastCfg(sock, true)))
	wantRunning(t, ch)
	for _, sc := range srv.snapshotConns() {
		sc.c.Close() // as if the process died
	}
	wantExit(t, ch, ErrAppGone)
	srv.Close()
}

func (s *Server) snapshotConns() []*serverConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*serverConn
	for _, c := range s.conns {
		out = append(out, c)
	}
	return out
}

func TestHelperClickWhileNotRunningStartsWaylauncherThenBinds(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	rec := newRecorder()
	var srv *Server
	var urls []string
	var apps []string
	var mu sync.Mutex
	cfg := fastCfg(sock, false)
	cfg.WaylauncherApp = "/Apps/Waylauncher.app"
	cfg.OpenURL = func(url, app string) error {
		mu.Lock()
		urls, apps = append(urls, url), append(apps, app)
		mu.Unlock()
		// Waylauncher comes up a moment later and launches the app itself.
		go func() {
			time.Sleep(100 * time.Millisecond)
			s, err := Listen(sock, rec)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			srv = s
			mu.Unlock()
			s.SetCount("konsole", 1)
		}()
		return nil
	}
	ch := startHelper(t, NewHelper(cfg))
	wantRunning(t, ch)
	mu.Lock()
	defer mu.Unlock()
	if len(urls) != 1 || urls[0] != "waylauncher://launch/konsole" || apps[0] != "/Apps/Waylauncher.app" {
		t.Fatalf("OpenURL calls: %v %v", urls, apps)
	}
	if srv == nil || !srv.Connected("konsole") {
		t.Fatal("helper did not bind after Waylauncher came up")
	}
	for _, e := range rec.events {
		if e == "launch konsole" {
			t.Fatal("hello asked for a second launch although the deep link already launches")
		}
	}
	srv.SetCount("konsole", 0)
	wantExit(t, ch, ErrNoInstances)
	srv.Close()
}

func TestHelperClickWithWaylauncherRunningAsksForLaunch(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	rec := newRecorder()
	srv, _ := Listen(sock, rec)
	defer srv.Close()
	var opened atomic.Int32
	cfg := fastCfg(sock, false)
	cfg.OpenURL = func(string, string) error { opened.Add(1); return nil }
	ch := startHelper(t, NewHelper(cfg))
	rec.wait(t, "launch konsole")
	if opened.Load() != 0 {
		t.Fatal("opened a URL although Waylauncher was reachable")
	}
	srv.SetCount("konsole", 1)
	srv.SetCount("konsole", 0)
	wantExit(t, ch, ErrNoInstances)
}

func TestHelperLaunchFailureEndsHelper(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt")) // nobody listens
	cfg := fastCfg(sock, false)
	cfg.ConnectWait = 150 * time.Millisecond
	cfg.OpenURL = func(string, string) error { return nil } // opened, but it never comes up
	wantExit(t, startHelper(t, NewHelper(cfg)), ErrNoApp)

	cfg.OpenURL = func(string, string) error { return errors.New("no such app") }
	select {
	case err := <-startHelper(t, NewHelper(cfg)):
		if err == nil || !strings.Contains(err.Error(), "cannot start Waylauncher") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper did not end after a failed launch")
	}
}

func TestHelperStartedByAppWithoutSocketLeaves(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	cfg := fastCfg(sock, true)
	cfg.OpenURL = func(string, string) error { t.Error("must not open Waylauncher when it started us"); return nil }
	ch := startHelper(t, NewHelper(cfg))
	select {
	case err := <-ch:
		if !errors.Is(err, ErrNoApp) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper stayed without a Waylauncher")
	}
}

func TestHelperReopenAndQuit(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	rec := newRecorder()
	srv, _ := Listen(sock, rec)
	defer srv.Close()
	srv.SetCount("konsole", 1)

	now := time.Now()
	var mu sync.Mutex
	cfg := fastCfg(sock, true)
	cfg.StartupQuiet = 2 * time.Second
	cfg.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	// Not connected: nobody could confirm, so the process may quit.
	if !NewHelper(cfg).QuitRequested() {
		t.Fatal("quit refused although not connected")
	}

	h := NewHelper(cfg)
	ch := startHelper(t, h)
	waitFor(t, func() bool { return srv.Connected("konsole") })

	h.Reopen() // the launch itself, not a click
	time.Sleep(100 * time.Millisecond)
	for _, e := range rec.events {
		if e == "reopen konsole" {
			t.Fatal("reopen right after start was forwarded")
		}
	}
	advance(3 * time.Second)
	h.Reopen()
	rec.wait(t, "reopen konsole")

	// Quit from the Dock menu: forwarded, and the helper stays.
	if h.QuitRequested() {
		t.Fatal("quit allowed although connected")
	}
	rec.wait(t, "quit konsole")
	wantRunning(t, ch)
	// Only the count reaching zero (after the user confirmed) ends it.
	srv.SetCount("konsole", 0)
	wantExit(t, ch, ErrNoInstances)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
