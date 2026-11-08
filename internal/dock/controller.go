package dock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/keiko233/waylauncher/internal/execx"
)

// Actions is what helpers may ask Waylauncher to do. Implementations decide
// everything (focus, confirmation, stopping); a helper never starts or stops
// anything itself.
type Actions interface {
	// OpenApp starts the application, or focuses it when it is already running.
	OpenApp(app string) error
	// FocusApp brings the application's most recent instance forward.
	FocusApp(app string) error
	// ConfirmStopApp asks the user to confirm stopping every instance of the
	// application and stops them on confirmation.
	ConfirmStopApp(app string)
}

// Desired is the state the controller makes real.
type Desired struct {
	Apps    []AppSpec
	Counts  map[string]int // live instances per app ID
	Enabled bool           // the "Show running Linux apps in the Dock" setting
}

type ControllerConfig struct {
	Bundles    BundleConfig
	SocketPath string
	Actions    Actions
	// Spawn starts a bundle's helper. Default: open -g -a <bundle> --args ...
	Spawn func(bundle string, args []string) error
	// RespawnDelay waits after a helper disconnects before another is
	// started, so that a helper that is still terminating is not reopened.
	RespawnDelay time.Duration
	// SpawnBackoff is the minimum time between two spawn attempts for one app.
	SpawnBackoff time.Duration
	Logf         func(format string, args ...any)
}

// Controller runs Waylauncher's side: the socket server, the bundles and the
// set of helper processes.
type Controller struct {
	cfg ControllerConfig
	srv *Server

	mu      sync.Mutex
	desired Desired
	paths   map[string]string    // app ID -> bundle path
	synced  string               // signature of the apps the bundles were synced for
	spawned map[string]time.Time // last spawn attempt per app
	wake    chan struct{}
	stop    chan struct{}
	done    sync.WaitGroup
	closed  bool

	ready     chan struct{} // closed by the first Update or Apply
	readyOnce sync.Once

	applyMu sync.Mutex
}

// NewController listens on the socket and starts the worker.
func NewController(cfg ControllerConfig) (*Controller, error) {
	if cfg.Spawn == nil {
		cfg.Spawn = openBundle
	}
	if cfg.RespawnDelay == 0 {
		cfg.RespawnDelay = 400 * time.Millisecond
	}
	if cfg.SpawnBackoff == 0 {
		cfg.SpawnBackoff = 20 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	c := &Controller{cfg: cfg, paths: map[string]string{}, spawned: map[string]time.Time{},
		wake: make(chan struct{}, 1), stop: make(chan struct{}), ready: make(chan struct{})}
	srv, err := Listen(cfg.SocketPath, (*handler)(c))
	if err != nil {
		return nil, err
	}
	c.srv = srv
	c.done.Add(1)
	go c.worker()
	return c, nil
}

// openBundle starts a bundle in the background (no focus change). The
// arguments reach the helper only when it was not running yet.
func openBundle(bundle string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := append([]string{"-g", "-a", bundle, "--args"}, args...)
	res, err := execx.Run(ctx, "/usr/bin/open", a...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("open: %s", string(res.Stderr))
	}
	return nil
}

// HelperArgs are the arguments a helper started by Waylauncher receives.
func HelperArgs(socket, waylauncherApp string) []string {
	a := []string{"--started-by-waylauncher", "--socket", socket}
	if waylauncherApp != "" {
		a = append(a, "--waylauncher-app", waylauncherApp)
	}
	return a
}

// Update records the desired state and applies it in the background.
// Calls coalesce: only the newest state is applied.
func (c *Controller) Update(d Desired) {
	c.mu.Lock()
	c.desired = d
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.ready) })
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Controller) worker() {
	defer c.done.Done()
	for {
		select {
		case <-c.stop:
			return
		case <-c.wake:
			c.mu.Lock()
			d := c.desired
			c.mu.Unlock()
			if err := c.apply(d); err != nil {
				c.cfg.Logf("dock: %v", err)
			}
		}
	}
}

// Apply makes the bundles and helpers match d, synchronously.
func (c *Controller) Apply(d Desired) error {
	c.mu.Lock()
	c.desired = d
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.ready) })
	return c.apply(d)
}

