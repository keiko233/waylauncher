package dock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/icon"
	"github.com/keiko233/waylauncher/internal/iconpath"
)

// BundleIDPrefix starts the bundle identifier of every generated bundle; the
// profile ID follows. It is also how generated bundles are told apart from
// anything else a user keeps in the same folder.
const BundleIDPrefix = "com.keiko233.waylauncher.app."

// HelperExecutable is the name of the executable inside each bundle.
const HelperExecutable = "waylauncher-dock-helper"

// BundleSubdir is the folder inside ~/Applications that holds the bundles.
const BundleSubdir = "Waylauncher Apps"

const bundleSchema = "1" // bump to regenerate every bundle

// AppSpec is what a bundle is generated from.
type AppSpec struct {
	ID   string
	Name string
	// Icon is the profile's icon: a local PNG path ("~/" and paths relative
	// to BundleConfig.IconBase are accepted). Empty or unreadable: the
	// generated glyph icon is used.
	Icon  string
	Glyph string // generated icon text, as the Waylauncher window shows it
	Tile  string // "#RRGGBB" tile colour of the generated icon
}

type BundleConfig struct {
	Dir          string // where the .app bundles live
	BackupDir    string // parent of timestamped backup directories
	HelperBinary string // the waylauncher-dock-helper executable to copy
	// WaylauncherApp is recorded in each bundle so that the helper can open
	// the right Waylauncher.app when it is not running (optional).
	WaylauncherApp string
	// Socket is the helper socket Waylauncher listens on, recorded in each
	// bundle so that a helper started from Finder or the Dock finds the same
	// one (matters when --runtime-dir moves it). Optional.
	Socket   string
	IconBase string // directory for relative icon paths (optional)
	Version  string
	// Sign signs a finished bundle. Default: ad-hoc codesign.
	Sign func(bundle string) error
	Now  func() time.Time
}

// SyncResult reports what a sync did.
type SyncResult struct {
	Paths    map[string]string // app ID -> bundle path
	Written  []string          // bundles created or regenerated
	Kept     []string          // bundles already up to date
	BackedUp []string          // old bundles moved into the backup directory
	Warnings []string
}

func adhocSign(bundle string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := execx.Run(ctx, "/usr/bin/codesign", "--force", "--sign", "-", bundle)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("codesign: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// BundleNames chooses a unique, file-system-safe bundle name per app. The
// first app with a name keeps it; a later one with the same name (compared
// case-insensitively, as the file system does) gets " (<id>)" appended.
func BundleNames(apps []AppSpec) map[string]string {
	out := make(map[string]string, len(apps))
	used := map[string]bool{}
	for _, a := range apps {
		n := safeName(a.Name)
		if n == "" {
			n = a.ID
		}
		if used[strings.ToLower(n)] {
			n = safeName(n + " (" + a.ID + ")")
		}
		used[strings.ToLower(n)] = true
		out[a.ID] = n + ".app"
	}
	return out
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == ':' || r < 0x20 || r == 0x7f {
			return '-'
		}
		return r
	}, s)
	s = strings.TrimLeft(strings.TrimSpace(s), ".")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return strings.TrimSpace(s)
}

// plist writes the XML property list of a bundle: ordered string, bool pairs.
type plistKV struct {
	Key string
	Val any // string or bool
}

