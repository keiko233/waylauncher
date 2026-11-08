package launcher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/discovery"
	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
)

// SourceDiscovered is the prefix of App.Source for imported applications:
// "discovered:<desktop-id>". It is how a re-scan recognises what is already
// imported; the profile itself is the user's after the import.
const SourceDiscovered = "discovered:"

// DiscoverEntry is one application found on a host, as the Discover page
// shows it.
type DiscoverEntry struct {
	ID          string   `json:"id"` // desktop-file ID, e.g. org.kde.konsole
	ProfileID   string   `json:"profileId"`
	Name        string   `json:"name"`
	Comment     string   `json:"comment,omitempty"`
	Command     []string `json:"command"`
	CommandText string   `json:"commandText"`
	Cwd         string   `json:"cwd,omitempty"`
	Terminal    bool     `json:"terminal"`
	WaylandOnly bool     `json:"waylandOnly"`
	// Icon is the cached PNG (a local path), empty until the icons are
	// loaded or when the application has none.
	Icon string `json:"icon,omitempty"`
	// Imported is true when a profile on this host already has
	// Source "discovered:<ID>"; ImportedAs is its profile ID.
	Imported   bool   `json:"imported"`
	ImportedAs string `json:"importedAs,omitempty"`
}

// Discovery is the last scan of one host.
type Discovery struct {
	HostID  string          `json:"hostId"`
	SSH     string          `json:"ssh"`
	Scanned bool            `json:"scanned"` // a scan has succeeded
	At      time.Time       `json:"at"`
	Entries []DiscoverEntry `json:"entries"`
	// Error is the last scan's failure, shown beside the (older) entries.
	Error   string   `json:"error,omitempty"`
	Failure *Failure `json:"failure,omitempty"` // host-key and connection guidance
	// IconError is set when the icons could not be fetched or cached; the
	// entries then show generated tiles.
	IconError string `json:"iconError,omitempty"`
}

type discState struct {
	gen       uint64 // incremented by every scan; older scans' results are dropped
	at        time.Time
	scanned   bool
	entries   []discovery.Entry
	icons     map[string]string // desktop ID -> cached PNG path
	err       string
	failure   *Failure
	iconError string
}

func (s *Service) discoveryOptions(h profile.Host) discovery.Options {
	exec := s.cfg.Exec
	if exec == nil {
		exec = execx.Run
	}
	return discovery.Options{
		SSH: s.cfg.SSHPath, Target: h.SSH, Exec: exec,
		Locales: discovery.LocalesFor(s.Settings().Language),
	}
}

func (s *Service) hostByID(id string) (profile.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.file.HostByID(id)
	if h == nil {
		return profile.Host{}, errors.New(i18n.T("launcher.diag.err.unknownHost", "host", id))
	}
	return *h, nil
}

