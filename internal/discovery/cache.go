package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// Cache stores normalised icon PNGs under Dir, one directory per host:
// <Dir>/<hostid>/<first 16 hex of sha256(png)>.png. Equal icons share a
// file, so a re-scan does not grow the cache. Files are 0600, directories
// 0700, and writes are atomic (temp file + rename).
type Cache struct{ Dir string }

func safeHostID(id string) (string, error) {
	if id == "" || id == "." || id == ".." {
		return "", errors.New("discovery: empty cache host id")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return "", errors.New("discovery: invalid cache host id " + id)
		}
	}
	return id, nil
}

// Path returns where Store would put png, without touching the disk.
func (c Cache) Path(hostID string, png []byte) (string, error) {
	h, err := safeHostID(hostID)
	if err != nil {
		return "", err
	}
	if c.Dir == "" {
		return "", errors.New("discovery: no cache directory")
	}
	sum := sha256.Sum256(png)
	return filepath.Join(c.Dir, h, hex.EncodeToString(sum[:])[:16]+".png"), nil
}

// Store writes png into the cache (if absent) and returns the local path to
// record as the profile's icon.
func (c Cache) Store(hostID string, png []byte) (string, error) {
	if len(png) == 0 {
		return "", errors.New("discovery: empty icon")
	}
	p, err := c.Path(hostID, png)
	if err != nil {
		return "", err
	}
	if old, err := os.ReadFile(p); err == nil && string(old) == string(png) {
		return p, nil
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, ".icon-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(png); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return "", err
	}
	return p, nil
}
