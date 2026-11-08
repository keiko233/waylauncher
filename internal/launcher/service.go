package launcher

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/compositor/cocoaway"
	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/instance"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

// Prober is the SSH side: the supervisor's pre-launch probe plus the
// diagnostics check.
type Prober interface {
	instance.Prober
	CheckRemote(ctx context.Context, target string) (remote.Check, error)
}

type Config struct {
	Version    string
	Paths      profile.Paths
	LogDir     string
	Compositor compositor.Compositor
	Transports map[string]transport.Transport
	Prober     Prober
	// CocoaSocket and CocoaApp are shown in diagnostics.
	CocoaSocket string
	CocoaApp    string
	// Exec-free hooks, replaced in tests.
	LocalWaypipeVersion func(ctx context.Context) (path, version string, err error)
}

type Service struct {
	cfg Config
	mgr *instance.Manager

	mu       sync.RWMutex
	file     *profile.File
	profInfo ProfilesInfo
	settings profile.Settings
	cocoa    CocoaInfo
	reports  map[string]HostReport

	subMu sync.Mutex
	subs  map[int]func()
	next  int
}

// New builds the service and loads profiles and settings. With migrate false a
// missing profiles.toml is planned but not written (used by tests and
// read-only tools).
func New(cfg Config, migrate bool) *Service {
	s := &Service{cfg: cfg, reports: map[string]HostReport{}, subs: map[int]func(){}}
	s.settings = profile.DefaultSettings()
	s.mgr = instance.New(instance.Config{
		Compositor: cfg.Compositor,
		Transports: cfg.Transports,
		Prober:     cfg.Prober,
		Settings:   s.Settings,
		Logs:       &instance.LogStore{Dir: cfg.LogDir},
	})
	s.cocoa = CocoaInfo{Socket: cfg.CocoaSocket, Protocol: cocoaway.ProtocolVersion}
	s.Reload(migrate)
	s.mgr.Subscribe(func(instance.Event) { s.notify() })
	return s
}

// Manager exposes the supervisor to the desktop glue (events, quit).
func (s *Service) Manager() *instance.Manager { return s.mgr }

// Subscribe calls fn after any change of the state the UI shows.
func (s *Service) Subscribe(fn func()) (unsubscribe func()) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	id := s.next
	s.next++
	s.subs[id] = fn
	return func() { s.subMu.Lock(); delete(s.subs, id); s.subMu.Unlock() }
}