// ScanHost lists the applications installed on a host with one read-only ssh
// call (BatchMode; host-key checking stays as the user's ssh configures it).
// It starts nothing on the host and never changes the profiles. The result is
// kept for DiscoveryOf and ImportDiscovered. A failed scan keeps the previous
// entries and records the error (with a Copy ssh command hint for host-key
// problems); a cancelled scan changes nothing.
func (s *Service) ScanHost(ctx context.Context, hostID string) error {
	h, err := s.hostByID(hostID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	st := s.discFor(hostID)
	st.gen++
	gen := st.gen
	s.mu.Unlock()

	scan := s.cfg.ScanHook
	if scan == nil {
		scan = discovery.Scan
	}
	entries, err := scan(ctx, s.discoveryOptions(h))
	if ctx.Err() != nil {
		return ctx.Err()
	}

	s.mu.Lock()
	st = s.discFor(hostID)
	if st.gen != gen { // a newer scan owns the result
		s.mu.Unlock()
		return context.Canceled
	}
	if err != nil {
		st.err, st.failure = err.Error(), connectionFailure(err, h)
	} else {
		st.err, st.failure = "", nil
		st.scanned, st.at, st.entries = true, time.Now(), entries
		st.icons, st.iconError = map[string]string{}, ""
	}
	s.mu.Unlock()
	s.notify()
	return err
}

// LoadHostIcons fetches the icons of the last scan (read-only ssh) and stores
// them in the cache directory, normalised PNGs shared by content. It is best
// effort: a failure leaves the entries on generated tiles.
func (s *Service) LoadHostIcons(ctx context.Context, hostID string) error {
	h, err := s.hostByID(hostID)
	if err != nil {
		return err
	}
	s.mu.RLock()
	st := s.disc[hostID]
	if st == nil || !st.scanned {
		s.mu.RUnlock()
		return nil
	}
	gen := st.gen
	entries := append([]discovery.Entry(nil), st.entries...)
	s.mu.RUnlock()

	load := s.cfg.IconsHook
	if load == nil {
		load = discovery.LoadIcons
	}
	loadErr := load(ctx, s.discoveryOptions(h), entries)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cache := discovery.Cache{Dir: s.cfg.Paths.CacheDir}
	icons := map[string]string{}
	var cacheErr error
	for _, e := range entries {
		if len(e.IconPNG) == 0 {
			continue
		}
		p, err := cache.Store(hostID, e.IconPNG)
		if err != nil {
			cacheErr = err
			break
		}
		icons[e.ID] = p
	}

	s.mu.Lock()
	st = s.discFor(hostID)
	if st.gen != gen {
		s.mu.Unlock()
		return context.Canceled
	}
	pngs := make(map[string][]byte, len(entries))
	for _, e := range entries {
		pngs[e.ID] = e.IconPNG
	}
	for i := range st.entries {
		st.entries[i].IconPNG = pngs[st.entries[i].ID]
	}
	st.icons = icons
	switch {
	case loadErr != nil:
		st.iconError = loadErr.Error()
	case cacheErr != nil:
		st.iconError = cacheErr.Error()
	default:
		st.iconError = ""
	}
	s.mu.Unlock()
	s.notify()
	return errors.Join(loadErr, cacheErr)
}

// discFor returns the discovery state of a host; callers hold s.mu.
func (s *Service) discFor(hostID string) *discState {
	if s.disc == nil {
		s.disc = map[string]*discState{}
	}
	st := s.disc[hostID]
	if st == nil {
		st = &discState{}
		s.disc[hostID] = st
	}
	return st
}

// DiscoveryOf returns the last scan of a host with the Imported marks
// computed from the current profiles.
func (s *Service) DiscoveryOf(hostID string) Discovery {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := Discovery{HostID: hostID, Entries: []DiscoverEntry{}}
	if h := s.file.HostByID(hostID); h != nil {
		d.SSH = h.SSH
	}
	st := s.disc[hostID]
	if st == nil {
		return d
	}
	d.Scanned, d.At, d.Error, d.Failure, d.IconError = st.scanned, st.at, st.err, st.failure, st.iconError
	imported := importedOn(s.file, hostID)
	for _, e := range st.entries {
		de := DiscoverEntry{
			ID: e.ID, ProfileID: e.ProfileID, Name: e.Name, Comment: e.Comment,
			Command: e.Command, CommandText: remote.Join(e.Command), Cwd: e.Cwd,
			Terminal: e.Terminal, WaylandOnly: e.WaylandOnly, Icon: st.icons[e.ID],
		}
		if id, ok := imported[e.ID]; ok {
			de.Imported, de.ImportedAs = true, id
		}
		d.Entries = append(d.Entries, de)
	}
	return d
}

// importedOn maps desktop IDs to the profile imported from them on a host.
func importedOn(f *profile.File, hostID string) map[string]string {
	m := map[string]string{}
	for _, a := range f.Apps {
		if a.Host == hostID {
			if id, ok := strings.CutPrefix(a.Source, SourceDiscovered); ok && id != "" {
				m[id] = a.ID
			}
		}
	}
	return m
}

// ImportResult says what ImportDiscovered did.
type ImportResult struct {
	Imported []string `json:"imported"` // new profile IDs
	Skipped  []string `json:"skipped"`  // desktop IDs that were already imported or unknown
}

// ImportDiscovered turns the chosen entries (desktop IDs) of the last scan of
// a host into profiles: Source "discovered:<id>", the entry's name, command
// and working folder, this host, a single instance and the cached icon. It is
// the only way a scan result becomes a profile, and only on request. An entry
// that is already imported is skipped and its profile is never touched, so
// edits survive any number of re-scans. The file is saved with the usual
// atomic write after copying the previous one to the backup directory.
func (s *Service) ImportDiscovered(hostID string, ids []string) (ImportResult, error) {
	res := ImportResult{Imported: []string{}, Skipped: []string{}}
	h, err := s.hostByID(hostID)
	if err != nil {
		return res, err
	}
	s.mu.RLock()
	st := s.disc[hostID]
	var entries []discovery.Entry
	if st != nil {
		entries = append(entries, st.entries...)
	}
	f := s.file.Clone()
	s.mu.RUnlock()
	byID := map[string]discovery.Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}

	have := importedOn(f, hostID)
	used := map[string]bool{}
	for _, a := range f.Apps {
		used[strings.ToLower(a.ID)] = true
	}
	cache := discovery.Cache{Dir: s.cfg.Paths.CacheDir}
	seen := map[string]bool{}
	for _, id := range ids {
		e, ok := byID[id]
		if !ok || seen[id] {
			res.Skipped = append(res.Skipped, id)
			continue
		}
		seen[id] = true
		if _, dup := have[id]; dup {
			res.Skipped = append(res.Skipped, id)
			continue
		}
		app := profile.App{
			ID: uniqueProfileID(used, e.ProfileID), Name: e.Name, Host: h.ID,
			Command: append([]string(nil), e.Command...), Cwd: e.Cwd,
			Instances: profile.InstancesSingle, Source: SourceDiscovered + e.ID,
			Transport: profile.Transport{MacOS: profile.TransportWaypipe},
		}
		if e.WaylandOnly {
			app.Display = "wayland"
		}
		if len(e.IconPNG) > 0 {
			// The cached file is the profile's icon; a failure to cache only
			// costs the icon, never the import.
			if p, err := cache.Store(hostID, e.IconPNG); err == nil {
				app.Icon = p
			}
		}
		f.Apps = append(f.Apps, app)
		res.Imported = append(res.Imported, app.ID)
	}
	if len(res.Imported) == 0 {
		return res, nil
	}
	if _, err := profile.Save(s.cfg.Paths, f); err != nil {
		return ImportResult{Imported: []string{}, Skipped: res.Skipped}, err
	}
	s.setFile(f)
	return res, nil
}

