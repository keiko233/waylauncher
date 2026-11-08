package profile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Empty returns a valid file with no hosts or apps, used before the first
// save when neither profiles.toml nor a legacy profiles.json exists.
func Empty() *File { return &File{Version: SchemaVersion} }

// Clone returns a deep copy, so a caller can edit and validate a candidate
// without touching the live configuration.
func (f *File) Clone() *File {
	c := &File{Version: f.Version, Hosts: append([]Host(nil), f.Hosts...)}
	for _, a := range f.Apps {
		a.Command = append([]string(nil), a.Command...)
		a.Platforms = append([]string(nil), a.Platforms...)
		if a.Env != nil {
			env := make(map[string]string, len(a.Env))
			for k, v := range a.Env {
				env[k] = v
			}
			a.Env = env
		}
		c.Apps = append(c.Apps, a)
	}
	return c
}

// Save validates f and replaces profiles.toml atomically (temporary file in
// the same directory, then rename) with mode 0600. An existing profiles.toml
// is first copied to <BackupDir>/<timestamp>/profiles.toml, so every earlier
// version stays recoverable. The returned string is the backup directory, or
// empty when there was nothing to back up.
func Save(p Paths, f *File) (backupDir string, err error) {
	f.Version = SchemaVersion
	f.applyDefaults()
	if err := f.Validate(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return "", err
	}
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		return "", err
	}
	if old, rerr := os.ReadFile(p.ProfilesTOML()); rerr == nil {
		dir, err := newBackupDir(p.BackupDir, p.now().Format("20060102-150405.000"))
		if err != nil {
			return "", fmt.Errorf("backup profiles.toml: %w", err)
		}
		if err := writeExcl(filepath.Join(dir, "profiles.toml"), old); err != nil {
			return "", fmt.Errorf("backup profiles.toml: %w", err)
		}
		backupDir = dir
	}
	tmp, err := os.CreateTemp(p.ConfigDir, ".profiles-*.tmp") // created 0600
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return backupDir, os.Rename(tmp.Name(), p.ProfilesTOML())
}

// newBackupDir creates <parent>/<stamp>, or <stamp>-2, -3... when saves land in
// the same millisecond, so a backup never overwrites an earlier one.
func newBackupDir(parent, stamp string) (string, error) {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		dir := filepath.Join(parent, stamp)
		if n > 1 {
			dir = fmt.Sprintf("%s-%d", dir, n)
		}
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !os.IsExist(err) || n > 1000 {
			return "", err
		}
	}
}
