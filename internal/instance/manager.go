package instance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

var (
	ErrUnknownInstance = errors.New("unknown instance")
	ErrNotLive         = errors.New("instance is not running")
	ErrSingleInstance  = errors.New("application allows only one instance")
	ErrStopping        = errors.New("previous instance is still stopping")
	ErrStillLive       = errors.New("instance is still running; stop it first")
)

const maxFinished = 50 // finished instances kept for the UI

type Prober interface {
	Probe(ctx context.Context, target string) (remote.ProbeResult, error)
}

type Config struct {
	Compositor compositor.Compositor
	Transports map[string]transport.Transport // by name, e.g. "waypipe"
	Prober     Prober
	Settings   func() profile.Settings // read at each launch and exit
	Logs       *LogStore

	ProbeAttempts int           // pre-check attempts, default 3
	RetryDelay    time.Duration // between pre-check attempts, default 1s
	StopTimeout   time.Duration // SIGTERM to SIGKILL, default 3s
}

type inst struct {
	snap          Snapshot
	app           profile.App
	host          profile.Host
	ctx           context.Context
	cancel        context.CancelFunc
	cmd           *exec.Cmd
	stopRequested bool
	killTimer     *time.Timer
	done          chan struct{}
}

type Manager struct {
	cfg Config

	mu    sync.Mutex
	insts map[string]*inst
	order []string // creation order, oldest first

	evMu      sync.Mutex
	queue     []Event
	draining  bool
	subs      map[int]func(Event)
	nextSubID int
}

func New(cfg Config) *Manager {
	if cfg.ProbeAttempts == 0 {
		cfg.ProbeAttempts = 3
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 3 * time.Second
	}
	if cfg.Settings == nil {
		cfg.Settings = profile.DefaultSettings
	}
	return &Manager{cfg: cfg, insts: map[string]*inst{}, subs: map[int]func(Event){}}
}

// Subscribe registers fn for state changes. Events are delivered in order on
// a single goroutine, so fn may block briefly but should not call Subscribe
// callbacks recursively. The returned function unsubscribes.
func (m *Manager) Subscribe(fn func(Event)) (unsubscribe func()) {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	id := m.nextSubID
	m.nextSubID++
	m.subs[id] = fn
	return func() {
		m.evMu.Lock()
		delete(m.subs, id)
		m.evMu.Unlock()
	}
}

func (m *Manager) emit(ev Event) {
	m.evMu.Lock()
	m.queue = append(m.queue, ev)
	start := !m.draining
	m.draining = true
	m.evMu.Unlock()
	if start {
		go m.drain()
	}
}

func (m *Manager) drain() {
	for {
		m.evMu.Lock()
		if len(m.queue) == 0 {
			m.draining = false
			m.evMu.Unlock()
			return
		}
		ev := m.queue[0]
		m.queue = m.queue[1:]
		fns := make([]func(Event), 0, len(m.subs))
		for _, fn := range m.subs {
			fns = append(fns, fn)
		}
		m.evMu.Unlock()
		for _, fn := range fns {
			fn(ev)
		}
	}
}

// Launch starts the application, or returns its existing live instance with
// ActionFocus. Single-instance apps always reuse a live instance; multiple-
// instance apps reuse the newest one unless the multiInstanceReopen setting
// is "new". Use NewInstance to start another explicitly.
func (m *Manager) Launch(app profile.App, host profile.Host) (LaunchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live := m.liveLocked(app.ID)
	if len(live) > 0 {
		reuse := app.Instances != profile.InstancesMultiple || m.cfg.Settings().MultiInstanceReopen != "new"
		if reuse {
			for i := len(live) - 1; i >= 0; i-- { // newest first
				if live[i].snap.State != Stopping {
					return LaunchResult{Instance: live[i].snap, Action: ActionFocus}, nil
				}
			}
			if app.Instances != profile.InstancesMultiple {
				return LaunchResult{}, ErrStopping
			}
		}
	}
	snap := m.startLocked(app, host)
	return LaunchResult{Instance: snap, Action: ActionStarted}, nil
}

// NewInstance always starts another instance of a multiple-instance app. A
// single-instance app with a live instance is refused.
func (m *Manager) NewInstance(app profile.App, host profile.Host) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app.Instances != profile.InstancesMultiple && len(m.liveLocked(app.ID)) > 0 {
		return Snapshot{}, ErrSingleInstance
	}
	return m.startLocked(app, host), nil
}

func (m *Manager) liveLocked(appID string) []*inst {
	var out []*inst
	for _, id := range m.order {
		if in := m.insts[id]; in.snap.AppID == appID && in.snap.State.Live() {
			out = append(out, in)
		}
	}
	return out
}

func (m *Manager) newIDLocked(appID string) string {
	for {
		var b [2]byte
		rand.Read(b[:])
		id := appID + "-" + hex.EncodeToString(b[:])
		if _, taken := m.insts[id]; !taken {
			return id
		}
	}
}

