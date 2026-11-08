package profile

import (
	"os"
	"path/filepath"
	"time"
)

// Paths locates configuration. Tests point both directories at temp dirs.
type Paths struct {
	ConfigDir string // holds profiles.toml and settings.toml
	// LegacyDir holds the Swift launcher's profiles.json. It is only ever
	// read (and copied into a backup); it is never modified or moved.
	LegacyDir string
	BackupDir string // parent of timestamped backup directories
	// CacheDir holds regenerable data such as discovered application icons
	// (<CacheDir>/<host id>/<hash>.png). Nothing in it is user data.
	CacheDir string
	Now      func() time.Time
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil || cache == "" {
		cache = filepath.Join(home, "Library", "Caches")
	}
	return Paths{
		CacheDir:  filepath.Join(cache, "Waylauncher"),
		ConfigDir: filepath.Join(home, ".config", "waylauncher"),
		LegacyDir: filepath.Join(home, ".config", "vm200-launcher"),
		BackupDir: filepath.Join(home, "Library", "Application Support", "Waylauncher", "Backups"),
	}, nil
}

func (p Paths) ProfilesTOML() string { return filepath.Join(p.ConfigDir, "profiles.toml") }

// ProfilesJSON is the legacy Swift launcher file; empty when no legacy
// directory is configured.
func (p Paths) ProfilesJSON() string {
	if p.LegacyDir == "" {
		return ""
	}
	return filepath.Join(p.LegacyDir, "profiles.json")
}
func (p Paths) SettingsTOML() string { return filepath.Join(p.ConfigDir, "settings.toml") }
func (p Paths) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