func (s *Service) notify() {
	s.subMu.Lock()
	fns := make([]func(), 0, len(s.subs))
	for _, fn := range s.subs {
		fns = append(fns, fn)
	}
	s.subMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// Reload re-reads profiles.toml and settings.toml. On first start without a
// profiles.toml, a legacy profiles.json is migrated (copied to the backup
// directory first, and never modified).
func (s *Service) Reload(migrate bool) {
	info := ProfilesInfo{Warnings: []string{}}
	res, err := profile.Load(s.cfg.Paths, !migrate)
	var file *profile.File
	switch {
	case errors.Is(err, profile.ErrNoProfiles):
		file, info.Source = profile.Empty(), "none"
	case res != nil && res.File != nil:
		file, info.Source = res.File, res.Source
		info.Warnings = append(info.Warnings, res.Warnings...)
		if m := res.Migration; m != nil && m.Applied {
			info.Notice = i18n.T("launcher.service.notice.imported", "path", m.BackupPath)
		}
		if err != nil {
			info.Error = err.Error()
		}
	default:
		file = profile.Empty()
		info.Source = "none"
		if err != nil {
			info.Error = err.Error()
		}
	}
	st, warnings, serr := profile.LoadSettings(s.cfg.Paths)
	for _, w := range warnings {
		info.Warnings = append(info.Warnings, w)
	}
	if serr != nil {
		info.Error = strings.TrimSpace(info.Error + "\n" + serr.Error())
		st = profile.DefaultSettings()
	}
	s.mu.Lock()
	s.file, s.profInfo, s.settings = file, info, st
	s.mu.Unlock()
	s.notify()
}

func (s *Service) Settings() profile.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// SaveSettings validates and persists settings.toml.
func (s *Service) SaveSettings(st profile.Settings) error {
	if err := profile.SaveSettings(s.cfg.Paths, st); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	s.notify()
	return nil
}

// ---- state -----------------------------------------------------------------

func (s *Service) State() State {
	s.mu.RLock()
	file, prof, settings, cocoa := s.file, s.profInfo, s.settings, s.cocoa
	reports := make(map[string]HostReport, len(s.reports))
	for k, v := range s.reports {
		reports[k] = v
	}
	s.mu.RUnlock()

	snaps := s.mgr.List()
	st := State{
		Version: s.cfg.Version, ConfigDir: s.cfg.Paths.ConfigDir, LogDir: s.cfg.LogDir,
		Profiles: prof, Settings: settings, Cocoa: cocoa,
		Hosts: []HostInfo{}, Apps: []AppInfo{}, Instances: []InstanceInfo{},
	}
	appsPerHost := map[string]int{}
	for _, a := range file.Apps {
		appsPerHost[a.Host]++
	}
	for _, h := range file.Hosts {
		hi := HostInfo{ID: h.ID, SSH: h.SSH, Apps: appsPerHost[h.ID]}
		if r, ok := reports[h.ID]; ok {
			hi.Checked, hi.CheckedAt = true, r.At
			hi.Reachable = r.Failure == nil
			for _, c := range r.Checks {
				if c.ID == "ssh" && c.Kind != KindOK {
					hi.Reachable = false
				}
			}
		}
		st.Hosts = append(st.Hosts, hi)
	}
	byApp := map[string][]instance.Snapshot{}
	for _, sn := range snaps {
		byApp[sn.AppID] = append(byApp[sn.AppID], sn)
	}
	for _, a := range file.Apps {
		st.Apps = append(st.Apps, appInfo(a, file.HostByID(a.Host), byApp[a.ID]))
	}
	for _, sn := range snaps {
		in := instanceInfo(sn, file)
		st.Instances = append(st.Instances, in)
		if in.Live {
			st.Live++
		} else {
			st.Ended++
		}
	}
	// Live instances first, newest first within each group.
	sort.SliceStable(st.Instances, func(i, j int) bool {
		a, b := st.Instances[i], st.Instances[j]
		if a.Live != b.Live {
			return a.Live
		}
		return a.StartedAt.After(b.StartedAt)
	})
	return st
}

var terminals = map[string]bool{"konsole": true, "foot": true, "alacritty": true, "kitty": true, "xterm": true,
	"gnome-terminal": true, "wezterm": true, "xfce4-terminal": true, "terminator": true, "tilix": true}

var tiles = []string{"#23262E", "#4A4A55", "#1D5FD1", "#0E6F86", "#A44A00", "#6B3FA0", "#1F7A4D", "#B0306A"}

func glyphAndTile(a profile.App) (string, string) {
	h := fnv.New32a()
	h.Write([]byte(a.ID))
	tile := tiles[int(h.Sum32())%len(tiles)]
	if len(a.Command) > 0 && terminals[filepath.Base(a.Command[0])] {
		return ">_", "#23262E"
	}
	var letters []rune
	for _, r := range a.Name {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			letters = append(letters, r)
		}
		if len(letters) == 2 {
			break
		}
	}
	if len(letters) == 0 {
		return "?", tile
	}
	g := strings.ToUpper(string(letters[:1]))
	if len(letters) > 1 {
		g += strings.ToLower(string(letters[1]))
	}
	return g, tile
}

func transportLabel(name string) string {
	if name == profile.TransportWaypipe {
		return "Waypipe"
	}
	return name
}

func transportDetail(name string) string {
	if name == profile.TransportWaypipe {
		return "Waypipe, lz4, no GPU"
	}
	return name
}

