package launcher

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
	"github.com/keiko233/waylauncher/internal/transport/waypipe"
)

// NewEdit returns the editor model for a new application. The first host is
// preselected.
func (s *Service) NewEdit() AppEdit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := AppEdit{IsNew: true, Command: []string{}, Env: []EnvVar{}, Instances: profile.InstancesSingle}
	if len(s.file.Hosts) > 0 {
		e.Host = s.file.Hosts[0].ID
	}
	return e
}

// GetEdit returns the editor model of an existing application.
func (s *Service) GetEdit(appID string) (AppEdit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.file.AppByID(appID)
	if a == nil {
		return AppEdit{}, fmt.Errorf("%w %q", ErrUnknownApp, appID)
	}
	e := AppEdit{ID: a.ID, Name: a.Name, Host: a.Host, Command: append([]string{}, a.Command...), Cwd: a.Cwd,
		Env: []EnvVar{}, Instances: a.Instances, Pinned: a.Pinned}
	for _, k := range sortedKeys(a.Env) {
		e.Env = append(e.Env, EnvVar{Name: k, Value: a.Env[k]})
	}
	return e, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort keeps this dependency-free and the lists are tiny
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// candidate converts the editor model to a profile app and reports env
// problems row by row (a map would lose duplicates and ordering).
func candidate(e AppEdit) (profile.App, []Problem) {
	var problems []Problem
	env := map[string]string{}
	for i, v := range e.Env {
		name := strings.TrimSpace(v.Name)
		switch {
		case name == "" && v.Value == "":
			continue // an untouched empty row
		case !profile.ValidEnvKey(name):
			problems = append(problems, Problem{Field: "env." + strconv.Itoa(i), Message: "Names must start with a letter or “_” and use only letters, digits and “_”."})
		default:
			if _, dup := env[name]; dup {
				problems = append(problems, Problem{Field: "env." + strconv.Itoa(i), Message: "Duplicate variable " + name + "."})
			}
			env[name] = v.Value
		}
	}
	if len(env) == 0 {
		env = nil
	}
	cmd := make([]string, 0, len(e.Command))
	for _, c := range e.Command {
		cmd = append(cmd, c)
	}
	return profile.App{
		ID: strings.TrimSpace(e.ID), Name: strings.TrimSpace(e.Name), Host: e.Host, Command: cmd,
		Cwd: strings.TrimSpace(e.Cwd), Instances: e.Instances, Pinned: e.Pinned, Source: "manual",
		Transport: profile.Transport{MacOS: profile.TransportWaypipe}, Env: env,
	}, problems
}

func fieldOf(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "env"):
		return "env"
	case strings.Contains(m, "name is empty"):
		return "name"
	case strings.Contains(m, "host"):
		return "host"
	case strings.Contains(m, "command"):
		return "command"
	case strings.Contains(m, "cwd"):
		return "cwd"
	case strings.Contains(m, "instances"):
		return "instances"
	case strings.Contains(m, " id "), strings.Contains(m, "app id"):
		return "id"
	}
	return "general"
}

// withCandidate returns a copy of the profiles with the edited app inserted
// (or replacing the existing one), and the app's index.
func (s *Service) withCandidate(e AppEdit) (*profile.File, int, []Problem) {
	app, problems := candidate(e)
	s.mu.RLock()
	f := s.file.Clone()
	s.mu.RUnlock()
	idx := -1
	if !e.IsNew {
		for i := range f.Apps {
			if f.Apps[i].ID == app.ID {
				idx = i
			}
		}
		if idx >= 0 {
			// Keep fields the editor does not show (icon, platforms, transport...).
			old := f.Apps[idx]
			old.Name, old.Host, old.Command, old.Cwd, old.Env = app.Name, app.Host, app.Command, app.Cwd, app.Env
			old.Instances, old.Pinned = app.Instances, app.Pinned
			app = old
			f.Apps[idx] = app
		}
	}
	if idx < 0 {
		f.Apps = append(f.Apps, app)
		idx = len(f.Apps) - 1
	}
	return f, idx, problems
}

// ValidateEdit applies the profile validation rules to the editor model.
func (s *Service) ValidateEdit(e AppEdit) []Problem {
	f, idx, problems := s.withCandidate(e)
	hasEnvRows := len(problems) > 0
	if err := f.Validate(); err != nil {
		prefix := fmt.Sprintf("apps[%d]", idx)
		for _, line := range flatten(err) {
			if !strings.HasPrefix(line, prefix) {
				continue // a problem in another app is not this editor's to show
			}
			msg := line[strings.Index(line, ": ")+2:]
			field := fieldOf(msg)
			if field == "env" && hasEnvRows {
				continue
			}
			problems = append(problems, Problem{Field: field, Message: capitalize(msg)})
		}
	}
	return problems
}