func plistXML(kvs []plistKV) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	for _, kv := range kvs {
		fmt.Fprintf(&b, "\t<key>%s</key>\n", esc(kv.Key))
		switch v := kv.Val.(type) {
		case string:
			fmt.Fprintf(&b, "\t<string>%s</string>\n", esc(v))
		case bool:
			if v {
				b.WriteString("\t<true/>\n")
			} else {
				b.WriteString("\t<false/>\n")
			}
		}
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// InfoPlist is the Info.plist of one application's bundle.
func InfoPlist(a AppSpec, cfg BundleConfig, fingerprint string) []byte {
	ver := cfg.Version
	if ver == "" {
		ver = "0.1.0"
	}
	kvs := []plistKV{
		{"CFBundleIdentifier", BundleIDPrefix + a.ID},
		{"CFBundleName", a.Name},
		{"CFBundleDisplayName", a.Name},
		{"CFBundleExecutable", HelperExecutable},
		{"CFBundlePackageType", "APPL"},
		{"CFBundleIconFile", "AppIcon"},
		{"CFBundleShortVersionString", ver},
		{"CFBundleVersion", ver},
		{"LSMinimumSystemVersion", "12.0"},
		{"NSHighResolutionCapable", true},
		// One helper per application, never two.
		{"LSMultipleInstancesProhibited", true},
		{"LSApplicationCategoryType", "public.app-category.utilities"},
		{"WaylauncherAppID", a.ID},
		{"WaylauncherFingerprint", fingerprint},
	}
	if cfg.WaylauncherApp != "" {
		kvs = append(kvs, plistKV{"WaylauncherApp", cfg.WaylauncherApp})
	}
	if cfg.Socket != "" {
		kvs = append(kvs, plistKV{"WaylauncherSocket", cfg.Socket})
	}
	return plistXML(kvs)
}

var plistStringRE = func(key string) *regexp.Regexp {
	return regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`)
}

var (
	idKeyRE = plistStringRE("CFBundleIdentifier")
	fpKeyRE = plistStringRE("WaylauncherFingerprint")
)

type existing struct{ path, fingerprint string }

// scan finds generated bundles in dir, by app ID.
func scan(dir string) (map[string]existing, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]existing{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".app") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "Contents", "Info.plist"))
		if err != nil {
			continue
		}
		m := idKeyRE.FindSubmatch(data)
		if m == nil || !strings.HasPrefix(string(m[1]), BundleIDPrefix) {
			continue // not ours
		}
		id := strings.TrimPrefix(string(m[1]), BundleIDPrefix)
		ex := existing{path: filepath.Join(dir, e.Name())}
		if f := fpKeyRE.FindSubmatch(data); f != nil {
			ex.fingerprint = string(f[1])
		}
		out[id] = ex
	}
	return out, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolveIcon reads the profile's PNG icon (see package iconpath: the window
// resolves the same value the same way).
func resolveIcon(a AppSpec, base string) (data []byte, warn string) {
	if a.Icon == "" {
		return nil, ""
	}
	p, _, err := iconpath.Resolve(a.Icon, base)
	if err != nil {
		return nil, fmt.Sprintf("%s: icon %s: %v; using the generated icon", a.ID, a.Icon, err)
	}
	data, err = os.ReadFile(p)
	if err != nil {
		return nil, fmt.Sprintf("%s: icon %s: %v; using the generated icon", a.ID, a.Icon, err)
	}
	if _, err := icon.FromPNG(data, 16); err != nil {
		return nil, fmt.Sprintf("%s: icon %s: %v; using the generated icon", a.ID, a.Icon, err)
	}
	return data, ""
}

func makeICNS(a AppSpec, png []byte) ([]byte, error) {
	if png != nil {
		return icon.ICNS(func(n int) ([]byte, error) { return icon.FromPNG(png, n) })
	}
	glyph, tile := a.Glyph, a.Tile
	if glyph == "" {
		glyph = "?"
	}
	if tile == "" {
		tile = "#4A4A55"
	}
	return icon.ICNS(func(n int) ([]byte, error) { return icon.Tile(glyph, tile, n) })
}

func fingerprint(a AppSpec, iconPNG []byte, helperHash string, cfg BundleConfig) string {
	h := sha256.New()
	for _, s := range []string{bundleSchema, a.ID, a.Name, a.Glyph, a.Tile, helperHash, cfg.WaylauncherApp, cfg.Socket, cfg.Version} {
		fmt.Fprintf(h, "%d:%s\x00", len(s), s)
	}
	if iconPNG != nil {
		fmt.Fprintf(h, "png:%x", sha256.Sum256(iconPNG))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// SyncBundles makes cfg.Dir hold exactly one up-to-date bundle per app. A
// bundle is rebuilt only when its name, icon, helper or Waylauncher location
// changed. Whatever it replaces, and the bundle of a removed or renamed app,
// is moved to <BackupDir>/<timestamp>/Waylauncher Apps/ rather than deleted.
// Folders and bundles that are not Waylauncher's (other bundle identifiers)
// are left alone, unless one sits on a name a bundle needs: that is backed up
// too.
func SyncBundles(cfg BundleConfig, apps []AppSpec) (SyncResult, error) {
	res := SyncResult{Paths: map[string]string{}}
	if cfg.Dir == "" || cfg.HelperBinary == "" {
		return res, errors.New("bundle directory or helper binary is not configured")
	}
	helperHash, err := fileHash(cfg.HelperBinary)
	if err != nil {
		return res, fmt.Errorf("helper binary: %w", err)
	}
	if cfg.Sign == nil {
		cfg.Sign = adhocSign
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return res, err
	}
	// Staging leftovers of an interrupted sync are ours and never user data.
	if stale, _ := filepath.Glob(filepath.Join(cfg.Dir, ".staging-*")); len(stale) > 0 {
		for _, s := range stale {
			_ = os.RemoveAll(s)
		}
	}
	staging, err := os.MkdirTemp(cfg.Dir, ".staging-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(staging)

	have, err := scan(cfg.Dir)
	if err != nil {
		return res, err
	}
	names := BundleNames(apps)
	desired := map[string]bool{}
	type build struct {
		spec   AppSpec
		staged string
		final  string
	}
	var builds []build
	var errs []error

	for _, a := range apps {
		desired[a.ID] = true
		final := filepath.Join(cfg.Dir, names[a.ID])
		png, warn := resolveIcon(a, cfg.IconBase)
		if warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
		fp := fingerprint(a, png, helperHash, cfg)
		if ex, ok := have[a.ID]; ok && ex.path == final && ex.fingerprint == fp {
			res.Kept = append(res.Kept, final)
			res.Paths[a.ID] = final
			continue
		}
		staged := filepath.Join(staging, names[a.ID])
		if err := writeBundle(staged, a, png, fp, cfg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.ID, err))
			// Keep using the old bundle if there is one.
			if ex, ok := have[a.ID]; ok {
				res.Paths[a.ID] = ex.path
			}
			delete(desired, a.ID)
			continue
		}
		builds = append(builds, build{a, staged, final})
	}

	stamp := cfg.Now().Format("20060102-150405.000")
	backup := func(path string) {
		dest, err := moveToBackup(cfg.BackupDir, stamp, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("back up %s: %w", filepath.Base(path), err))
			return
		}
		res.BackedUp = append(res.BackedUp, dest)
	}
	rebuilt := map[string]bool{}
	for _, b := range builds {
		rebuilt[b.spec.ID] = true
	}
	// Bundles of removed apps, and of apps whose replacement is ready.
	ids := make([]string, 0, len(have))
	for id := range have {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ex := have[id]
		_, failed := res.Paths[id]
		stillWanted := desired[id] || failed
		if !stillWanted || rebuilt[id] {
			backup(ex.path)
		}
	}
	for _, b := range builds {
		if _, err := os.Lstat(b.final); err == nil { // something else sits on the name
			backup(b.final)
		}
		if err := os.Rename(b.staged, b.final); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.spec.ID, err))
			continue
		}
		res.Written = append(res.Written, b.final)
		res.Paths[b.spec.ID] = b.final
	}
	return res, errors.Join(errs...)
}

func writeBundle(path string, a AppSpec, png []byte, fp string, cfg BundleConfig) error {
	macos := filepath.Join(path, "Contents", "MacOS")
	resources := filepath.Join(path, "Contents", "Resources")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(resources, 0o755); err != nil {
		return err
	}
	if err := copyFile(cfg.HelperBinary, filepath.Join(macos, HelperExecutable), 0o755); err != nil {
		return err
	}
	icns, err := makeICNS(a, png)
	if err != nil {
		return fmt.Errorf("icon: %w", err)
	}
	if err := os.WriteFile(filepath.Join(resources, "AppIcon.icns"), icns, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), InfoPlist(a, cfg, fp), 0o644); err != nil {
		return err
	}
	return cfg.Sign(path)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// moveToBackup renames path to <backupRoot>/<stamp>/Waylauncher Apps/<base>.
func moveToBackup(backupRoot, stamp, path string) (string, error) {
	if backupRoot == "" {
		return "", errors.New("no backup directory configured")
	}
	dir := filepath.Join(backupRoot, stamp, BundleSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	dest := filepath.Join(dir, base)
	for n := 2; ; n++ {
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s-%d.app", strings.TrimSuffix(base, ".app"), n))
	}
	return dest, os.Rename(path, dest)
}

// BundleInfo is what a helper reads from its own bundle.
type BundleInfo struct {
	AppID          string
	Name           string
	WaylauncherApp string
	Socket         string
}

// ReadBundleInfo reads the Info.plist of the bundle that contains the helper
// executable exe (<bundle>/Contents/MacOS/<exe>).
func ReadBundleInfo(exe string) (BundleInfo, error) {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist"))
	if err != nil {
		return BundleInfo{}, err
	}
	get := func(key string) string {
		if m := plistStringRE(key).FindSubmatch(data); m != nil {
			return xmlUnescape(string(m[1]))
		}
		return ""
	}
	return BundleInfo{AppID: get("WaylauncherAppID"), Name: get("CFBundleName"), WaylauncherApp: get("WaylauncherApp"), Socket: get("WaylauncherSocket")}, nil
}

func xmlUnescape(s string) string {
	d := struct {
		Text string `xml:",chardata"`
	}{}
	if err := xml.Unmarshal([]byte("<x>"+s+"</x>"), &d); err != nil {
		return s
	}
	return d.Text
}
