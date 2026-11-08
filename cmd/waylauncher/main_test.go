package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keiko233/waylauncher/internal/profile"
)

const legacy = `[{"id":"konsole","name":"Linux Terminal","target":"keiko@10.0.0.9","arguments":["konsole","--separate","--nofork"]}]`

var legacyDirs = map[string]string{}

// setup returns config and backup dirs; the legacy dir is registered per cfg
// and appended by runCLI.
func setup(t *testing.T, json string) (cfg, backup string) {
	d := t.TempDir()
	cfg, backup = filepath.Join(d, "cfg"), filepath.Join(d, "bak")
	legacy := filepath.Join(d, "legacy")
	os.MkdirAll(cfg, 0o700)
	os.MkdirAll(legacy, 0o700)
	legacyDirs[cfg] = legacy
	if json != "" {
		os.WriteFile(filepath.Join(legacy, "profiles.json"), []byte(json), 0o600)
	}
	return
}

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	for i, a := range args {
		if a == "--config-dir" && i+1 < len(args) {
			args = append(args, "--legacy-dir", legacyDirs[args[i+1]])
			break
		}
	}
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCheckConfigDryRunWritesNothing(t *testing.T) {
	cfg, bak := setup(t, legacy)
	code, out, _ := runCLI("--check-config", "--dry-run", "--config-dir", cfg, "--backup-dir", bak)
	if code != 0 || !strings.Contains(out, "dry run") || !strings.Contains(out, "result: OK") {
		t.Fatalf("code %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(cfg, "profiles.toml")); err == nil {
		t.Fatal("dry run wrote profiles.toml")
	}
	if _, err := os.Stat(bak); err == nil {
		t.Fatal("dry run created a backup")
	}
}

func TestCheckConfigMigratesAndJSON(t *testing.T) {
	cfg, bak := setup(t, legacy)
	code, out, _ := runCLI("--check-config", "--json", "--config-dir", cfg, "--backup-dir", bak)
	var rep checkReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != 0 {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	if !rep.OK || rep.Migration == nil || !rep.Migration.Applied || len(rep.Apps) != 1 || rep.Apps[0].Host != "host-10-0-0-9" {
		t.Fatalf("%+v", rep)
	}
	if _, err := os.Stat(filepath.Join(cfg, "profiles.toml")); err != nil {
		t.Fatal(err)
	}
	// Second run reads the TOML and needs no migration.
	code, out, _ = runCLI("--check-config", "--config-dir", cfg, "--backup-dir", bak)
	if code != 0 || strings.Contains(out, "migration") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestCheckConfigInvalid(t *testing.T) {
	cfg, bak := setup(t, `[{"id":"a b","name":"x","target":"-h","arguments":[]}]`)
	code, out, _ := runCLI("--check-config", "--dry-run", "--config-dir", cfg, "--backup-dir", bak)
	if code != 1 || !strings.Contains(out, "result: INVALID") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestCheckConfigMissing(t *testing.T) {
	cfg, bak := setup(t, "")
	if code, out, _ := runCLI("--check-config", "--config-dir", cfg, "--backup-dir", bak); code != 1 || !strings.Contains(out, "no profiles") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{{"launch"}, {"bogus"}, {"status", "--dry-run"}} {
		if code, _, _ := runCLI(args...); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}

func TestFakeTransportNeedsIsolatedDirectories(t *testing.T) {
	def := profile.Paths{ConfigDir: "/home/u/.config/waylauncher", LegacyDir: "/home/u/.config/vm200-launcher", BackupDir: "/home/u/bak", CacheDir: "/home/u/cache"}
	good := options{paths: profile.Paths{ConfigDir: "/t/c", LegacyDir: "/t/l", BackupDir: "/t/b", CacheDir: "/t/cache"},
		logDir: "/t/logs", dockBundleDir: "/t/apps", runtimeDir: "/t/rt"}
	if err := checkFakeIsolated(good, def); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*options){
		"config":      func(o *options) { o.paths.ConfigDir = def.ConfigDir },
		"legacy":      func(o *options) { o.paths.LegacyDir = def.LegacyDir },
		"backup":      func(o *options) { o.paths.BackupDir = def.BackupDir },
		"cache":       func(o *options) { o.paths.CacheDir = def.CacheDir },
		"cache-empty": func(o *options) { o.paths.CacheDir = "" },
		"log":         func(o *options) { o.logDir = "" },
		"bundles":     func(o *options) { o.dockBundleDir = "" },
		"runtime":     func(o *options) { o.runtimeDir = "" },
	} {
		o := good
		mut(&o)
		if err := checkFakeIsolated(o, def); err == nil {
			t.Errorf("%s: the real default was accepted", name)
		}
	}
}