func (m *Manager) startLocked(app profile.App, host profile.Host) Snapshot {
	ctx, cancel := context.WithCancel(context.Background())
	in := &inst{
		app: app, host: host, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		snap: Snapshot{
			ID: m.newIDLocked(app.ID), AppID: app.ID, AppName: app.Name, HostID: host.ID,
			State: Connecting, StartedAt: time.Now(),
		},
	}
	m.insts[in.snap.ID] = in
	m.order = append(m.order, in.snap.ID)
	m.pruneLocked()
	snap := in.snap
	m.emit(Event{Instance: snap})
	go m.run(in)
	return snap
}

func (m *Manager) pruneLocked() {
	finished := 0
	for _, id := range m.order {
		if !m.insts[id].snap.State.Live() {
			finished++
		}
	}
	kept := m.order[:0]
	for _, id := range m.order {
		if finished > maxFinished && !m.insts[id].snap.State.Live() {
			delete(m.insts, id)
			finished--
			continue
		}
		kept = append(kept, id)
	}
	m.order = kept
}

// transition updates state under the lock and emits the change.
func (m *Manager) transition(in *inst, to State, mutate func(*Snapshot)) {
	m.mu.Lock()
	prev := in.snap.State
	in.snap.State = to
	if mutate != nil {
		mutate(&in.snap)
	}
	// Emitting under the lock keeps events in state-change order.
	m.emit(Event{Instance: in.snap, Previous: prev})
	m.mu.Unlock()
}

func (m *Manager) run(in *inst) {
	defer close(in.done)
	defer in.cancel()

	logPath, logf, err := m.openLog(in)
	if err != nil {
		m.fail(in, nil, fmt.Errorf("cannot create application log: %w", err))
		return
	}
	defer logf.Close()
	fmt.Fprintf(logf, "# instance %s (app %s, host %s) starting\n", in.snap.ID, in.app.ID, in.host.ID)

	if err := m.cfg.Compositor.Ensure(in.ctx); err != nil {
		m.fail(in, logf, fmt.Errorf("compositor not ready: %w", err))
		return
	}
	if err := m.probe(in); err != nil {
		m.fail(in, logf, err)
		return
	}
	rt, err := m.cfg.Compositor.Runtime(in.ctx)
	if err != nil {
		m.fail(in, logf, err)
		return
	}
	tr, ok := m.cfg.Transports[in.app.Transport.MacOS]
	if !ok {
		m.fail(in, logf, fmt.Errorf("transport %q is not available", in.app.Transport.MacOS))
		return
	}
	tc, err := tr.Build(transport.Spec{
		InstanceID: in.snap.ID,
		SSHTarget:  in.host.SSH,
		Remote:     remote.Spec{Command: in.app.Command, Cwd: in.app.Cwd, Env: in.app.Env},
		Runtime:    rt,
	})
	if err != nil {
		m.fail(in, logf, err)
		return
	}

	cmd := exec.Command(tc.Path, tc.Args...)
	cmd.Env = tc.Env
	cmd.Stdin = nil // /dev/null
	cmd.Stdout, cmd.Stderr = logf, logf
	setProcessGroup(cmd)

	m.mu.Lock()
	if in.stopRequested { // Stop arrived while pre-checks ran
		m.mu.Unlock()
		m.fail(in, logf, nil)
		return
	}
	err = cmd.Start()
	if err == nil {
		in.cmd = cmd
		in.snap.State = Connected // under the same lock, so a concurrent Stop cannot be overwritten
		m.emit(Event{Instance: in.snap, Previous: Connecting})
	}
	m.mu.Unlock()
	if err != nil {
		m.fail(in, logf, fmt.Errorf("cannot start %s: %w", tr.Name(), err))
		return
	}

	_ = cmd.Wait() // the exit code is read from ProcessState
	// Best effort: reap helpers (ssh) that outlived the leader.
	killGroup(cmd, syscall.SIGTERM)
	code := cmd.ProcessState.ExitCode() // -1 when killed by a signal
	m.finish(in, code, logPath)
}

func (m *Manager) openLog(in *inst) (string, *os.File, error) {
	m.mu.Lock()
	protected := map[string]bool{}
	for _, other := range m.insts {
		if other.snap.LogPath != "" && other.snap.State.Live() {
			protected[other.snap.LogPath] = true
		}
	}
	m.mu.Unlock()
	f, err := m.cfg.Logs.Create(in.app.ID, m.cfg.Settings().LogsKeepPerApp, protected)
	if err != nil {
		return "", nil, err
	}
	m.mu.Lock()
	in.snap.LogPath = f.Name()
	m.mu.Unlock()
	return f.Name(), f, nil
}

func (m *Manager) probe(in *inst) error {
	var err error
	for attempt := 1; attempt <= m.cfg.ProbeAttempts; attempt++ {
		if _, err = m.cfg.Prober.Probe(in.ctx, in.host.SSH); err == nil {
			return nil
		}
		var re *remote.Error
		if in.ctx.Err() != nil || (errors.As(err, &re) && !re.Retryable()) || attempt == m.cfg.ProbeAttempts {
			break
		}
		select {
		case <-in.ctx.Done():
		case <-time.After(m.cfg.RetryDelay):
		}
	}
	return err
}

