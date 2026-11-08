package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var ErrNoProfiles = errors.New("no profiles.toml or profiles.json found")

// legacyProfile is one entry of the Swift launcher's profiles.json.
type legacyProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Target    string   `json:"target"`
	Arguments []string `json:"arguments"`
}

// Migration describes (and, once applied, records) a profiles.json to
// profiles.toml conversion.
type Migration struct {
	File       *File
	SourcePath string // profiles.json, never modified
	TOMLPath   string // created exclusively
	BackupPath string // directory receiving a copy of profiles.json
	Applied    bool
	legacy     []byte
}

// Result is what Load returns.
type Result struct {
	File      *File
	Warnings  []string
	Source    string     // "toml", or "json-legacy" when migration was planned/applied
	Migration *Migration // non-nil when profiles.toml was missing
}

// Load reads profiles.toml, migrating from profiles.json when it is absent.
// With dryRun the migration is planned and validated but nothing is written.
// A validation error still returns a Result so callers can report details.
func Load(p Paths, dryRun bool) (*Result, error) {
	data, err := os.ReadFile(p.ProfilesTOML())
	if err == nil {
		f, warnings, verr := Parse(data)
		return &Result{File: f, Warnings: warnings, Source: "toml"}, verr
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	m, err := PlanMigration(p)
	if err != nil {
		return nil, err
	}
	res := &Result{File: m.File, Source: "json-legacy", Migration: m}
	if dryRun {
		return res, nil
	}
	if err := m.Apply(); err != nil {
		return res, err
	}
	return res, nil
}

// PlanMigration converts profiles.json in memory and validates the result.
func PlanMigration(p Paths) (*Migration, error) {
	if p.ProfilesJSON() == "" {
		return nil, ErrNoProfiles
	}
	data, err := os.ReadFile(p.ProfilesJSON())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoProfiles
	}
	if err != nil {
		return nil, err
	}
	var old []legacyProfile
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, fmt.Errorf("profiles.json: %w", err)
	}
	f := convertLegacy(old)
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("profiles.json cannot be migrated:\n%w", err)
	}
	return &Migration{
		File:       f,
		SourcePath: p.ProfilesJSON(),
		TOMLPath:   p.ProfilesTOML(),
		BackupPath: filepath.Join(p.BackupDir, p.now().Format("20060102-150405")),
		legacy:     data,
	}, nil
}

func convertLegacy(old []legacyProfile) *File {
	f := &File{Version: SchemaVersion}
	hostByTarget := map[string]string{}
	used := map[string]bool{}
	for _, o := range old {
		id, ok := hostByTarget[o.Target]
		if !ok {
			id = uniqueSlug(hostSlug(o.Target), used)
			hostByTarget[o.Target] = id
			f.Hosts = append(f.Hosts, Host{ID: id, SSH: o.Target})
		}
		f.Apps = append(f.Apps, App{
			ID:        o.ID,
			Name:      o.Name,
			Host:      id,
			Command:   o.Arguments,
			Instances: InstancesSingle, // the Swift launcher runs one instance per profile
			Source:    "manual",
			Transport: Transport{MacOS: TransportWaypipe},
		})
	}
	return f
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// hostSlug derives "host-10-0-0-9" from "keiko@10.0.0.9".
func hostSlug(target string) string {
	if i := strings.LastIndex(target, "@"); i >= 0 {
		target = target[i+1:]
	}
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(target), "-"), "-")
	if s == "" {
		s = "default"
	}
	return "host-" + s
}

func uniqueSlug(base string, used map[string]bool) string {
	id := base
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	used[id] = true
	return id
}

// Apply backs up profiles.json, then creates profiles.toml with O_EXCL and
// mode 0600. It never modifies or deletes profiles.json and never overwrites
// an existing profiles.toml. If writing fails, the partial file is removed.
func (m *Migration) Apply() error {
	if err := os.MkdirAll(m.BackupPath, 0o700); err != nil {
		return err
	}
	if err := writeExcl(filepath.Join(m.BackupPath, "profiles.json"), m.legacy); err != nil {
		return fmt.Errorf("backup profiles.json: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString("# Migrated from the legacy profiles.json, which is left in place for the Swift launcher.\n")
	if err := toml.NewEncoder(&buf).Encode(m.File); err != nil {
		return err
	}
	if err := writeExcl(m.TOMLPath, buf.Bytes()); err != nil {
		return fmt.Errorf("write profiles.toml: %w", err)
	}
	m.Applied = true
	return nil
}

// writeExcl creates path exclusively with mode 0600.
func writeExcl(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
