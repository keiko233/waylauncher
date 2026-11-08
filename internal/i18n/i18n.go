// Package i18n holds the launcher's UI translations.
//
// Scope: strings a person reads in the window, menus, tray, dialogs and
// notifications. CLI output, log files, profile/settings keys and raw
// ssh/waypipe output stay English on purpose: they are diagnostic data.
//
// Catalogs are flat JSON files embedded from locales/<tag>/<area>.json
// (several files per locale are merged, so areas can be edited separately).
// English (en-US) is the source of truth and the fallback.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Tag is a supported locale.
type Tag string

const (
	EnUS Tag = "en-US"
	ZhCN Tag = "zh-CN"
	ZhTW Tag = "zh-TW"
)

// Supported lists locales in the order the settings picker shows them.
var Supported = []Tag{EnUS, ZhCN, ZhTW}

// SettingSystem is the settings value that follows the system language.
const SettingSystem = "system"

// Native is the locale's name written in itself (for the language picker).
func (t Tag) Native() string {
	switch t {
	case ZhCN:
		return "简体中文"
	case ZhTW:
		return "繁體中文"
	}
	return "English"
}

//go:embed locales/*/*.json
var localeFS embed.FS

var (
	loadOnce sync.Once
	catalogs map[Tag]map[string]string
	current  atomic.Value // Tag
)

func load() {
	loadOnce.Do(func() {
		catalogs = map[Tag]map[string]string{}
		for _, tag := range Supported {
			m := map[string]string{}
			files, _ := fs.Glob(localeFS, path.Join("locales", string(tag), "*.json"))
			for _, f := range files {
				data, err := localeFS.ReadFile(f)
				if err != nil {
					panic("i18n: " + err.Error())
				}
				var part map[string]string
				if err := json.Unmarshal(data, &part); err != nil {
					panic("i18n: " + f + ": " + err.Error())
				}
				for k, v := range part {
					if _, dup := m[k]; dup {
						panic("i18n: duplicate key " + k + " in " + f)
					}
					m[k] = v
				}
			}
			catalogs[tag] = m
		}
	})
}

// Parse maps a language identifier (zh_CN, zh-Hans-CN, zh-HK, en-GB, …) to a
// supported locale. Any Chinese variant that is not clearly Traditional maps
// to zh-CN; any English maps to en-US.
func Parse(s string) (Tag, bool) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"))
	if i := strings.IndexAny(s, ".@"); i >= 0 { // en_US.UTF-8, de@euro
		s = s[:i]
	}
	switch {
	case s == "en" || strings.HasPrefix(s, "en-"):
		return EnUS, true
	case s == "zh" || strings.HasPrefix(s, "zh-"):
		for _, p := range strings.Split(s, "-")[1:] {
			switch p {
			case "hant", "tw", "hk", "mo":
				return ZhTW, true
			}
		}
		return ZhCN, true
	}
	return "", false
}

// ValidSetting reports whether v is a value of the language setting.
func ValidSetting(v string) bool {
	if v == SettingSystem {
		return true
	}
	for _, t := range Supported {
		if string(t) == v {
			return true
		}
	}
	return false
}

// Resolve picks the locale for a setting value; "system" (or anything
// unknown) goes through the system's preferred languages, then en-US.
func Resolve(setting string) Tag {
	if setting != SettingSystem {
		if t, ok := Parse(setting); ok {
			return t
		}
	}
	for _, l := range SystemLanguages() {
		if t, ok := Parse(l); ok {
			return t
		}
	}
	return EnUS
}

// SystemLanguages returns the user's preferred languages, best first:
// macOS AppleLanguages, then LC_ALL / LC_MESSAGES / LANG.
func SystemLanguages() []string {
	if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
		var langs []string
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.Trim(strings.TrimSpace(line), `(),"`)
			if line != "" {
				langs = append(langs, line)
			}
		}
		if len(langs) > 0 {
			return langs
		}
	}
	var langs []string
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			langs = append(langs, v)
		}
	}
	return langs
}

// Set makes t the language of T and N.
func Set(t Tag) { current.Store(t) }

// Current is the active locale (en-US until Set is called).
func Current() Tag {
	if t, ok := current.Load().(Tag); ok {
		return t
	}
	return EnUS
}

// T returns the translation of key with {name} placeholders replaced from
// alternating name, value arguments: T("x", "name", "Foo"). A key missing from
// the active locale falls back to en-US, then to the key itself.
func T(key string, args ...any) string {
	load()
	s, ok := catalogs[Current()][key]
	if !ok {
		s, ok = catalogs[EnUS][key]
	}
	if !ok {
		return key
	}
	return fill(s, args)
}

// N is T for a counted noun. It looks up key.one / key.other (English) or
// key.other (Chinese has no plural forms) and adds {n} automatically.
func N(key string, n int, args ...any) string {
	load()
	cat := "other"
	if Current() == EnUS && n == 1 {
		cat = "one"
	}
	k := key + "." + cat
	if _, ok := catalogs[Current()][k]; !ok {
		k = key + ".other"
	}
	return T(k, append([]any{"n", n}, args...)...)
}

var placeholderRE = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func fill(s string, args []any) string {
	if len(args) == 0 || !strings.Contains(s, "{") {
		return s
	}
	vals := make(map[string]string, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		name, _ := args[i].(string)
		vals[name] = toString(args[i+1])
	}
	return placeholderRE.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := vals[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case error:
		return x.Error()
	case interface{ String() string }:
		return x.String()
	}
	return fmt.Sprint(v)
}

// Placeholders returns the sorted-by-appearance {name} set of a message
// (used by the catalog consistency test).
func Placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// Catalog exposes a locale's merged keys for tests and tooling.
func Catalog(t Tag) map[string]string {
	load()
	return catalogs[t]
}