func appInfo(a profile.App, h *profile.Host, snaps []instance.Snapshot) AppInfo {
	g, tile := glyphAndTile(a)
	info := AppInfo{
		ID: a.ID, Name: a.Name, Host: a.Host, Command: a.Command, CommandText: remote.Join(a.Command),
		Cwd: a.Cwd, Env: a.Env, Instances: a.Instances, Transport: transportLabel(a.Transport.MacOS),
		Pinned: a.Pinned, Icon: a.Icon, Glyph: g, Tile: tile, StatusKind: KindIdle, StatusLabel: i18n.T("launcher.service.app.notRunning"), Action: "open",
	}
	if h != nil {
		info.HostSSH = h.SSH
	}
	var connected, connecting, stopping int
	var last *instance.Snapshot
	for i := range snaps {
		sn := snaps[i]
		switch sn.State {
		case instance.Connected:
			connected++
		case instance.Connecting:
			connecting++
		case instance.Stopping:
			stopping++
		}
		if !sn.State.Live() {
			last = &snaps[i] // snaps are oldest first, so this ends as the newest finished one
		}
	}
	info.Live = connected + connecting + stopping
	switch {
	case info.Live > 0:
		var parts []string
		if connected > 0 {
			parts = append(parts, i18n.T("launcher.service.app.nConnected", "n", connected))
		}
		if connecting > 0 {
			parts = append(parts, i18n.T("launcher.service.app.nConnecting", "n", connecting))
		}
		if stopping > 0 {
			parts = append(parts, i18n.T("launcher.service.app.nStopping", "n", stopping))
		}
		info.StatusLabel = strings.Join(parts, " · ")
		info.StatusKind = KindOK
		if connected == 0 {
			info.StatusKind = KindWarn
		}
		if a.Instances == profile.InstancesMultiple {
			info.Action = "new-window"
		}
	case last != nil && last.State == instance.Disconnected:
		info.StatusKind, info.StatusLabel, info.Action = KindErr, i18n.T("launcher.service.instance.disconnected")+exitSuffix(last), "start-again"
	case last != nil && last.State == instance.Failed:
		info.StatusKind, info.StatusLabel, info.Action = KindErr, i18n.T("launcher.service.app.failedToStart"), "start-again"
	}
	return info
}

func exitSuffix(sn *instance.Snapshot) string {
	if sn.ExitCode != nil && *sn.ExitCode >= 0 {
		return i18n.T("launcher.service.exitSuffix", "code", *sn.ExitCode)
	}
	return ""
}

func instanceInfo(sn instance.Snapshot, f *profile.File) InstanceInfo {
	in := InstanceInfo{
		ID: sn.ID, AppID: sn.AppID, AppName: sn.AppName, HostID: sn.HostID, State: string(sn.State),
		ExitCode: sn.ExitCode, StartedAt: sn.StartedAt, EndedAt: sn.EndedAt, Live: sn.State.Live(), LogPath: sn.LogPath,
	}
	if a := f.AppByID(sn.AppID); a != nil {
		in.Glyph, in.Tile = glyphAndTile(*a)
		in.Command = remote.Join(a.Command)
		in.Transport = transportDetail(a.Transport.MacOS)
	} else {
		in.Glyph, in.Tile = "?", "#4A4A55"
	}
	if h := f.HostByID(sn.HostID); h != nil {
		in.HostSSH = h.SSH
	}
	switch sn.State {
	case instance.Connecting:
		in.Label, in.Kind, in.Detail = i18n.T("launcher.service.instance.connecting"), KindWarn, i18n.T("launcher.service.instance.connecting.detail")
	case instance.Connected:
		in.Label, in.Kind, in.Detail = i18n.T("launcher.service.instance.connected"), KindOK, i18n.T("launcher.service.instance.connected.detail")
	case instance.Stopping:
		in.Label, in.Kind, in.Detail = i18n.T("launcher.service.instance.stopping"), KindWarn, i18n.T("launcher.service.instance.stopping.detail")
	case instance.Disconnected:
		in.Label, in.Kind, in.Detail = i18n.T("launcher.service.instance.disconnected"), KindErr, sn.Reason
	case instance.Failed:
		in.Label, in.Kind, in.Detail = i18n.T("launcher.service.instance.failed"), KindErr, sn.Reason
	case instance.Exited:
		in.Label, in.Kind, in.Detail = i18n.T("launcher.service.instance.exited"), KindIdle, sn.Reason
		if strings.HasPrefix(sn.Reason, "stopped") {
			in.Label = i18n.T("launcher.service.instance.stopped")
		}
	}
	return in
}

// ---- opening, stopping ----------------------------------------------------------

