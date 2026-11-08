package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Settings are launcher-wide preferences persisted in settings.toml.
type Settings struct {
	Language                      string `toml:"language" json:"language"`                       // system | en-US | zh-CN | zh-TW
	Theme                         string `toml:"theme" json:"theme"`                             // system | light | dark
	Glass                         string `toml:"glass" json:"glass"`                             // system | clear | tinted
	QuitBehavior                  string `toml:"quitBehavior" json:"quitBehavior"`               // immediate | ask
	MultiInstanceReopen           string `toml:"multiInstanceReopen" json:"multiInstanceReopen"` // focus-recent | new
	TreatNoChildProcessesAsNormal bool   `toml:"treatNoChildProcessesAsNormal" json:"treatNoChildProcessesAsNormal"`
	NotifyOnDisconnect            bool   `toml:"notifyOnDisconnect" json:"notifyOnDisconnect"`
	ShowInMenuBar                 bool   `toml:"showInMenuBar" json:"showInMenuBar"`
	// ShowRunningInDock starts one Dock helper per running application, so
	// the Linux app's own icon shows in the Dock with a running dot.
	ShowRunningInDock bool `toml:"showRunningInDock" json:"showRunningInDock"`
	LogsKeepPerApp    int  `toml:"logsKeepPerApp" json:"logsKeepPerApp"`
}

func DefaultSettings() Settings {
	return Settings{
		Language:                      "system",
		Theme:                         "system",
		Glass:                         "system",
		QuitBehavior:                  "immediate",
		MultiInstanceReopen:           "focus-recent",
		TreatNoChildProcessesAsNormal: true,
		NotifyOnDisconnect:            true,
		ShowInMenuBar:                 true,
		ShowRunningInDock:             true,
		LogsKeepPerApp:                20,
	}
}

func (s Settings) Validate() error {
	var errs []error
	oneOf := func(name, v string, ok ...string) {
		for _, o := range ok {
			if v == o {
				return
			}
		}
		errs = append(errs, fmt.Errorf("%s: %q is not one of %v", name, v, ok))
	}
	oneOf("language", s.Language, "system", "en-US", "zh-CN", "zh-TW")
	oneOf("theme", s.Theme, "system", "light", "dark")
	oneOf("glass", s.Glass, "system", "clear", "tinted")
	oneOf("quitBehavior", s.QuitBehavior, "immediate", "ask")
	oneOf("multiInstanceReopen", s.MultiInstanceReopen, "focus-recent", "new")
	if s.LogsKeepPerApp < 1 {
		errs = append(errs, errors.New("logsKeepPerApp must be at least 1"))
	}
	return errors.Join(errs...)
}

// LoadSettings returns defaults when settings.toml does not exist. Keys that
// are absent keep their default value; unknown keys produce warnings.
func LoadSettings(p Paths) (Settings, []string, error) {
	s := DefaultSettings()
	data, err := os.ReadFile(p.SettingsTOML())
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil, nil
	}
	if err != nil {
		return s, nil, err
	}
	md, err := toml.Decode(string(data), &s)
	if err != nil {
		return DefaultSettings(), nil, fmt.Errorf("settings.toml: %w", err)
	}
	var warnings []string
	for _, k := range md.Undecoded() {
		warnings = append(warnings, "settings.toml: unknown key: "+k.String())
	}
	return s, warnings, s.Validate()
}

// SaveSettings replaces settings.toml atomically with mode 0600.
func SaveSettings(p Paths, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.ConfigDir, ".settings-*.tmp") // 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(p.ConfigDir, "settings.toml"))
}
