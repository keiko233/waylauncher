package desktop

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"github.com/keiko233/waylauncher/internal/gui"
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
			it := item(a.Name, func() { d.report("Open", firstErr(d.svc.Open(id))) })
			if a.Live > 0 {
				it.Subtitle = a.StatusLabel
			}
			pinned = append(pinned, it)
		}
	}
	if len(pinned) > 0 {
		items = append(items, header("Pinned"))
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
					item("Focus window", func() { _, err := d.svc.Focus(in.ID); d.report("Focus window", err) }),
					item("Open log", func() { d.report("Open log", openPath(d.svc.LogPath(in.ID))) }),
					mygo.Separator(),
					item("Stop…", func() { d.confirmStop(in) }),
				},
			})
		case in.State == "disconnected" || in.State == "failed":
			running = append(running, &mygo.MenuItem{
				Label: "● " + in.AppName + " · " + strings.ToLower(in.Label), Subtitle: "Not restarted",
				Submenu: []*mygo.MenuItem{
					item("Start again", func() { d.report("Start again", firstErr(d.svc.StartAgain(in.ID))) }),
					item("Open log", func() { d.report("Open log", openPath(d.svc.LogPath(in.ID))) }),
					item("Dismiss", func() { d.report("Dismiss", d.svc.Dismiss(in.ID)) }),
				},
			})
		}
	}
	if len(running) > 0 {
		items = append(items, header("Running"))
		items = append(items, running...)
		items = append(items, mygo.Separator())
	}

	items = append(items,
		item("Open Waylauncher", func() { d.showWindow(gui.Nav{View: gui.PageApps}) }),
		item("Diagnostics…", func() { d.showWindow(gui.Nav{View: gui.PageHost}) }),
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleQuit, Label: "Quit Waylauncher"},
	)
	m.tray.SetMenu(mygo.NewMenu(items))
	if st.Live > 0 {
		m.tray.SetTitle(fmt.Sprint(st.Live))
		m.tray.SetToolTip(fmt.Sprintf("Waylauncher · %d running", st.Live))
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
		Type: mygo.MessageWarning, Message: "Stop " + in.AppName + "?",
		Detail:  "This ends the application on " + in.HostID + ". Unsaved work in it will be lost. It won’t be restarted.",
		Buttons: []string{"Cancel", "Stop"}, DestructiveButtons: []int{1}, CancelButton: 0, DefaultButton: 0,
	})
	if err != nil || res.Button != 1 {
		return
	}
	d.report("Stop", d.svc.Stop(in.ID))
}

// report shows an error from a menu action (menus have no page to show it on).
func (d *desktop) report(what string, err error) {
	if err == nil {
		return
	}
	log.Printf("%s: %v", what, err)
	mygo.Dialog.Error(what+" failed", err.Error())
}
