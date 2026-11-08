// Package iconpath is the one place that turns a profile's icon setting (a
// local PNG path) into a readable file, so the window and the Dock bundles
// treat the same value the same way.
package iconpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes caps the size of an icon file. Imported icons are 512x512 PNGs
// well below this; anything larger is ignored and the generated glyph tile
// is used instead, in the window and in the Dock alike.
const MaxBytes = 16 << 20

// Resolve expands a profile icon value to a local file path: "~/" is the
// home directory, and a relative path is taken against base (when set). It
// returns an error when the file does not exist, is not a regular file or
// exceeds MaxBytes. Only the file's presence and size are checked; decoding
// is the caller's job.
func Resolve(icon, base string) (path string, fi os.FileInfo, err error) {
	if icon == "" {
		return "", nil, fmt.Errorf("no icon")
	}
	p := icon
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, rest)
	} else if !filepath.IsAbs(p) && base != "" {
		p = filepath.Join(base, p)
	}
	fi, err = os.Stat(p)
	switch {
	case err != nil:
		return p, nil, err
	case !fi.Mode().IsRegular() || fi.Size() > MaxBytes:
		return p, nil, fmt.Errorf("not a regular PNG file under %d MB", MaxBytes>>20)
	}
	return p, fi, nil
}