// fail ends an instance whose transport never started. If the user stopped
// it meanwhile, that is a normal exit, not a failure.
func (m *Manager) fail(in *inst, logf *os.File, cause error) {
	m.mu.Lock()
	stopped := in.stopRequested
	m.mu.Unlock()
	now := time.Now()
	if stopped {
		m.transition(in, Exited, func(s *Snapshot) { s.Reason = "stopped before the application started"; s.EndedAt = &now })
		return
	}
	if logf != nil {
		fmt.Fprintf(logf, "# pre-launch check failed: %v\n", cause)
	}
	m.transition(in, Failed, func(s *Snapshot) { s.Reason = cause.Error(); s.EndedAt = &now })
}

func (m *Manager) finish(in *inst, code int, logPath string) {
	m.mu.Lock()
	stopped := in.stopRequested
	if in.killTimer != nil {
		in.killTimer.Stop()
	}
	m.mu.Unlock()

	now := time.Now()
	set := func(reason string) func(*Snapshot) {
		return func(s *Snapshot) { s.ExitCode = &code; s.Reason = reason; s.EndedAt = &now }
	}
	if stopped {
		m.transition(in, Exited, set("stopped by user"))
		return
	}
	x := Classify(code, readTail(logPath, 64<<10), m.cfg.Settings().TreatNoChildProcessesAsNormal)
	if x.Normal {
		m.transition(in, Exited, set(x.Reason))
		return
	}
	// Deliberately no restart: unsaved remote work and duplicate processes matter.
	m.transition(in, Disconnected, set(x.Reason))
}

// Stop terminates the instance's process group (SIGTERM, then SIGKILL after
// StopTimeout). A user-stopped instance ends as "exited", never "disconnected".
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	in, ok := m.insts[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknownInstance
	}
	if !in.snap.State.Live() {
		m.mu.Unlock()
		return ErrNotLive
	}
	if in.stopRequested {
		m.mu.Unlock()
		return nil
	}
	in.stopRequested = true
	prev := in.snap.State
	in.snap.State = Stopping
	m.emit(Event{Instance: in.snap, Previous: prev})
	if in.cmd != nil {
		killGroup(in.cmd, syscall.SIGTERM)
		cmd := in.cmd
		in.killTimer = time.AfterFunc(m.cfg.StopTimeout, func() { killGroup(cmd, syscall.SIGKILL) })
	}
	m.mu.Unlock()
	in.cancel() // abort pre-checks
	return nil
}

// StopAll stops every live instance and waits for them, bounded by ctx.
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	var live []*inst
	for _, id := range m.order {
		if in := m.insts[id]; in.snap.State.Live() {
			live = append(live, in)
		}
	}
	m.mu.Unlock()
	for _, in := range live {
		_ = m.Stop(in.snap.ID)
	}
	for _, in := range live {
		select {
		case <-in.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Drain blocks until every emitted event has been delivered to subscribers,
// so a short-lived program does not exit before the final state is reported.
func (m *Manager) Drain(ctx context.Context) {
	for {
		m.evMu.Lock()
		idle := len(m.queue) == 0 && !m.draining
		m.evMu.Unlock()
		if idle || ctx.Err() != nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Wait blocks until the instance has finished.
func (m *Manager) Wait(ctx context.Context, id string) (Snapshot, error) {
	m.mu.Lock()
	in, ok := m.insts[id]
	m.mu.Unlock()
	if !ok {
		return Snapshot{}, ErrUnknownInstance
	}
	select {
	case <-in.done:
		s, _ := m.Get(id)
		return s, nil
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

// Focus activates the compositor for an instance, using precise window focus
// when the compositor supports it.
func (m *Manager) Focus(ctx context.Context, id string) error {
	s, ok := m.Get(id)
	if !ok {
		return ErrUnknownInstance
	}
	return m.cfg.Compositor.Activate(ctx, compositor.WindowHint{InstanceID: s.ID, AppID: s.AppID, Title: s.AppName})
}

func (m *Manager) Get(id string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.insts[id]
	if !ok {
		return Snapshot{}, false
	}
	return in.snap, true
}

// List returns all known instances, oldest first.
func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.insts[id].snap)
	}
	return out
}

// HasLive reports whether any instance is still connecting, connected or stopping.
func (m *Manager) HasLive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.insts {
		if in.snap.State.Live() {
			return true
		}
	}
	return false
}

// Remove forgets one finished instance (the UI's Dismiss). Its log file is
// kept on disk. A live instance cannot be removed: stop it first.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.insts[id]
	if !ok {
		return ErrUnknownInstance
	}
	if in.snap.State.Live() {
		return ErrStillLive
	}
	m.forgetLocked(id)
	return nil
}

// ClearFinished forgets every finished instance and returns how many.
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var gone []string
	for _, id := range m.order {
		if !m.insts[id].snap.State.Live() {
			gone = append(gone, id)
		}
	}
	for _, id := range gone {
		m.forgetLocked(id)
	}
	return len(gone)
}

func (m *Manager) forgetLocked(id string) {
	delete(m.insts, id)
	kept := m.order[:0]
	for _, o := range m.order {
		if o != id {
			kept = append(kept, o)
		}
	}
	m.order = kept
}