func appsSignature(apps []AppSpec) string {
	b, _ := json.Marshal(apps)
	return string(b)
}

func (c *Controller) apply(d Desired) error {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	var errs []error

	if c.cfg.Bundles.HelperBinary != "" {
		if sig := appsSignature(d.Apps); sig != c.syncedSig() {
			res, err := SyncBundles(c.cfg.Bundles, d.Apps)
			for _, w := range res.Warnings {
				c.cfg.Logf("dock: %s", w)
			}
			if len(res.Written)+len(res.BackedUp) > 0 {
				c.cfg.Logf("dock: bundles: %d written, %d kept, %d backed up", len(res.Written), len(res.Kept), len(res.BackedUp))
			}
			c.mu.Lock()
			c.paths = res.Paths
			if err == nil {
				c.synced = sig
			} else {
				c.synced = "" // retry on the next change
				errs = append(errs, err)
			}
			c.mu.Unlock()
		}
	}

	known := map[string]bool{}
	for _, a := range d.Apps {
		known[a.ID] = true
	}
	if !d.Enabled {
		c.srv.ExitAll()
	}
	for _, id := range c.srv.Apps() {
		if !known[id] {
			c.srv.Exit(id)
		}
	}
	for _, a := range d.Apps {
		n := d.Counts[a.ID]
		c.srv.SetCount(a.ID, n)
		if !d.Enabled || n == 0 || c.srv.Connected(a.ID) {
			continue
		}
		if err := c.spawn(a.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) syncedSig() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced
}

func (c *Controller) spawn(app string) error {
	c.mu.Lock()
	path := c.paths[app]
	last, tried := c.spawned[app]
	if path == "" || (tried && time.Since(last) < c.cfg.SpawnBackoff) {
		c.mu.Unlock()
		return nil
	}
	c.spawned[app] = time.Now()
	c.mu.Unlock()
	if err := c.cfg.Spawn(path, HelperArgs(c.cfg.SocketPath, c.cfg.Bundles.WaylauncherApp)); err != nil {
		return fmt.Errorf("start Dock helper for %s: %w", app, err)
	}
	return nil
}

// BundlePath returns the generated bundle of an application, if any.
func (c *Controller) BundlePath(app string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paths[app]
}

// Connected reports whether the application's helper is bound.
func (c *Controller) Connected(app string) bool { return c.srv.Connected(app) }

// Close tells every helper to exit, closes the socket and stops the worker.
func (c *Controller) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.stop)
	c.mu.Unlock()
	c.done.Wait()
	c.srv.Close()
}

// handler adapts the controller to the server's Handler.
type handler Controller

func (h *handler) c() *Controller { return (*Controller)(h) }

func (h *handler) Hello(app string, launch bool) bool {
	c := h.c()
	// A helper started by a click can connect before the profiles are loaded.
	select {
	case <-c.ready:
	case <-time.After(10 * time.Second):
		return false
	}
	c.mu.Lock()
	enabled := c.desired.Enabled
	known := false
	for _, a := range c.desired.Apps {
		known = known || a.ID == app
	}
	c.mu.Unlock()
	if !known {
		return false
	}
	if enabled {
		c.mu.Lock()
		delete(c.spawned, app) // bound: the next start may spawn at once
		c.mu.Unlock()
	}
	if launch { // the user opened the bundle: start the application
		go func() {
			if err := c.cfg.Actions.OpenApp(app); err != nil {
				c.cfg.Logf("dock: open %s: %v", app, err)
			}
		}()
	}
	return enabled
}

func (h *handler) Reopen(app string) {
	c := h.c()
	go func() {
		if err := c.cfg.Actions.FocusApp(app); err != nil {
			c.cfg.Logf("dock: focus %s: %v", app, err)
		}
	}()
}

func (h *handler) QuitRequest(app string) {
	c := h.c()
	go c.cfg.Actions.ConfirmStopApp(app)
}

func (h *handler) Gone(app string) {
	c := h.c()
	// A helper that left (count reached zero, or it was stopped) may need to
	// come back if the application started again meanwhile.
	time.AfterFunc(c.cfg.RespawnDelay, func() {
		select {
		case <-c.stop:
		default:
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
	})
}