var (
	ErrUnknownApp = errors.New("unknown application")
	ErrNoHost     = errors.New("the application's host is missing from the profiles")
)

func (s *Service) appAndHost(appID string) (profile.App, profile.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.file.AppByID(appID)
	if a == nil {
		return profile.App{}, profile.Host{}, fmt.Errorf("%w %q", ErrUnknownApp, appID)
	}
	h := s.file.HostByID(a.Host)
	if h == nil {
		return profile.App{}, profile.Host{}, ErrNoHost
	}
	return *a, *h, nil
}

// HasApp reports whether id is an existing profile (deep links accept only these).
func (s *Service) HasApp(id string) bool {
	_, _, err := s.appAndHost(id)
	return err == nil
}

// Open launches an application, or focuses its running instance (falling back
// to bringing Cocoa-Way forward). Multiple-instance apps follow the
// "multiInstanceReopen" setting. The remote application is never restarted
// automatically; this is always a user action.
func (s *Service) Open(appID string) (OpenResult, error) {
	a, h, err := s.appAndHost(appID)
	if err != nil {
		return OpenResult{}, err
	}
	lr, err := s.mgr.Launch(a, h)
	if err != nil {
		return OpenResult{}, err
	}
	return s.afterLaunch(lr)
}

func (s *Service) afterLaunch(lr instance.LaunchResult) (OpenResult, error) {
	res := OpenResult{Action: string(lr.Action), InstanceID: lr.Instance.ID}
	if lr.Action == instance.ActionFocus {
		res.Action = "focused"
		note, err := s.Focus(lr.Instance.ID)
		res.Note = note
		return res, err
	}
	return res, nil
}

// NewWindow always starts another instance of a multiple-instance app.
func (s *Service) NewWindow(appID string) (OpenResult, error) {
	a, h, err := s.appAndHost(appID)
	if err != nil {
		return OpenResult{}, err
	}
	sn, err := s.mgr.NewInstance(a, h)
	if err != nil {
		return OpenResult{}, err
	}
	return OpenResult{Action: "started", InstanceID: sn.ID}, nil
}

// StartAgain starts a NEW instance of the instance's application. It does not
// reattach to or restart the old remote process.
func (s *Service) StartAgain(instanceID string) (OpenResult, error) {
	sn, ok := s.mgr.Get(instanceID)
	if !ok {
		return OpenResult{}, instance.ErrUnknownInstance
	}
	a, h, err := s.appAndHost(sn.AppID)
	if err != nil {
		return OpenResult{}, err
	}
	if a.Instances == profile.InstancesMultiple {
		n, err := s.mgr.NewInstance(a, h)
		if err != nil {
			return OpenResult{}, err
		}
		return OpenResult{Action: "started", InstanceID: n.ID}, nil
	}
	lr, err := s.mgr.Launch(a, h)
	if err != nil {
		return OpenResult{}, err
	}
	return s.afterLaunch(lr)
}

func focusFallbackNote() string { return i18n.T("launcher.service.focusFallback") }

// Focus raises the instance's window when the compositor can, otherwise
// brings Cocoa-Way forward. The note is shown to the user in the fallback case.
func (s *Service) Focus(instanceID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.mgr.Focus(ctx, instanceID); err != nil {
		return "", err
	}
	return focusFallbackNote(), nil
}

func (s *Service) Stop(instanceID string) error { return s.mgr.Stop(instanceID) }

// liveOf returns the live instances of an application, oldest first.
func (s *Service) liveOf(appID string) []instance.Snapshot {
	var out []instance.Snapshot
	for _, sn := range s.mgr.List() {
		if sn.AppID == appID && sn.State.Live() {
			out = append(out, sn)
		}
	}
	return out
}

// LiveCount is the number of live instances of an application (connecting,
// connected or stopping).
func (s *Service) LiveCount(appID string) int { return len(s.liveOf(appID)) }

// ErrNotRunning is returned by FocusApp when the application has no live instance.
var ErrNotRunning = errors.New("the application is not running")

// FocusApp focuses the application's most recent live instance (a Dock icon
// click). It never starts an instance.
func (s *Service) FocusApp(appID string) error {
	live := s.liveOf(appID)
	for i := len(live) - 1; i >= 0; i-- {
		if live[i].State != instance.Stopping {
			_, err := s.Focus(live[i].ID)
			return err
		}
	}
	return ErrNotRunning
}