// uniqueProfileID returns base, or base-2, base-3… when taken (compared
// case-insensitively), and marks the result as used.
func uniqueProfileID(used map[string]bool, base string) string {
	if base == "" {
		base = "app"
	}
	id := base
	for n := 2; used[strings.ToLower(id)]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	used[strings.ToLower(id)] = true
	return id
}

// connectionFailure explains a scan error that has next steps: an untrusted
// or changed host key, an unreachable host or an authentication failure. The
// launcher never accepts host keys itself; the user runs the ssh command.
func connectionFailure(err error, h profile.Host) *Failure {
	switch kind := remote.KindOf(err); kind {
	case remote.KindHostKeyUnknown:
		return &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.untrusted.title"),
			Message: i18n.T("launcher.diag.fail.untrusted.message", "target", h.SSH), SSHCommand: SSHCommand(h.SSH)}
	case remote.KindHostKeyChanged:
		return &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.changed.title"),
			Message: i18n.T("launcher.diag.fail.changed.message", "target", h.SSH), SSHCommand: SSHCommand(h.SSH)}
	case remote.KindUnreachable, remote.KindTimeout:
		return &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.unreachable.title"),
			Message: i18n.T("launcher.diag.fail.unreachable.message"), SSHCommand: SSHCommand(h.SSH)}
	case remote.KindAuth:
		return &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.auth.title"),
			Message: i18n.T("launcher.diag.fail.auth.message"), SSHCommand: SSHCommand(h.SSH)}
	}
	return nil
}