func flatten(err error) []string {
	type multi interface{ Unwrap() []error }
	var out []string
	var walk func(error)
	walk = func(e error) {
		if m, ok := e.(multi); ok {
			for _, c := range m.Unwrap() {
				walk(c)
			}
			return
		}
		out = append(out, e.Error())
	}
	walk(err)
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Preview renders the commands that would run, with the same code the
// launcher uses to start the application.
func (s *Service) Preview(e AppEdit) Preview {
	app, _ := candidate(e)
	s.mu.RLock()
	h := s.file.HostByID(app.Host)
	s.mu.RUnlock()
	target := "<host>"
	if h != nil {
		target = h.SSH
	}
	spec := remote.Spec{Command: app.Command, Cwd: app.Cwd, Env: app.Env}
	if len(spec.Command) == 0 {
		spec.Command = []string{"<command>"}
	}
	p := Preview{Remote: remote.Command(spec)}
	cmd, err := waypipe.Transport{}.Build(transport.Spec{SSHTarget: target, Remote: spec,
		Runtime: runtimePlaceholder})
	if err != nil {
		p.Local = err.Error()
		return p
	}
	args := cmd.Args[:len(cmd.Args)-1] // the last argument is the remote command
	p.Local = "waypipe " + remote.Join(args) + " " + remote.Quote(p.Remote)
	return p
}

// SaveEdit validates and writes profiles.toml (atomically, after backing up
// the previous file). It returns validation problems without saving.
func (s *Service) SaveEdit(e AppEdit) ([]Problem, error) {
	if problems := s.ValidateEdit(e); len(problems) > 0 {
		return problems, nil
	}
	f, _, _ := s.withCandidate(e)
	if _, err := profile.Save(s.cfg.Paths, f); err != nil {
		return nil, err
	}
	s.setFile(f)
	return nil, nil
}

func (s *Service) setFile(f *profile.File) {
	s.mu.Lock()
	s.file = f
	if s.profInfo.Source == "none" {
		s.profInfo.Source = "toml"
	}
	s.profInfo.Error = ""
	s.mu.Unlock()
	s.notify()
}

// SetPinned pins or unpins an application for the menu bar.
func (s *Service) SetPinned(appID string, pinned bool) error {
	s.mu.RLock()
	f := s.file.Clone()
	s.mu.RUnlock()
	a := f.AppByID(appID)
	if a == nil {
		return fmt.Errorf("%w %q", ErrUnknownApp, appID)
	}
	a.Pinned = pinned
	if _, err := profile.Save(s.cfg.Paths, f); err != nil {
		return err
	}
	s.setFile(f)
	return nil
}

// RemoveApp deletes a profile. Running instances must be stopped first.
func (s *Service) RemoveApp(appID string) error {
	for _, sn := range s.mgr.List() {
		if sn.AppID == appID && sn.State.Live() {
			return errors.New("Stop its running instances before removing it.")
		}
	}
	s.mu.RLock()
	f := s.file.Clone()
	s.mu.RUnlock()
	kept := f.Apps[:0]
	found := false
	for _, a := range f.Apps {
		if a.ID == appID {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	if !found {
		return fmt.Errorf("%w %q", ErrUnknownApp, appID)
	}
	f.Apps = kept
	if _, err := profile.Save(s.cfg.Paths, f); err != nil {
		return err
	}
	s.setFile(f)
	return nil
}

// ---- hosts -------------------------------------------------------------------

type HostEdit struct {
	IsNew bool   `json:"isNew"`
	ID    string `json:"id"`
	SSH   string `json:"ssh"`
}

func (s *Service) hostProblems(h HostEdit) (*profile.File, []Problem) {
	h.ID, h.SSH = strings.TrimSpace(h.ID), strings.TrimSpace(h.SSH)
	s.mu.RLock()
	f := s.file.Clone()
	s.mu.RUnlock()
	var problems []Problem
	idx := -1
	for i := range f.Hosts {
		if f.Hosts[i].ID == h.ID {
			idx = i
		}
	}
	switch {
	case h.IsNew && idx >= 0:
		problems = append(problems, Problem{Field: "id", Message: "A host with this ID exists."})
	case !h.IsNew && idx < 0:
		problems = append(problems, Problem{Field: "general", Message: "Unknown host."})
	}
	if idx >= 0 && !h.IsNew {
		f.Hosts[idx].SSH = h.SSH
	} else {
		f.Hosts = append(f.Hosts, profile.Host{ID: h.ID, SSH: h.SSH})
	}
	if err := f.Validate(); err != nil {
		for _, line := range flatten(err) {
			if !strings.HasPrefix(line, "hosts[") {
				continue
			}
			msg := line[strings.Index(line, ": ")+2:]
			field := "ssh"
			if strings.Contains(msg, "id") {
				field = "id"
			}
			problems = append(problems, Problem{Field: field, Message: capitalize(msg)})
		}
	}
	return f, problems
}

func (s *Service) ValidateHost(h HostEdit) []Problem {
	_, p := s.hostProblems(h)
	return p
}

// SaveHost adds or edits a host (an SSH target: user@host or an ssh config
// alias; never a password or key).
func (s *Service) SaveHost(h HostEdit) ([]Problem, error) {
	f, problems := s.hostProblems(h)
	if len(problems) > 0 {
		return problems, nil
	}
	if _, err := profile.Save(s.cfg.Paths, f); err != nil {
		return nil, err
	}
	s.setFile(f)
	return nil, nil
}

func (s *Service) RemoveHost(id string) error {
	s.mu.RLock()
	f := s.file.Clone()
	s.mu.RUnlock()
	for _, a := range f.Apps {
		if a.Host == id {
			return fmt.Errorf("%s still uses this host. Remove or move it first.", a.Name)
		}
	}
	kept := f.Hosts[:0]
	for _, h := range f.Hosts {
		if h.ID != id {
			kept = append(kept, h)
		}
	}
	f.Hosts = kept
	if _, err := profile.Save(s.cfg.Paths, f); err != nil {
		return err
	}
	s.setFile(f)
	return nil
}
