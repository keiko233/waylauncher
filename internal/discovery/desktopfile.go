// Package discovery finds the graphical applications installed on a remote
// host over SSH, read-only. The remote side only lists and prints files; all
// parsing (Desktop Entry files, Exec quoting, icon choice) happens locally.
package discovery

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Entry is one discovered application. It is a suggestion only: nothing is
// created or run until the user imports it.
type Entry struct {
	ID          string // desktop-file ID without ".desktop", e.g. org.kde.konsole
	ProfileID   string // suggested profile ID, matches ^[A-Za-z0-9-]+$, unique per scan
	Name        string // localized when possible
	Comment     string
	Command     []string // argv from Exec with field codes removed; never run through a shell
	Cwd         string   // Path=, only when absolute
	Icon        string   // raw Icon key (name or absolute path)
	Terminal    bool     // Terminal=true: the command expects a terminal
	Categories  []string
	WaylandOnly bool // hint: known not to work through an X11 transport

	// IconPNG is a 512x512 PNG, set by LoadIcons (nil when none was found).
	IconPNG []byte
}

// desktopFile is the parsed [Desktop Entry] group: raw (unescaped) values by
// key, where localized keys keep their "[locale]" suffix.
type desktopFile map[string]string

// parseDesktopFile reads the [Desktop Entry] group; other groups (such as
// "Desktop Action") are ignored. Keys are case sensitive per the spec. The
// first occurrence of a key wins.
func parseDesktopFile(data []byte) desktopFile {
	out := desktopFile{}
	in := false
	seen := false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			in = line == "[Desktop Entry]"
			if in {
				if seen {
					in = false // a second main group is invalid; ignore it
				}
				seen = true
			}
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if _, dup := out[k]; dup || k == "" {
			continue
		}
		out[k] = strings.TrimLeft(v, " \t")
	}
	return out
}

// unescape applies the string escapes of the spec: \s \n \t \r \\.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 's':
				b.WriteByte(' ')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (d desktopFile) bool(key string) bool { return d[key] == "true" || d[key] == "1" }

// list splits a ';' separated list value, honouring "\;" escapes.
func (d desktopFile) list(key string) []string {
	v, ok := d[key]
	if !ok {
		return nil
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(v); i++ {
		switch {
		case v[i] == '\\' && i+1 < len(v) && v[i+1] == ';':
			cur.WriteByte(';')
			i++
		case v[i] == ';':
			if cur.Len() > 0 {
				out = append(out, unescape(cur.String()))
			}
			cur.Reset()
		default:
			cur.WriteByte(v[i])
		}
	}
	if cur.Len() > 0 {
		out = append(out, unescape(cur.String()))
	}
	return out
}

// localized returns the value of key for the first matching locale in
// locales (such as "zh_CN", "zh"), else the unlocalized value. Locales may
// carry @MODIFIER and .ENCODING parts, which are tried from most to least
// specific.
func (d desktopFile) localized(key string, locales []string) string {
	for _, loc := range locales {
		for _, cand := range localeVariants(loc) {
			if v, ok := d[key+"["+cand+"]"]; ok && v != "" {
				return unescape(v)
			}
		}
	}
	return unescape(d[key])
}

// localeVariants expands lang_COUNTRY.ENC@MOD to the keys the spec tries:
// lang_COUNTRY@MOD, lang_COUNTRY, lang@MOD, lang.
func localeVariants(loc string) []string {
	loc = strings.ReplaceAll(loc, "-", "_")
	mod := ""
	if i := strings.IndexByte(loc, '@'); i >= 0 {
		loc, mod = loc[:i], loc[i:]
	}
	if i := strings.IndexByte(loc, '.'); i >= 0 {
		loc = loc[:i]
	}
	lang, country, _ := strings.Cut(loc, "_")
	var out []string
	if country != "" {
		if mod != "" {
			out = append(out, lang+"_"+country+mod)
		}
		out = append(out, lang+"_"+country)
	}
	if mod != "" {
		out = append(out, lang+mod)
	}
	return append(out, lang)
}

// LocalesFor maps a profile language setting ("system", "en-US", "zh-CN",
// "zh-TW") to a preferred-locale list. "system" and unknown values give nil,
// which selects the plain Name.
func LocalesFor(lang string) []string {
	if lang == "" || lang == "system" {
		return nil
	}
	l := strings.ReplaceAll(lang, "-", "_")
	if i := strings.IndexByte(l, '_'); i > 0 {
		return []string{l, l[:i]}
	}
	return []string{l}
}

// shownIn reports whether the OnlyShowIn/NotShowIn keys allow the session.
// With no known desktops (a generic session) any OnlyShowIn restriction
// excludes the entry and NotShowIn excludes nothing.
func shownIn(d desktopFile, desktops []string) bool {
	match := func(list []string) bool {
		for _, a := range list {
			for _, b := range desktops {
				if strings.EqualFold(a, b) {
					return true
				}
			}
		}
		return false
	}
	if only := d.list("OnlyShowIn"); len(only) > 0 && !match(only) {
		return false
	}
	if not := d.list("NotShowIn"); len(not) > 0 && match(not) {
		return false
	}
	return true
}

// parseExec turns an Exec value into argv by the spec's rules: string
// escapes first, then double-quote quoting (inside quotes \" \` \$ \\ are
// escapes), whitespace separation, "%%" for a percent sign. Field codes are
// dropped, and an argument that held only field codes disappears. No shell is
// involved; reserved characters outside quotes are kept literally.
func parseExec(raw string) []string {
	s := unescape(raw)
	var argv []string
	var cur strings.Builder
	quoted, had := false, false // inside quotes; arg has content or quotes
	flush := func() {
		if had {
			argv = append(argv, cur.String())
		}
		cur.Reset()
		had = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted && c == '\\' && i+1 < len(s) && strings.IndexByte("\"`$\\", s[i+1]) >= 0:
			i++
			cur.WriteByte(s[i])
			had = true
		case c == '"':
			quoted = !quoted
			if quoted {
				had = true
			}
		case c == '%' && i+1 < len(s):
			i++
			if s[i] == '%' {
				cur.WriteByte('%')
				had = true
			}
			// any other code (f F u U i c k d D n N v m ...) expands to nothing
		case !quoted && (c == ' ' || c == '\t' || c == '\n'):
			flush()
		default:
			cur.WriteByte(c)
			had = true
		}
	}
	flush()
	return argv
}

