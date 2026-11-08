package i18n

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]Tag{
		"en": EnUS, "en_US.UTF-8": EnUS, "en-GB": EnUS,
		"zh": ZhCN, "zh-CN": ZhCN, "zh_CN": ZhCN, "zh-Hans": ZhCN, "zh-Hans-CN": ZhCN, "zh-SG": ZhCN,
		"zh-TW": ZhTW, "zh-Hant": ZhTW, "zh-Hant-TW": ZhTW, "zh-HK": ZhTW, "zh-Hant-HK": ZhTW, "zh_TW.UTF-8": ZhTW,
	}
	for in, want := range cases {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "de", "fr-CA", "C"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestResolveExplicit(t *testing.T) {
	if got := Resolve("zh-TW"); got != ZhTW {
		t.Errorf("got %q", got)
	}
	if !ValidSetting("system") || !ValidSetting("zh-CN") || ValidSetting("fr") {
		t.Error("ValidSetting wrong")
	}
}

func TestTAndN(t *testing.T) {
	defer Set(EnUS)
	Set(EnUS)
	if got := T("settings.language.title"); got != "Language" {
		t.Errorf("got %q", got)
	}
	Set(ZhCN)
	if got := T("settings.language.title"); got != "语言" {
		t.Errorf("got %q", got)
	}
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("missing key: %q", got)
	}
	if got := fill("Remove {name}? {n}", []any{"name", "A", "n", 3}); got != "Remove A? 3" {
		t.Errorf("fill: %q", got)
	}
}

// Every locale must have exactly the keys of en-US, non-empty, with the same
// placeholders; plural keys must come with an .other form.
func TestCatalogsConsistent(t *testing.T) {
	en := Catalog(EnUS)
	for _, tag := range Supported {
		cat := Catalog(tag)
		for k, v := range en {
			tv, ok := cat[k]
			if !ok {
				t.Errorf("%s: missing key %q", tag, k)
				continue
			}
			if tv == "" {
				t.Errorf("%s: empty value for %q", tag, k)
			}
			a, b := Placeholders(v), Placeholders(tv)
			slices.Sort(a)
			slices.Sort(b)
			if !slices.Equal(a, b) {
				t.Errorf("%s: placeholders of %q differ: en %v, %s %v", tag, k, a, tag, b)
			}
		}
		for k := range cat {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: extra key %q not in en-US", tag, k)
			}
		}
	}
	for k := range en {
		if len(k) > 4 && k[len(k)-4:] == ".one" {
			if _, ok := en[k[:len(k)-4]+".other"]; !ok {
				t.Errorf("plural key %q lacks .other", k)
			}
		}
	}
}
