package desktop

import (
	"context"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/gui"
	"github.com/keiko233/waylauncher/internal/instance"
	"github.com/keiko233/waylauncher/internal/launcher"
	"github.com/keiko233/waylauncher/internal/profile"
)

// Scheme is the deep link scheme: waylauncher://launch/<profile id>.
const Scheme = "waylauncher"

type Options struct {
	Service *launcher.Service
	Version string
	// CocoaIcons are candidate paths of Cocoa-Way's icon (PNG), tried in
	// order; the icon is read at run time and never bundled.
	CocoaIcons []string
	// Dock configures the per-application Dock helpers.
	Dock DockOptions
	// Dev enables the development-only command hooks (see dev.go); set only
	// with WAYLAUNCHER_FAKE_TRANSPORT=1.
	Dev bool
}

type desktop struct {
	svc  *launcher.Service
	bar  *menuBar
	gui  *gui.App
	dock *dockHost
	dev  bool

	mu       sync.Mutex
	win      *mygo.Window
	lastSet  profile.Settings
	quitting atomic.Bool // set once quit is confirmed and apps are stopped
	quitBusy atomic.Bool // a quit confirmation or shutdown is in progress
}

// Run starts the application and returns when it quits. It must be called
// from the main goroutine.
func Run(o Options) error {
	d := &desktop{svc: o.Service, gui: gui.New(o.Service, gui.Options{CocoaIcons: o.CocoaIcons}), dev: o.Dev}
	d.bar = &menuBar{d: d}

	// One running instance; later launches (and deep links) reach it.
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	mygo.App.SetName("Waylauncher")
	mygo.App.SetVersion(o.Version)

	mygo.App.OnSecondInstance(func(args []string, _ string) {
		if d.dev && d.devCommand(args) {
			return
		}
		d.showWindow(gui.Nav{View: gui.PageApps})
	})
	mygo.App.OnOpenURL(d.handleURL)
	mygo.App.OnNotificationClick(func(id string) { d.showWindow(d.navFor(id)) })
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			d.showWindow(gui.Nav{})
		}
	})
	// Closing the window hides it: quitting must never be a side effect of
	// that, because it would end every running remote application.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(d.beforeQuit)

	d.lastSet = o.Service.Settings()
	d.startDock(o.Dock)
	mygo.Theme.SetSource(themeSource(d.lastSet.Theme))
	mygo.App.SetMenu(appMenu(d))

	mygo.App.WhenReady(func() {
		d.createWindow()
		d.bar.setVisible(d.lastSet.ShowInMenuBar)
		d.startWatching()
	})
	return mygo.App.Run()
}

func themeSource(s string) mygo.ThemeSource {
	switch s {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}

// glassFor maps the Glass setting to a window material. MyGo cannot read the
// Liquid Glass slider of System Settings, so "system" uses the regular glass.
func glassFor(s string) mygo.Vibrancy {
	if s == "clear" {
		return mygo.VibrancyGlassClear
	}
	return mygo.VibrancyGlass
}

// glassTint is the colour laid over the window glass: only "tinted" has one.
func glassTint(s string) string {
	if s == "tinted" {
		return "light-dark(rgba(0, 96, 223, 0.16), rgba(64, 140, 255, 0.18))"
	}
	return ""
}

func (d *desktop) createWindow() {
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "Waylauncher", Width: 1180, Height: 760, MinWidth: 900, MinHeight: 560,
		TitleBarStyle: mygo.TitleBarHiddenInset, TitleBarHeight: 52,
		Vibrancy: glassFor(d.lastSet.Glass), VibrancyTint: glassTint(d.lastSet.Glass),
		StateKey: "main", Content: ui.View(d.gui.View),
	})
	d.gui.Attach(w)
	w.OnClose(func(e *mygo.CloseEvent) {
		if !d.quitting.Load() && !d.quitBusy.Load() {
			e.PreventDefault()
			w.Hide()
		}
	})
	d.mu.Lock()
	d.win = w
	d.mu.Unlock()
}

// showWindow brings the window forward and asks it to show a page.
func (d *desktop) showWindow(n gui.Nav) {
	d.mu.Lock()
	w := d.win
	d.mu.Unlock()
	if w == nil {
		return
	}
	w.Show()
	w.Restore()
	w.Focus()
	mygo.App.Focus()
	if n.View != "" {
		d.gui.Navigate(n)
	}
}

func (d *desktop) windowVisible() bool {
	d.mu.Lock()
	w := d.win
	d.mu.Unlock()
	return w != nil && w.IsVisible()
}

func (d *desktop) navFor(notificationID string) gui.Nav {
	if id, ok := strings.CutPrefix(notificationID, "instance:"); ok {
		return gui.Nav{View: gui.PageRunning, ID: id}
	}
	return gui.Nav{View: gui.PageRunning}
}