var waylandOnlyBins = map[string]bool{
	"foot": true, "footclient": true, "wev": true, "wofi": true, "waybar": true,
	"mako": true, "swaylock": true, "wlogout": true, "wdisplays": true,
	"nwg-look": true, "nwg-drawer": true, "nwg-panel": true, "grim": true,
	"slurp": true, "swayimg": true, "imv-wayland": true, "kanshi": true,
	"hyprpicker": true, "hyprlock": true, "wlr-randr": true, "fuzzel": true,
	"tofi": true, "bemenu": true, "yambar": true, "cage": true,
}

func isWaylandOnly(id string, argv []string) bool {
	if len(argv) > 0 {
		b := argv[0]
		if i := strings.LastIndexByte(b, '/'); i >= 0 {
			b = b[i+1:]
		}
		if waylandOnlyBins[b] {
			return true
		}
	}
	return waylandOnlyBins[strings.ToLower(id)] || strings.HasPrefix(id, "org.codeberg.dnkl.foot")
}

var nonIDRE = regexp.MustCompile(`[^A-Za-z0-9-]+`)

// sanitizeID makes a profile ID (see profile.ValidID) from a desktop ID.
func sanitizeID(id string) string {
	s := strings.Trim(nonIDRE.ReplaceAllString(id, "-"), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		s = "app"
	}
	return s
}

// assignProfileIDs sets unique ProfileIDs, deterministic by desktop ID.
func assignProfileIDs(entries []Entry) {
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return entries[idx[a]].ID < entries[idx[b]].ID })
	used := map[string]bool{}
	for _, i := range idx {
		base := sanitizeID(entries[i].ID)
		id := base
		for n := 2; used[strings.ToLower(id)]; n++ {
			id = base + "-" + strconv.Itoa(n)
		}
		used[strings.ToLower(id)] = true
		entries[i].ProfileID = id
	}
}

// entryFrom builds an Entry from a parsed file; ok is false when the file
// must not be offered.
func entryFrom(id string, d desktopFile, opt Options) (Entry, bool) {
	if d["Type"] != "Application" || d.bool("NoDisplay") || d.bool("Hidden") {
		return Entry{}, false
	}
	if !shownIn(d, opt.Desktops) {
		return Entry{}, false
	}
	argv := parseExec(d["Exec"])
	if len(argv) == 0 || argv[0] == "" {
		return Entry{}, false
	}
	name := d.localized("Name", opt.Locales)
	if strings.TrimSpace(name) == "" {
		return Entry{}, false
	}
	e := Entry{
		ID:          id,
		Name:        strings.TrimSpace(name),
		Comment:     strings.TrimSpace(d.localized("Comment", opt.Locales)),
		Command:     argv,
		Icon:        d["Icon"],
		Terminal:    d.bool("Terminal"),
		Categories:  d.list("Categories"),
		WaylandOnly: isWaylandOnly(id, argv),
	}
	if p := unescape(d["Path"]); strings.HasPrefix(p, "/") {
		e.Cwd = p
	}
	return e, true
}
