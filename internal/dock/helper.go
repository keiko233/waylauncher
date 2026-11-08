package dock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// LaunchURL is the deep link that starts Waylauncher and an application.
func LaunchURL(app string) string { return "waylauncher://launch/" + app }

// HelperConfig configures the helper's lifecycle. The zero values of the
// durations select the defaults documented on each field.
type HelperConfig struct {
	App    string // profile ID
	Socket string // Waylauncher's helper socket
	// StartedByApp is true when Waylauncher started this helper (the
	// application is already launching). Otherwise the user opened the
	// bundle, and the helper makes sure Waylauncher runs and starts the app.
	StartedByApp bool
	// WaylauncherApp is the Waylauncher.app to open when it is not running;
	// empty means "whatever handles waylauncher://".
	WaylauncherApp string

	// OpenURL starts Waylauncher with a deep link. Default: /usr/bin/open.
	OpenURL func(url, app string) error
	// Dial connects to the socket. Default: Dial.
	Dial func(path string) (*Client, error)

	// Grace is how long the helper waits for the first live instance before
	// giving up (a launch that never produced an instance). Default 45s.
	Grace time.Duration
	// ConnectWait is how long it waits for Waylauncher's socket after
	// starting it. Default 30s.
	ConnectWait time.Duration
	// StartupQuiet ignores Dock "reopen" events right after the process
	// started: they are the launch itself, not a click. Default 2s.
	StartupQuiet time.Duration
	// PollEvery is the socket polling interval. Default 200ms.
	PollEvery time.Duration

	Now func() time.Time
}

func (c *HelperConfig) defaults() {
	if c.OpenURL == nil {
		c.OpenURL = openURL
	}
	if c.Dial == nil {
		c.Dial = func(p string) (*Client, error) { return Dial(p, time.Second) }
	}
	if c.Grace == 0 {
		c.Grace = 45 * time.Second
	}
	if c.ConnectWait == 0 {
		c.ConnectWait = 30 * time.Second
	}
	if c.StartupQuiet == 0 {
		c.StartupQuiet = 2 * time.Second
	}
	if c.PollEvery == 0 {
		c.PollEvery = 200 * time.Millisecond
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

func openURL(url, app string) error {
	args := []string{url}
	if app != "" {
		args = []string{"-a", app, url}
	}
	return exec.Command("/usr/bin/open", args...).Run()
}

// Exit reasons returned by Helper.Run (all are normal ends).
var (
	ErrNoInstances = errors.New("no live instances left")
	ErrAppExit     = errors.New("Waylauncher asked the helper to exit")
	ErrAppGone     = errors.New("Waylauncher closed the connection")
	ErrNoLaunch    = errors.New("the application never started")
	ErrNoApp       = errors.New("Waylauncher did not come up")
	ErrQuit        = errors.New("quit while not connected")
)

// Helper is the lifecycle of one Dock helper process: connect, report Dock
// events, and stop when the application has no live instances. It contains
// no AppKit code; the executable wires Reopen and QuitRequested to the Dock.
type Helper struct {
	cfg   HelperConfig
	start time.Time

	mu     sync.Mutex
	client *Client
	stop   chan struct{}
	once   sync.Once
}

func NewHelper(cfg HelperConfig) *Helper {
	cfg.defaults()
	return &Helper{cfg: cfg, start: cfg.Now(), stop: make(chan struct{})}
}

// Reopen reports a click on the Dock icon (or Finder/Launchpad/Spotlight)
// while the helper is running. Waylauncher focuses the application's most
// recent instance; this never starts another one.
func (h *Helper) Reopen() {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil || h.cfg.Now().Sub(h.start) < h.cfg.StartupQuiet {
		return
	}
	go func() { _ = c.Send(Message{Type: TypeReopen}) }()
}

// QuitRequested handles Quit from the Dock menu or Cmd-Q. It returns true if
// the process may terminate now (not connected, so nobody could confirm).
// Otherwise it asks Waylauncher, which confirms with the user and, if the
// instances end, drops the count to zero so Run returns.
func (h *Helper) QuitRequested() (terminateNow bool) {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil {
		h.once.Do(func() { close(h.stop) })
		return true
	}
	go func() { _ = c.Send(Message{Type: TypeQuitRequest}) }()
	return false
}

// Run connects and serves until the helper should exit; the returned error
// says why (all of the Err* values are normal).
func (h *Helper) Run(ctx context.Context) error {
	c, launch, err := h.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(Hello(h.cfg.App, launch)); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	h.mu.Lock()
	h.client = c
	h.mu.Unlock()

	msgs := make(chan Message)
	errc := make(chan error, 1)
	go func() {
		for {
			m, err := c.Recv()
			if err != nil {
				errc <- err
				return
			}
			select {
			case msgs <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	grace := time.NewTimer(h.cfg.Grace)
	defer grace.Stop()
	seenLive := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-h.stop:
			return ErrQuit
		case <-grace.C:
			if !seenLive {
				return ErrNoLaunch
			}
		case err := <-errc:
			if errors.Is(err, io.EOF) {
				return ErrAppGone
			}
			return fmt.Errorf("%w: %v", ErrAppGone, err)
		case m := <-msgs:
			switch m.Type {
			case TypeExit:
				return ErrAppExit
			case TypeCount:
				if *m.N > 0 {
					seenLive = true
				} else if seenLive {
					return ErrNoInstances
				}
			}
		}
	}
}

// connect dials the socket. If Waylauncher is not running and the user
// opened the bundle, it starts Waylauncher with the deep link first. launch
// reports whether the hello should ask for the application to be started.
func (h *Helper) connect(ctx context.Context) (c *Client, launch bool, err error) {
	if c, err = h.cfg.Dial(h.cfg.Socket); err == nil {
		return c, !h.cfg.StartedByApp, nil
	}
	if h.cfg.StartedByApp {
		// Waylauncher started us and its socket is gone: it quit meanwhile.
		c, err = h.poll(ctx, 2*time.Second)
		if err != nil {
			return nil, false, ErrNoApp
		}
		return c, false, nil
	}
	// The deep link starts Waylauncher, which then launches the application.
	if err := h.cfg.OpenURL(LaunchURL(h.cfg.App), h.cfg.WaylauncherApp); err != nil {
		return nil, false, fmt.Errorf("cannot start Waylauncher: %w", err)
	}
	if c, err = h.poll(ctx, h.cfg.ConnectWait); err != nil {
		return nil, false, ErrNoApp
	}
	return c, false, nil
}

func (h *Helper) poll(ctx context.Context, limit time.Duration) (*Client, error) {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	t := time.NewTicker(h.cfg.PollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-h.stop:
			return nil, ErrQuit
		case <-deadline.C:
			return nil, ErrNoApp
		case <-t.C:
			if c, err := h.cfg.Dial(h.cfg.Socket); err == nil {
				return c, nil
			}
		}
	}
}
