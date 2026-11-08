package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/keiko233/waylauncher/internal/icon"
	"github.com/keiko233/waylauncher/internal/remote"
)

// IconSize is the edge, in pixels, of the PNGs LoadIcons produces.
const IconSize = 512

const (
	maxIconBytes = 1 << 20
	candBatch    = 100
	fetchBatch   = 20
	fetchRounds  = 3

	markCand = "\x01WLC "
	markIcon = "\x01WLI "
)

type iconKind int

const (
	kindPNG iconKind = iota
	kindSVG
)

// iconCand is one icon file on the remote host.
type iconCand struct {
	Path string
	Kind iconKind
	Size int // pixels for PNG; 0 when unknown (pixmaps); ignored for SVG
}

var (
	sizeDirRE = regexp.MustCompile(`^(\d+)x\d+(@\d+x?)?$`)
	numDirRE  = regexp.MustCompile(`^\d+$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
)

// parseIconPath classifies a path by extension and by the icon-theme size
// directory in it (48x48, 48, scalable). Other extensions (xpm) are
// unsupported.
func parseIconPath(p string) (iconCand, bool) {
	c := iconCand{Path: p}
	switch {
	case strings.HasSuffix(p, ".png"):
		c.Kind = kindPNG
	case strings.HasSuffix(p, ".svg"):
		c.Kind = kindSVG
	default:
		return c, false
	}
	segs := strings.Split(p, "/")
	for i := 0; i+1 < len(segs); i++ {
		s := segs[i]
		if m := sizeDirRE.FindStringSubmatch(s); m != nil {
			c.Size, _ = strconv.Atoi(m[1])
			if m[2] != "" { // 48x48@2 is a 96 pixel image
				n, _ := strconv.Atoi(strings.Trim(m[2], "@x"))
				if n > 1 {
					c.Size *= n
				}
			}
		} else if numDirRE.MatchString(s) && i > 0 {
			c.Size, _ = strconv.Atoi(s) // breeze style: apps/48/name.png
		} else if s == "scalable" {
			c.Kind = kindSVG
		}
	}
	return c, true
}

// chooseIcon picks the icon to fetch. A PNG of at least 128 pixels wins
// (the largest not above 512, else the smallest above it); otherwise an SVG
// if svgOK (it rasterises sharply), otherwise the largest small PNG.
// Unknown-size PNGs (pixmaps) count as 48.
func chooseIcon(cands []iconCand, svgOK bool) (iconCand, bool) {
	var best, svg, small iconCand
	var haveBest, haveSVG, haveSmall bool
	for _, c := range cands {
		if c.Kind == kindSVG {
			// Prefer scalable themes' files over unsized ones; first wins ties.
			if !haveSVG {
				svg, haveSVG = c, true
			}
			continue
		}
		sz := c.Size
		if sz == 0 {
			sz = 48
		}
		if sz >= 128 {
			switch {
			case !haveBest:
				best, haveBest = c, true
			case effSize(best) > IconSize && sz < effSize(best):
				best = c
			case sz <= IconSize && sz > effSize(best):
				best = c
			}
		} else if !haveSmall || sz > effSize(small) {
			small, haveSmall = c, true
		}
	}
	switch {
	case haveBest:
		return best, true
	case haveSVG && svgOK:
		return svg, true
	case haveSmall:
		return small, true
	}
	return iconCand{}, false
}

func effSize(c iconCand) int {
	if c.Size == 0 {
		return 48
	}
	return c.Size
}

// iconName normalises an Icon key to a theme name, or reports that it is not
// searchable (absolute paths are handled separately).
func iconName(key string) (string, bool) {
	for _, ext := range []string{".png", ".svg"} {
		key = strings.TrimSuffix(key, ext)
	}
	return key, nameRE.MatchString(key)
}

// candScript lists, for each named icon (positional parameters), the files
// that exist in the icon theme directories. $n is the 1-based argument index.
// It only tests and prints paths.
const candScript = `LC_ALL=C; export LC_ALL
home=${HOME:-}
xh=${XDG_DATA_HOME:-$home/.local/share}
xd=${XDG_DATA_DIRS:-/usr/local/share:/usr/share}
dirs=$(printf %s "$xh:$xd:$home/.local/share/flatpak/exports/share:/var/lib/flatpak/exports/share:/var/lib/snapd/desktop" | tr ':' '\n')
i=0
for name in "$@"; do
  i=$((i+1))
  while IFS= read -r b; do
    [ -n "$b" ] || continue
    for f in "$b"/icons/hicolor/*/apps/"$name".png "$b"/icons/hicolor/*/apps/"$name".svg \
             "$b"/icons/*/*/apps/"$name".png "$b"/icons/*/*/apps/"$name".svg \
             "$b"/icons/*/apps/*/"$name".png "$b"/icons/*/apps/*/"$name".svg \
             "$b"/pixmaps/"$name".png "$b"/pixmaps/"$name".svg; do
      [ -f "$f" ] && printf '\001WLC %s %s\n' "$i" "$f"
    done
  done <<WLEOF
$dirs
WLEOF
  for f in "$home"/.icons/*/*/apps/"$name".png "$home"/.icons/*/*/apps/"$name".svg; do
    [ -f "$f" ] && printf '\001WLC %s %s\n' "$i" "$f"
  done
done
exit 0
`

// fetchScript prints each file (positional parameter) as base64 in frames
//
//	\x01WLI <index> <ok|big|missing> <b64 length>\n<b64>\n
//
// Files over 1MB are not read.
const fetchScript = `LC_ALL=C; export LC_ALL
i=0
for f in "$@"; do
  i=$((i+1))
  if [ ! -f "$f" ]; then printf '\001WLI %s missing 0\n\n' "$i"; continue; fi
  sz=$(wc -c < "$f" 2>/dev/null | tr -d ' ')
  if [ "${sz:-0}" -gt 1048576 ]; then printf '\001WLI %s big 0\n\n' "$i"; continue; fi
  b=$( (base64 < "$f" 2>/dev/null || openssl base64 -A -in "$f" 2>/dev/null) | tr -d '\r\n')
  if [ -z "$b" ]; then printf '\001WLI %s missing 0\n\n' "$i"; continue; fi
  printf '\001WLI %s ok %s\n%s\n' "$i" "${#b}" "$b"
done
exit 0
`

// parseCandidates reads candScript output into candidates by argument index
// (1-based). Malformed lines are ignored.
func parseCandidates(out []byte, n int) map[int][]iconCand {
	res := map[int][]iconCand{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, markCand)
		if i < 0 {
			continue
		}
		idxS, path, ok := strings.Cut(line[i+len(markCand):], " ")
		idx, err := strconv.Atoi(idxS)
		if !ok || err != nil || idx < 1 || idx > n || !strings.HasPrefix(path, "/") {
			continue
		}
		if seen[idxS+path] {
			continue
		}
		seen[idxS+path] = true
		if c, ok := parseIconPath(path); ok {
			res[idx] = append(res[idx], c)
		}
	}
	return res
}

type fetched struct {
	status string // ok | big | missing
	data   []byte
}

// parseFetch decodes fetchScript output into results by argument index.
func parseFetch(out []byte) (map[int]fetched, error) {
	res := map[int]fetched{}
	for len(out) > 0 {
		i := bytes.Index(out, []byte(markIcon))
		if i < 0 {
			break
		}
		out = out[i:]
		line, rest, _ := bytes.Cut(out, []byte("\n"))
		f := strings.Fields(string(line[len(markIcon):]))
		if len(f) != 3 {
			out = rest
			continue
		}
		idx, err1 := strconv.Atoi(f[0])
		n, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || n < 0 || n > maxIconBytes*2 {
			out = rest
			continue
		}
		if len(rest) < n {
			return res, errors.New("discovery: truncated icon output")
		}
		r := fetched{status: f[1]}
		if f[1] == "ok" {
			data, err := base64.StdEncoding.DecodeString(string(rest[:n]))
			if err != nil || len(data) > maxIconBytes {
				r.status = "missing"
			} else {
				r.data = data
			}
		}
		res[idx] = r
		out = rest[n:]
	}
	return res, nil
}

// normalizeIcon turns a fetched PNG or SVG into a 512x512 PNG.
func normalizeIcon(c iconCand, data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("icon decode failed: %v", r)
		}
	}()
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return icon.FromPNG(data, IconSize)
	}
	if c.Kind == kindSVG || bytes.Contains(data[:min(len(data), 512)], []byte("<svg")) {
		raster, err := rasterizeSVG(data)
		if err != nil {
			return nil, err
		}
		return icon.FromPNG(raster, IconSize)
	}
	return nil, errors.New("unsupported icon format")
}

// rasterizeSVG draws an SVG to a 512x512 PNG with the MyGo ui package's
// software renderer, which works without a window. It supports paths, shapes,
// groups, transforms, gradients, strokes, opacity, clips and masks, and not
// text, embedded images, patterns, markers or filters.
func rasterizeSVG(data []byte) ([]byte, error) {
	s, err := ui.ParseSVG(data)
	if err != nil {
		return nil, err
	}
	if w, h := s.Size(); w <= 0 || h <= 0 {
		return nil, errors.New("svg has no size")
	}
	img := s.Image(IconSize, IconSize, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LoadIcons fetches the icons of entries over ssh (a few calls: one to list
// candidate files, then batched base64 fetches), chooses the best file per
// Icon key, normalises it to a 512x512 PNG and sets Entry.IconPNG. Entries
// whose icon is not found or cannot be decoded keep a nil IconPNG so the UI
// can fall back to a generated tile. Remote access is read-only.
func LoadIcons(ctx context.Context, opt Options, entries []Entry) error {
	// Candidate files per distinct icon key.
	var names []string
	byName := map[string]string{} // icon key -> theme name
	cands := map[string][]iconCand{}
	for _, e := range entries {
		k := e.Icon
		if k == "" {
			continue
		}
		if _, done := cands[k]; done {
			continue
		}
		if strings.HasPrefix(k, "/") {
			if c, ok := parseIconPath(k); ok && !strings.ContainsAny(k, "\n\x01") {
				cands[k] = []iconCand{c}
			}
			continue
		}
		if n, ok := iconName(k); ok {
			byName[k] = n
			cands[k] = nil
			names = append(names, n)
		}
	}
	names = uniq(names)
	byTheme := map[string][]iconCand{}
	for lo := 0; lo < len(names); lo += candBatch {
		batch := names[lo:min(lo+candBatch, len(names))]
		res, err := opt.ssh(ctx, "sh -c "+remote.Quote(candScript)+" sh "+remote.Join(batch))
		if err != nil {
			return err
		}
		for idx, cs := range parseCandidates(res.Stdout, len(batch)) {
			byTheme[batch[idx-1]] = append(byTheme[batch[idx-1]], cs...)
		}
	}
	for k, n := range byName {
		cands[k] = byTheme[n]
	}

	// Fetch the chosen file per key; on failure (too big, undecodable) retry
	// with the next best.
	bad := map[string]bool{}
	pngs := map[string][]byte{} // path -> normalised PNG
	for round := 0; round < fetchRounds; round++ {
		want := map[string]iconCand{}
		for _, cs := range cands {
			var usable []iconCand
			for _, c := range cs {
				if !bad[c.Path] {
					usable = append(usable, c)
				}
			}
			if c, ok := chooseIcon(usable, true); ok && pngs[c.Path] == nil {
				want[c.Path] = c
			}
		}
		if len(want) == 0 {
			break
		}
		var paths []string
		for p := range want {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for lo := 0; lo < len(paths); lo += fetchBatch {
			batch := paths[lo:min(lo+fetchBatch, len(paths))]
			res, err := opt.ssh(ctx, "sh -c "+remote.Quote(fetchScript)+" sh "+remote.Join(batch))
			if err != nil {
				return err
			}
			got, _ := parseFetch(res.Stdout)
			for i, p := range batch {
				f, ok := got[i+1]
				if !ok || f.status != "ok" {
					bad[p] = true
					continue
				}
				out, err := normalizeIcon(want[p], f.data)
				if err != nil {
					bad[p] = true
					continue
				}
				pngs[p] = out
			}
		}
	}
	for i := range entries {
		if cs, ok := cands[entries[i].Icon]; ok {
			var usable []iconCand
			for _, c := range cs {
				if !bad[c.Path] {
					usable = append(usable, c)
				}
			}
			if c, ok := chooseIcon(usable, true); ok {
				entries[i].IconPNG = pngs[c.Path]
			}
		}
	}
	return nil
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
