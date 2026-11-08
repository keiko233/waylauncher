package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validTOML = `
version = 2
[[hosts]]
id = "vm"
ssh = "keiko@10.0.0.9"
[[apps]]
id = "konsole"
name = "Linux Terminal"
host = "vm"
command = ["konsole", "--separate"]
cwd = "/home/keiko"
instances = "multiple"
[apps.transport]
macos = "waypipe"
[apps.env]
QT_QPA_PLATFORM = "wayland"
`

func TestParseValid(t *testing.T) {
	f, warnings, err := Parse([]byte(validTOML))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	a := f.AppByID("konsole")
	if a == nil || a.Instances != "multiple" || a.Env["QT_QPA_PLATFORM"] != "wayland" || f.HostByID("vm") == nil {
		t.Fatalf("unexpected parse: %+v", f)
	}
}

func TestParseDefaults(t *testing.T) {
	f, _, err := Parse([]byte("version=2\n[[hosts]]\nid='h'\nssh='h'\n[[apps]]\nid='a'\nname='A'\nhost='h'\ncommand=['x']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a := f.Apps[0]; a.Instances != "single" || a.Transport.MacOS != "waypipe" {
		t.Fatalf("defaults not applied: %+v", a)
	}
}

func TestParseUnknownKeysWarn(t *testing.T) {
	_, warnings, err := Parse([]byte(validTOML + "\n[apps.extra]\nx = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "extra") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestValidate(t *testing.T) {
	base := func() *File {
		f, _, err := Parse([]byte(validTOML))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	tests := []struct {
		name   string
		mutate func(*File)
		want   string
	}{
		{"bad version", func(f *File) { f.Version = 1 }, "version must be 2"},
		{"empty app id", func(f *File) { f.Apps[0].ID = "" }, "invalid id"},
		{"underscore app id", func(f *File) { f.Apps[0].ID = "a_b" }, "invalid id"},
		{"non-ascii app id", func(f *File) { f.Apps[0].ID = "café" }, "invalid id"},
		{"duplicate app id", func(f *File) { f.Apps = append(f.Apps, f.Apps[0]) }, "duplicate app id"},
		{"duplicate host id", func(f *File) { f.Hosts = append(f.Hosts, f.Hosts[0]) }, "duplicate host id"},
		{"ssh leading dash", func(f *File) { f.Hosts[0].SSH = "-oProxyCommand=x" }, "must not start with '-'"},
		{"ssh empty", func(f *File) { f.Hosts[0].SSH = "" }, "empty"},
		{"ssh whitespace", func(f *File) { f.Hosts[0].SSH = "a b" }, "whitespace"},
		{"unknown host", func(f *File) { f.Apps[0].Host = "nope" }, "unknown host"},
		{"empty name", func(f *File) { f.Apps[0].Name = " " }, "name is empty"},
		{"empty command", func(f *File) { f.Apps[0].Command = nil }, "non-empty array"},
		{"empty program", func(f *File) { f.Apps[0].Command = []string{""} }, "non-empty array"},
		{"relative cwd", func(f *File) { f.Apps[0].Cwd = "~/x" }, "absolute"},
		{"bad env key", func(f *File) { f.Apps[0].Env = map[string]string{"1A": "x"} }, "invalid env key"},
		{"env key with dash", func(f *File) { f.Apps[0].Env = map[string]string{"A-B": "x"} }, "invalid env key"},
		{"bad instances", func(f *File) { f.Apps[0].Instances = "many" }, "instances must be"},
		{"bad transport", func(f *File) { f.Apps[0].Transport.MacOS = "vnc" }, "unknown transport"},
		{"wayland+xpra", func(f *File) { f.Apps[0].Display = "wayland"; f.Apps[0].Transport.Windows = "xpra" }, "xpra"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := base()
			tc.mutate(f)
			err := f.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("base should be valid: %v", err)
	}
}

const legacyJSON = `[
 {"id":"konsole","name":"Linux Terminal","target":"keiko@10.0.0.9","arguments":["konsole","--separate","--nofork"]},
 {"id":"foot","name":"Linux foot","target":"keiko@10.0.0.9","arguments":["foot"]},
 {"id":"other","name":"Other","target":"build.example.org","arguments":["xterm"]}
]`

func tempPaths(t *testing.T) Paths {
	t.Helper()
	d := t.TempDir()
	return Paths{
		ConfigDir: filepath.Join(d, "config"),
		LegacyDir: filepath.Join(d, "legacy"),
		BackupDir: filepath.Join(d, "backups"),
		Now:       func() time.Time { return time.Date(2026, 11, 8, 12, 30, 0, 0, time.UTC) },
	}
}

func writeJSON(t *testing.T, p Paths, body string) {
	t.Helper()
	if err := os.MkdirAll(p.LegacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ProfilesJSON(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationJSONToTOML(t *testing.T) {
	p := tempPaths(t)
	writeJSON(t, p, legacyJSON)

	res, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "json-legacy" || res.Migration == nil || !res.Migration.Applied {
		t.Fatalf("unexpected result %+v", res)
	}
	f := res.File
	if len(f.Hosts) != 2 || f.Hosts[0].ID != "host-10-0-0-9" || f.Hosts[1].ID != "host-build-example-org" {
		t.Fatalf("hosts = %+v", f.Hosts)
	}
	if len(f.Apps) != 3 || f.Apps[0].Host != "host-10-0-0-9" || f.Apps[1].Host != "host-10-0-0-9" || f.Apps[2].Host != "host-build-example-org" {
		t.Fatalf("apps = %+v", f.Apps)
	}
	if a := f.Apps[0]; a.Instances != "single" || a.Transport.MacOS != "waypipe" || strings.Join(a.Command, " ") != "konsole --separate --nofork" {
		t.Fatalf("app = %+v", a)
	}

	// The written file loads back identically and is 0600.
	st, err := os.Stat(p.ProfilesTOML())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("toml stat %v %v", st, err)
	}
	res2, err := Load(p, false)
	if err != nil || res2.Source != "toml" || len(res2.File.Apps) != 3 || len(res2.Warnings) != 0 {
		t.Fatalf("reload: %+v %v", res2, err)
	}

	// profiles.json untouched; backup copy exists with the same bytes.
	got, _ := os.ReadFile(p.ProfilesJSON())
	if string(got) != legacyJSON {
		t.Fatal("profiles.json was modified")
	}
	bak, err := os.ReadFile(filepath.Join(p.BackupDir, "20261108-123000", "profiles.json"))
	if err != nil || string(bak) != legacyJSON {
		t.Fatalf("backup: %v", err)
	}
}

func TestMigrationDryRunWritesNothing(t *testing.T) {
	p := tempPaths(t)
	writeJSON(t, p, legacyJSON)
	res, err := Load(p, true)
	if err != nil || res.Migration == nil || res.Migration.Applied {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, path := range []string{p.ProfilesTOML(), p.BackupDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should not exist (err=%v)", path, err)
		}
	}
}

func TestMigrationNeverOverwritesTOML(t *testing.T) {
	p := tempPaths(t)
	writeJSON(t, p, legacyJSON)
	m, err := PlanMigration(p)
	if err != nil {
		t.Fatal(err)
	}
	// A TOML file appears between planning and applying.
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ProfilesTOML(), []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); !errors.Is(err, os.ErrExist) {
		t.Fatalf("want ErrExist, got %v", err)
	}
	if b, _ := os.ReadFile(p.ProfilesTOML()); string(b) != "precious" {
		t.Fatal("existing profiles.toml was overwritten")
	}
	if m.Applied {
		t.Fatal("must not report applied")
	}
}

func TestMigrationInvalidLegacy(t *testing.T) {
	for name, body := range map[string]string{
		"dup id":       `[{"id":"a","name":"A","target":"h","arguments":["x"]},{"id":"a","name":"B","target":"h","arguments":["x"]}]`,
		"dash target":  `[{"id":"a","name":"A","target":"-oFoo","arguments":["x"]}]`,
		"no arguments": `[{"id":"a","name":"A","target":"h","arguments":[]}]`,
		"not json":     `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			p := tempPaths(t)
			writeJSON(t, p, body)
			if _, err := Load(p, false); err == nil {
				t.Fatal("expected error")
			}
			if _, err := os.Stat(p.ProfilesTOML()); err == nil {
				t.Fatal("no toml may be written for invalid input")
			}
		})
	}
}

func TestLoadNothing(t *testing.T) {
	if _, err := Load(tempPaths(t), false); !errors.Is(err, ErrNoProfiles) {
		t.Fatalf("got %v", err)
	}
}

func TestHostSlugUnique(t *testing.T) {
	f := convertLegacy([]legacyProfile{
		{ID: "a", Name: "A", Target: "u1@Host.Example", Arguments: []string{"x"}},
		{ID: "b", Name: "B", Target: "u2@host.example", Arguments: []string{"x"}},
		{ID: "c", Name: "C", Target: "u3@host-example", Arguments: []string{"x"}},
	})
	ids := []string{f.Hosts[0].ID, f.Hosts[1].ID, f.Hosts[2].ID}
	want := []string{"host-host-example", "host-host-example-2", "host-host-example-3"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v", ids)
		}
	}
}

func TestSettings(t *testing.T) {
	p := tempPaths(t)
	s, w, err := LoadSettings(p)
	if err != nil || len(w) != 0 || s != DefaultSettings() {
		t.Fatalf("defaults: %+v %v %v", s, w, err)
	}
	if !s.TreatNoChildProcessesAsNormal || s.LogsKeepPerApp != 20 || s.QuitBehavior != "immediate" {
		t.Fatalf("unexpected defaults %+v", s)
	}
	s.Theme = "dark"
	s.LogsKeepPerApp = 5
	s.TreatNoChildProcessesAsNormal = false
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p.SettingsTOML()); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	got, _, err := LoadSettings(p)
	if err != nil || got != s {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestDockSettingDefaultsOnAndRoundTrips(t *testing.T) {
	if !DefaultSettings().ShowRunningInDock {
		t.Fatal("Dock icons must default to on")
	}
	p := Paths{ConfigDir: t.TempDir()}
	// An existing settings.toml without the key keeps the default.
	os.WriteFile(p.SettingsTOML(), []byte("theme = \"dark\"\n"), 0o600)
	s, _, err := LoadSettings(p)
	if err != nil || !s.ShowRunningInDock {
		t.Fatalf("%+v %v", s, err)
	}
	s.ShowRunningInDock = false
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	got, _, _ := LoadSettings(p)
	if got.ShowRunningInDock {
		t.Fatal("false was not persisted")
	}
}

func TestSettingsPartialUnknownAndInvalid(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.ConfigDir, 0o700)
	os.WriteFile(p.SettingsTOML(), []byte("theme = \"light\"\nbogus = 1\n"), 0o600)
	s, w, err := LoadSettings(p)
	if err != nil || s.Theme != "light" || !s.NotifyOnDisconnect || len(w) != 1 {
		t.Fatalf("%+v %v %v", s, w, err)
	}
	os.WriteFile(p.SettingsTOML(), []byte("theme = \"neon\"\nlogsKeepPerApp = 0\n"), 0o600)
	if _, _, err := LoadSettings(p); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSaveBacksUpAndIsAtomic(t *testing.T) {
	p := tempPaths(t)
	f := Empty()
	f.Hosts = []Host{{ID: "vm", SSH: "keiko@10.0.0.9"}}
	f.Apps = []App{{ID: "foot", Name: "foot", Host: "vm", Command: []string{"foot"}}}
	bak, err := Save(p, f)
	if err != nil || bak != "" {
		t.Fatalf("first save: bak=%q err=%v", bak, err)
	}
	st, err := os.Stat(p.ProfilesTOML())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", st, err)
	}
	first, _ := os.ReadFile(p.ProfilesTOML())

	f2 := f.Clone()
	f2.Apps[0].Name = "Foot terminal"
	f2.Apps[0].Env = map[string]string{"A": "b"}
	bak, err = Save(p, f2)
	if err != nil || bak == "" {
		t.Fatalf("second save: bak=%q err=%v", bak, err)
	}
	if got, _ := os.ReadFile(filepath.Join(bak, "profiles.toml")); string(got) != string(first) {
		t.Fatal("backup differs from the previous file")
	}
	res, err := Load(p, true)
	if err != nil || res.Source != "toml" || res.File.Apps[0].Name != "Foot terminal" || res.File.Apps[0].Env["A"] != "b" {
		t.Fatalf("reload: %+v %v", res, err)
	}
	// No temporary files left behind.
	entries, _ := os.ReadDir(p.ConfigDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
}

func TestSaveRejectsInvalidAndKeepsOldFile(t *testing.T) {
	p := tempPaths(t)
	f := Empty()
	f.Hosts = []Host{{ID: "vm", SSH: "keiko@10.0.0.9"}}
	f.Apps = []App{{ID: "foot", Name: "foot", Host: "vm", Command: []string{"foot"}}}
	if _, err := Save(p, f); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p.ProfilesTOML())
	bad := f.Clone()
	bad.Apps[0].ID = "not valid"
	if _, err := Save(p, bad); err == nil {
		t.Fatal("expected validation error")
	}
	if after, _ := os.ReadFile(p.ProfilesTOML()); string(after) != string(before) {
		t.Fatal("invalid save modified profiles.toml")
	}
	if f.Clone().Apps[0].Command[0] != "foot" {
		t.Fatal("clone")
	}
}

func TestSaveTwiceInOneInstantKeepsBothBackups(t *testing.T) {
	p := tempPaths(t) // fixed clock: every save has the same timestamp
	f := Empty()
	f.Hosts = []Host{{ID: "vm", SSH: "h"}}
	for i := 0; i < 3; i++ {
		f.Hosts[0].SSH = "h" + string(rune('a'+i))
		if _, err := Save(p, f); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	entries, _ := os.ReadDir(p.BackupDir)
	if len(entries) != 2 {
		t.Fatalf("want 2 backups, got %d", len(entries))
	}
}
