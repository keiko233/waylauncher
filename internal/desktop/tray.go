package desktop

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"github.com/keiko233/waylauncher/internal/gui"
	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/icon"
	"github.com/keiko233/waylauncher/internal/launcher"
)

// menuBar owns the tray icon and rebuilds its menu from the state.
type menuBar struct {
	d    *desktop
	mu   sync.Mutex
	tray *mygo.Tray
}

func (m *menuBar) setVisible(show bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if show && m.tray == nil {
		t, err := mygo.NewTray(mygo.TrayOptions{
			ID: "waylauncher", Icon: icon.Tray(2), IconIsTemplate: true, ToolTip: "Waylauncher",
		})
		if err != nil {
			log.Println("menu bar item:", err)
			return
		}
		m.tray = t
		m.rebuildLocked(m.d.svc.State())
	}
	if !show && m.tray != nil {
		m.tray.Destroy()
		m.tray = nil
	}
}

func (m *menuBar) update(st launcher.State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tray != nil {
		m.rebuildLocked(st)
	}
}

func item(label string, click func()) *mygo.MenuItem {
	return &mygo.MenuItem{Label: label, Click: func(*mygo.MenuItem, *mygo.Window) { go click() }}
}

func header(label string) *mygo.MenuItem { return &mygo.MenuItem{Label: label, Disabled: true} }

func (m *menuBar) rebuildLocked(st launcher.State) {
	d := m.d
	var items []*mygo.MenuItem

	var pinned []*mygo.MenuItem
	for _, a := range st.Apps {
		if a.Pinned {
			id := a.ID
			it := item(a.Name, func() { d.report(i18n.T("desktop.action.open"), firstErr(d.svc.Open(id))) })
			if a.Live > 0 {
				it.Subtitle = a.StatusLabel
			}
			pinned = append(pinned, it)
		}
	}
	if len(pinned) > 0 {
		items = append(items, header(i18n.T("desktop.tray.pinned")))
		items = append(items, pinned...)
		items = append(items, mygo.Separator())
	}

	var running []*mygo.MenuItem
	for _, in := range st.Instances {
		in := in
		switch {
		case in.Live:
			running = append(running, &mygo.MenuItem{
				Label: "● " + in.AppName + " · " + in.ID, Subtitle: in.Label,
				Submenu: []*mygo.MenuItem{
					item(i18n.T("desktop.action.focus"), func() { _, err := d.svc.Focus(in.ID); d.report(i18n.T("desktop.action.focus"), err) }),
					item(i18n.T("desktop.action.openLog"), func() { d.report(i18n.T("desktop.action.openLog"), openPath(d.svc.LogPath(in.ID))) }),
					mygo.Separator(),
					item(i18n.T("desktop.tray.stop"), func() { d.confirmStop(in) }),
				},
			})
		case in.State == "disconnected" || in.State == "failed":
			running = append(running, &mygo.MenuItem{
				Label: "● " + in.AppName + " · " + strings.ToLower(in.Label), Subtitle: i18n.T("desktop.tray.notRestarted"),
				Submenu: []*mygo.MenuItem{
					item(i18n.T("desktop.action.startAgain"), func() { d.report(i18n.T("desktop.action.startAgain"), firstErr(d.svc.StartAgain(in.ID))) }),
					item(i18n.T("desktop.action.openLog"), func() { d.report(i18n.T("desktop.action.openLog"), openPath(d.svc.LogPath(in.ID))) }),
					item(i18n.T("desktop.action.dismiss"), func() { d.report(i18n.T("desktop.action.dismiss"), d.svc.Dismiss(in.ID)) }),
				},
			})
		}
	}
	if len(running) > 0 {
		items = append(items, header(i18n.T("desktop.tray.running")))
		items = append(items, running...)
		items = append(items, mygo.Separator())
	}

	items = append(items,
		item(i18n.T("desktop.tray.open"), func() { d.showWindow(gui.Nav{View: gui.PageApps}) }),
		item(i18n.T("desktop.tray.diagnostics"), func() { d.showWindow(gui.Nav{View: gui.PageHost}) }),
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleQuit, Label: i18n.T("desktop.tray.quit")},
	)
	m.tray.SetMenu(mygo.NewMenu(items))
	if st.Live > 0 {
		m.tray.SetTitle(fmt.Sprint(st.Live))
		m.tray.SetToolTip(i18n.T("desktop.tray.tooltipRunning", "n", st.Live))
	} else {
		m.tray.SetTitle("")
		m.tray.SetToolTip("Waylauncher")
	}
}

func firstErr[T any](_ T, err error) error { return err }

func openPath(p string, err error) error {
	if err != nil {
		return err
	}
	return mygo.Shell.OpenPath(p)
}

// confirmStop asks before ending a remote application: stopping closes the
// connection, which ends the program on the host, and it is not restarted.
func (d *desktop) confirmStop(in launcher.InstanceInfo) {
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type: mygo.MessageWarning, Message: i18n.T("desktop.stop.title", "app", in.AppName),
		Detail:  i18n.T("desktop.stop.detail.single", "host", in.HostID),
		Buttons: []string{i18n.T("desktop.cancel"), i18n.T("desktop.stop.button")}, DestructiveButtons: []int{1}, CancelButton: 0, DefaultButton: 0,
	})
	if err != nil || res.Button != 1 {
		return
	}
	d.report(i18n.T("desktop.stop.button"), d.svc.Stop(in.ID))
}

// report shows an error from a menu action (menus have no page to show it on).
func (d *desktop) report(what string, err error) {
	if err == nil {
		return
	}
	log.Printf("%s: %v", what, err)
	mygo.Dialog.Error(i18n.T("desktop.report.failed", "what", what), err.Error())
}