// handleURL accepts waylauncher://launch/<id> for an existing profile only.
// The URL never supplies a command, host or any other value.
func (d *desktop) handleURL(raw string) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != Scheme || u.Host != "launch" {
		log.Printf("ignored URL %q", raw)
		return
	}
	id := strings.Trim(u.Path, "/")
	if !profile.ValidID(id) || !d.svc.HasApp(id) {
		go mygo.Dialog.Error("Unknown application", "There is no application “"+truncate(id, 40)+"” in your profiles.")
		return
	}
	go func() {
		if _, err := d.svc.Open(id); err != nil {
			mygo.Dialog.Error("Couldn’t open "+id, err.Error())
		}
	}()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// startWatching connects the service to the window, the menu bar, notifications
// and the settings that need the desktop (theme, glass, menu bar item).
func (d *desktop) startWatching() {
	go d.svc.WatchCocoa(context.Background(), 5*time.Second)
	d.svc.Manager().Subscribe(d.onInstanceEvent)
	d.svc.Subscribe(func() {
		st := d.svc.State()
		d.gui.Refresh()
		d.bar.update(st)
		d.applySettings(st.Settings)
		d.dock.update(st)
	})
}

func (d *desktop) applySettings(s profile.Settings) {
	d.mu.Lock()
	prev := d.lastSet
	d.lastSet = s
	w := d.win
	d.mu.Unlock()
	if s.Theme != prev.Theme {
		mygo.Theme.SetSource(themeSource(s.Theme))
	}
	if s.Glass != prev.Glass && w != nil {
		w.SetVibrancy(glassFor(s.Glass))
		if err := w.SetVibrancyTint(glassTint(s.Glass)); err != nil {
			log.Println("glass tint:", err)
		}
	}
	if s.ShowInMenuBar != prev.ShowInMenuBar {
		d.bar.setVisible(s.ShowInMenuBar)
	}
}

// onInstanceEvent raises notifications. Disconnects are announced when the
// setting is on; start failures always, unless the window is already open.
func (d *desktop) onInstanceEvent(e instance.Event) {
	s := e.Instance
	if !e.Previous.Live() {
		return
	}
	set := d.svc.Settings()
	var title, body string
	switch s.State {
	case instance.Disconnected:
		if !set.NotifyOnDisconnect {
			return
		}
		title = s.AppName + " disconnected"
		body = s.Reason + ". It wasn’t restarted. Open Waylauncher to start it again; the old session can’t be recovered."
	case instance.Failed:
		if d.windowVisible() {
			return
		}
		title = s.AppName + " couldn’t start"
		body = s.Reason
	default:
		return
	}
	if !mygo.NotificationsSupported() {
		return
	}
	go func() {
		n := mygo.NewNotification(mygo.NotificationOptions{ID: "instance:" + s.ID, Title: title, Body: body})
		n.OnClick(func() { d.showWindow(gui.Nav{View: gui.PageRunning, ID: s.ID}) })
		if err := n.Show(); err != nil {
			log.Println("notification:", err)
		}
	}()
}

// beforeQuit implements the quit policy. With nothing running, quit at once.
// Otherwise: "immediate" stops every instance then quits; "ask" confirms
// first. Cocoa-Way keeps running. Waypipe sessions cannot survive the quit.
func (d *desktop) beforeQuit(e *mygo.QuitEvent) {
	if d.quitting.Load() || !d.svc.HasLive() {
		// Let the quit proceed: the windows it closes are not the user's
		// "close", so OnClose must not turn them into a hide.
		d.quitting.Store(true)
		return
	}
	e.PreventDefault()
	if !d.quitBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer d.quitBusy.Store(false)
		if d.svc.Settings().QuitBehavior == "ask" {
			n := d.svc.State().Live
			res, err := mygo.Dialog.Message(mygo.MessageOptions{
				Type: mygo.MessageWarning, Message: "Quit Waylauncher with " + plural(n, "app") + " running?",
				Detail:  "They will close on their host. Unsaved work in them will be lost. Cocoa-Way keeps running.",
				Buttons: []string{"Cancel", "Close apps and quit"}, DestructiveButtons: []int{1}, CancelButton: 0, DefaultButton: 0,
			})
			if err != nil || res.Button != 1 {
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := d.svc.StopAll(ctx); err != nil {
			log.Println("stopping apps:", err)
		}
		d.quitting.Store(true)
		mygo.App.Quit()
	}()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func appMenu(d *desktop) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "New Application…", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { go d.showWindow(gui.Nav{View: "new-app"}) }},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

// beforeQuitDev stops every instance and quits without asking (dev hook).
func (d *desktop) beforeQuitDev() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := d.svc.StopAll(ctx); err != nil {
		log.Println("stopping apps:", err)
	}
	d.quitting.Store(true)
	mygo.App.Quit()
}
