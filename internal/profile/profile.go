// Package profile loads, validates and migrates launcher profiles (TOML
// schema v2) and application settings.
package profile

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const SchemaVersion = 2

// Enumerations.
const (
	InstancesSingle   = "single"
	InstancesMultiple = "multiple"
	TransportWaypipe  = "waypipe"
	TransportXpra     = "xpra"
	TransportWprs     = "wprs"
)

type File struct {
	Version int    `toml:"version"`
	Hosts   []Host `toml:"hosts"`
	Apps    []App  `toml:"apps"`
}

type Host struct {
	ID  string `toml:"id" json:"id"`
	SSH string `toml:"ssh" json:"ssh"` // user@host or an ~/.ssh/config alias
}

type Transport struct {
	MacOS   string `toml:"macos,omitempty"`
	Windows string `toml:"windows,omitempty"`
}

type App struct {
	ID        string            `toml:"id"`
	Name      string            `toml:"name"`
	Host      string            `toml:"host"`
	Command   []string          `toml:"command"`
	Cwd       string            `toml:"cwd,omitempty"`
	Icon      string            `toml:"icon,omitempty"`
	Platforms []string          `toml:"platforms,omitempty"`
	Transport Transport         `toml:"transport"`
	Display   string            `toml:"display,omitempty"` // "wayland" | "x11" | "any"
	Instances string            `toml:"instances,omitempty"`
	Source    string            `toml:"source,omitempty"`
	Pinned    bool              `toml:"pinned,omitempty"`
	Env       map[string]string `toml:"env,omitempty"`
}

var (
	idRE     = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Parse decodes TOML, applies defaults and validates. Unknown keys are
// returned as warnings. The returned File is non-nil whenever decoding
// succeeded, even if validation failed, so callers can still report counts.
func Parse(data []byte) (*File, []string, error) {
	var f File
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, nil, fmt.Errorf("profiles.toml: %w", err)
	}
	var warnings []string
	for _, k := range md.Undecoded() {
		warnings = append(warnings, "unknown key: "+k.String())
	}
	f.applyDefaults()
	return &f, warnings, f.Validate()
}

func (f *File) applyDefaults() {
	for i := range f.Apps {
		a := &f.Apps[i]
		if a.Instances == "" {
			a.Instances = InstancesSingle
		}
		if a.Transport.MacOS == "" {
			a.Transport.MacOS = TransportWaypipe
		}
	}
}

// Validate checks every rule and reports all problems at once.
func (f *File) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if f.Version != SchemaVersion {
		add("version must be %d, got %d", SchemaVersion, f.Version)
	}
	hosts := map[string]bool{}
	for i, h := range f.Hosts {
		where := fmt.Sprintf("hosts[%d]", i)
		if !idRE.MatchString(h.ID) {
			add("%s: invalid id %q (use ASCII letters, digits and '-')", where, h.ID)
		} else if hosts[h.ID] {
			add("%s: duplicate host id %q", where, h.ID)
		}
		hosts[h.ID] = true
		if err := checkSSHTarget(h.SSH); err != nil {
			add("%s (%s): %v", where, h.ID, err)
		}
	}
	apps := map[string]bool{}
	for i, a := range f.Apps {
		where := fmt.Sprintf("apps[%d]", i)
		if !idRE.MatchString(a.ID) {
			add("%s: invalid id %q (use ASCII letters, digits and '-')", where, a.ID)
		} else if apps[a.ID] {
			add("%s: duplicate app id %q", where, a.ID)
		}
		apps[a.ID] = true
		where += " (" + a.ID + ")"
		if strings.TrimSpace(a.Name) == "" {
			add("%s: name is empty", where)
		}
		if !hosts[a.Host] {
			add("%s: unknown host %q", where, a.Host)
		}
		if len(a.Command) == 0 || a.Command[0] == "" {
			add("%s: command must be a non-empty array starting with a program name", where)
		}
		for _, c := range a.Command {
			if strings.ContainsRune(c, 0) {
				add("%s: command contains a NUL byte", where)
				break
			}
		}
		// The path is quoted for the remote shell, so "~" would not expand.
		if a.Cwd != "" && (!strings.HasPrefix(a.Cwd, "/") || strings.ContainsRune(a.Cwd, 0)) {
			add("%s: cwd must be an absolute remote path", where)
		}
		switch a.Instances {
		case InstancesSingle, InstancesMultiple:
		default:
			add("%s: instances must be %q or %q", where, InstancesSingle, InstancesMultiple)
		}
		switch a.Display {
		case "", "any", "x11", "wayland":
		default:
			add("%s: display must be \"wayland\", \"x11\" or \"any\"", where)
		}
		for _, t := range []string{a.Transport.MacOS, a.Transport.Windows} {
			switch t {
			case "", TransportWaypipe, TransportXpra, TransportWprs:
			default:
				add("%s: unknown transport %q", where, t)
			}
		}
		if a.Display == "wayland" && a.Transport.Windows == TransportXpra {
			add("%s: display \"wayland\" cannot use the xpra transport on Windows", where)
		}
		keys := make([]string, 0, len(a.Env))
		for k := range a.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !envKeyRE.MatchString(k) {
				add("%s: invalid env key %q", where, k)
			}
			if strings.ContainsRune(a.Env[k], 0) {
				add("%s: env %s contains a NUL byte", where, k)
			}
		}
	}
	return errors.Join(errs...)
}

// ValidID and ValidEnvKey expose the syntax rules so editors can check a
// field before building a whole profile.
func ValidID(id string) bool      { return idRE.MatchString(id) }
func ValidEnvKey(key string) bool { return envKeyRE.MatchString(key) }

func checkSSHTarget(t string) error {
	switch {
	case t == "":
		return errors.New("ssh target is empty")
	case strings.HasPrefix(t, "-"):
		return errors.New("ssh target must not start with '-'")
	case strings.ContainsAny(t, " \t\r\n\x00"):
		return errors.New("ssh target must not contain whitespace or NUL")
	}
	return nil
}

// HostByID and AppByID return nil when not found.
func (f *File) HostByID(id string) *Host {
	for i := range f.Hosts {
		if f.Hosts[i].ID == id {
			return &f.Hosts[i]
		}
	}
	return nil
}

func (f *File) AppByID(id string) *App {
	for i := range f.Apps {
		if f.Apps[i].ID == id {
			return &f.Apps[i]
		}
	}
	return nil
}
