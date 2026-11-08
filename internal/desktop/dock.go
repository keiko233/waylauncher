package desktop

import (
	"log"
	"strconv"
	"sync"

	"github.com/egoist/mygo"

	"github.com/keiko233/waylauncher/internal/dock"
	"github.com/keiko233/waylauncher/internal/launcher"
)

// DockOptions configures the per-application Dock helpers (internal/dock).
// Without a HelperBinary the feature is off.
type DockOptions struct {
	HelperBinary   string // the waylauncher-dock-helper executable to put in each bundle
	BundleDir      string // default ~/Applications/Waylauncher Apps
	BackupDir      string // replaced and removed bundles are moved below this directory
	RuntimeDir     string // holds the helper socket (default $TMPDIR/waylauncher, mode 0700)
	WaylauncherApp string // Waylauncher.app, so a helper can start it when it is not running
	IconBase       string // base directory for relative profile icon paths
	Version        string
}

// dockHost connects the dock controller to the application core and the
// native dialogs.
type dockHost struct {
	d    *desktop
	ctrl *dock.Controller

	mu     sync.Mutex
	asking map[string]bool // applications with a Stop confirmation open
	// devAnswer ("cancel" or "stop") answers the Stop confirmation without a
	// dialog. Development hook for the fake transport only (dev.go).
	devAnswer string
}

func (d *desktop) startDock(o DockOptions) {
	if o.HelperBinary == "" {
		log.Println("dock: no helper binary; per-application Dock icons are off")
		return
	}
	dir := o.RuntimeDir
	if dir == "" {
		dir = dock.DefaultRuntimeDir()
	}
	h := &dockHost{d: d, asking: map[string]bool{}}
	sock := dock.SocketPath(dir)
	ctrl, err := dock.NewController(dock.ControllerConfig{
		Bundles: dock.BundleConfig{
			Dir: o.BundleDir, BackupDir: o.BackupDir, HelperBinary: o.HelperBinary,
			WaylauncherApp: o.WaylauncherApp, Socket: sock, IconBase: o.IconBase, Version: o.Version,
		},
		SocketPath: sock,
		Actions:    h,
	})
	if err != nil {
		log.Println("dock:", err)
		return
	}
	h.ctrl = ctrl
	d.dock = h
	mygo.App.OnQuit(ctrl.Close)
	h.update(d.svc.State())
}

// update hands the current profiles, live counts and setting to the controller.
func (h *dockHost) update(st launcher.State) {
	if h == nil {
		return
	}
	des := dock.Desired{Counts: map[string]int{}, Enabled: st.Settings.ShowRunningInDock}
	for _, a := range st.Apps {
		des.Apps = append(des.Apps, dock.AppSpec{ID: a.ID, Name: a.Name, Icon: a.Icon, Glyph: a.Glyph, Tile: a.Tile})
		des.Counts[a.ID] = a.Live
	}
	h.ctrl.Update(des)
}

// ---- dock.Actions ---------------------------------------------------------------

func (h *dockHost) OpenApp(app string) error {
	_, err := h.d.svc.Open(app)
	return err
}

// FocusApp is a Dock click on a running application: focus its most recent
// instance (Cocoa-Way forward until it can focus one window). It never starts
// an instance.
func (h *dockHost) FocusApp(app string) error {
	log.Printf("dock: focus %s", app)
	return h.d.svc.FocusApp(app)
}

// ConfirmStopApp is Quit from the Dock menu: the same confirmation as
// stopping one instance, for all instances of the application.
func (h *dockHost) ConfirmStopApp(app string) {
	h.mu.Lock()
	if h.asking[app] {
		h.mu.Unlock()
		return
	}
	h.asking[app] = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.asking, app); h.mu.Unlock() }()

	var info *launcher.AppInfo
	st := h.d.svc.State()
	for i := range st.Apps {
		if st.Apps[i].ID == app {
			info = &st.Apps[i]
		}
	}
	if info == nil || info.Live == 0 {
		return
	}
	what := "the application"
	if info.Live > 1 {
		what = strconv.Itoa(info.Live) + " instances"
	}
	h.mu.Lock()
	answer := h.devAnswer
	h.mu.Unlock()
	if answer != "" {
		log.Printf("dock: dev answer %q to the Stop confirmation of %s", answer, app)
		if answer == "stop" {
			_, _ = h.d.svc.StopApp(app)
		}
		return
	}
	mygo.App.Focus()
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type: mygo.MessageWarning, Message: "Stop " + info.Name + "?",
		Detail:  "This ends " + what + " on " + info.Host + ". Unsaved work in it will be lost. It won’t be restarted.",
		Buttons: []string{"Cancel", "Stop"}, DestructiveButtons: []int{1}, CancelButton: 0, DefaultButton: 0,
	})
	if err != nil || res.Button != 1 {
		return // the helper stays in the Dock
	}
	if _, err := h.d.svc.StopApp(app); err != nil {
		h.d.report("Stop", err)
	}
}
