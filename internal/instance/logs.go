package instance

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LogStore creates per-instance log files and rotates old ones. The
// directory is 0700 and files are 0600.
type LogStore struct {
	Dir string
	Now func() time.Time
}

func DefaultLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "Waylauncher"), nil
}

// Create makes a new log file for appID and then deletes the oldest logs of
// that app beyond keep, never touching protected paths (live instances).
// File names are "<appID>_<UTC timestamp>_<hex>.log"; app IDs cannot contain
// '_', so the prefix is unambiguous and names sort chronologically.
func (l *LogStore) Create(appID string, keep int, protected map[string]bool) (*os.File, error) {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(l.Dir, 0o700); err != nil {
		return nil, err
	}
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	var f *os.File
	for try := 0; ; try++ {
		var b [2]byte
		rand.Read(b[:])
		name := fmt.Sprintf("%s_%s_%s.log", appID, now().UTC().Format("20060102T150405.000"), hex.EncodeToString(b[:]))
		var err error
		f, err = os.OpenFile(filepath.Join(l.Dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) || try > 5 {
			return nil, err
		}
	}
	l.rotate(appID, keep, protected)
	return f, nil
}

func (l *LogStore) rotate(appID string, keep int, protected map[string]bool) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil || keep < 1 {
		return
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); e.Type().IsRegular() && strings.HasPrefix(n, appID+"_") && strings.HasSuffix(n, ".log") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	excess := len(names) - keep
	for _, n := range names {
		if excess <= 0 {
			break
		}
		path := filepath.Join(l.Dir, n)
		if protected[path] {
			continue
		}
		if os.Remove(path) == nil {
			excess--
		}
	}
}

// readTail returns up to the last n bytes of a file.
func readTail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(st.Size()-n, 0)
	buf := make([]byte, st.Size()-off)
	m, _ := f.ReadAt(buf, off)
	return string(buf[:m])
}

// Tail returns up to the last n bytes of a log file, for display. It starts
// at a line boundary when the file was cut, and returns "" for a missing file.
func Tail(path string, n int64) string {
	s := readTail(path, n)
	if st, err := os.Stat(path); err == nil && st.Size() > n {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}