// StopApp stops every live instance of an application and returns how many
// it asked to stop. Callers confirm with the user first.
func (s *Service) StopApp(appID string) (int, error) {
	var errs []error
	n := 0
	for _, sn := range s.liveOf(appID) {
		if sn.State == instance.Stopping {
			continue
		}
		if err := s.mgr.Stop(sn.ID); err != nil && !errors.Is(err, instance.ErrNotLive) {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

func (s *Service) Dismiss(instanceID string) error {
	err := s.mgr.Remove(instanceID)
	s.notify()
	return err
}

func (s *Service) ClearEnded() int {
	n := s.mgr.ClearFinished()
	s.notify()
	return n
}

// StopAll ends every live instance (used when quitting), bounded by ctx.
func (s *Service) StopAll(ctx context.Context) error { return s.mgr.StopAll(ctx) }

func (s *Service) HasLive() bool { return s.mgr.HasLive() }

// ---- logs -------------------------------------------------------------------

// LogPath returns the log of an instance.
func (s *Service) LogPath(instanceID string) (string, error) {
	sn, ok := s.mgr.Get(instanceID)
	if !ok {
		return "", instance.ErrUnknownInstance
	}
	if sn.LogPath == "" {
		return "", errors.New(i18n.T("launcher.service.err.noInstanceLog"))
	}
	return sn.LogPath, nil
}

// LastLogPath finds the newest log of an application: from this session's
// instances, else from the log directory.
func (s *Service) LastLogPath(appID string) (string, error) {
	snaps := s.mgr.List()
	for i := len(snaps) - 1; i >= 0; i-- {
		if snaps[i].AppID == appID && snaps[i].LogPath != "" {
			return snaps[i].LogPath, nil
		}
	}
	entries, _ := os.ReadDir(s.cfg.LogDir)
	var newest string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, appID+"_") && strings.HasSuffix(n, ".log") && n > newest {
			newest = n
		}
	}
	if newest == "" {
		return "", errors.New(i18n.T("launcher.service.err.noAppLog"))
	}
	return filepath.Join(s.cfg.LogDir, newest), nil
}

// LogTail returns the end of an instance's log for live display.
func (s *Service) LogTail(instanceID string) string {
	sn, ok := s.mgr.Get(instanceID)
	if !ok || sn.LogPath == "" {
		return ""
	}
	return instance.Tail(sn.LogPath, 6<<10)
}

// ---- Cocoa-Way status ------------------------------------------------------------

// RefreshCocoa asks Cocoa-Way for its status. It never starts it.
func (s *Service) RefreshCocoa(ctx context.Context) CocoaInfo {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info := CocoaInfo{Socket: s.cfg.CocoaSocket, Protocol: cocoaway.ProtocolVersion, CheckedAt: time.Now()}
	if st, err := s.cfg.Compositor.Status(ctx); err != nil {
		info.Error = friendlyCocoaError(err)
	} else {
		info.Reachable, info.Version, info.PID = true, st.Version, st.PID
		if st.Socket != "" {
			info.Socket = st.Socket
		}
	}
	s.mu.Lock()
	changed := s.cocoa.Reachable != info.Reachable || s.cocoa.Version != info.Version || s.cocoa.Error != info.Error
	s.cocoa = info
	s.mu.Unlock()
	if changed {
		s.notify()
	}
	return info
}

// friendlyCocoaError shortens the common "not running" failure, whose raw
// text repeats the socket path twice.
func friendlyCocoaError(err error) string {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return i18n.T("launcher.service.cocoaNotRunning")
	}
	return err.Error()
}

// WatchCocoa polls Cocoa-Way until ctx ends.
func (s *Service) WatchCocoa(ctx context.Context, every time.Duration) {
	s.RefreshCocoa(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RefreshCocoa(ctx)
		}
	}
}

// OpenCocoaWay brings Cocoa-Way forward, opening it if needed.
func (s *Service) OpenCocoaWay() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.cfg.Compositor.Activate(ctx, compositor.WindowHint{})
}
