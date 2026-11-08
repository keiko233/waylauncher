package gui

import (
	"errors"

	"github.com/egoist/mygo"
)

func (a *App) openLastLog(appID string) error {
	p, err := a.svc.LastLogPath(appID)
	if err != nil {
		return err
	}
	return mygo.Shell.OpenPath(p)
}

func (a *App) openLog(instID string) error {
	p, err := a.svc.LogPath(instID)
	if err != nil {
		return err
	}
	return mygo.Shell.OpenPath(p)
}

func (a *App) revealLog(instID string) error {
	p, err := a.svc.LogPath(instID)
	if err != nil {
		return err
	}
	mygo.Shell.ShowItemInFolder(p)
	return nil
}

func (a *App) copyText(text string) error {
	if text == "" || len(text) > 4096 {
		return errors.New("nothing to copy")
	}
	mygo.Clipboard.WriteText(text)
	return nil
}

// openLocalNetworkSettings opens the System Settings pane. The launcher
// never changes that setting itself.
func openLocalNetworkSettings() error {
	return mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork")
}
