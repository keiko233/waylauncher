// Package gui is the native window of Waylauncher, drawn by MyGo's own ui
// toolkit (no web view, no HTML, no JavaScript). It presents package
// launcher, which owns all behaviour and state; this package only turns that
// state into elements and user input into service calls.
//
// Threading: the view and every field of App except where noted run on the
// main (UI) thread. Service calls that may block (opening, focusing, running
// checks, saving) run on a goroutine and report back through Window.Update.
package gui

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/launcher"
)

// Pages of the window.
const (
	PageApps     = "apps"
	PageRunning  = "running"
	PageHost     = "host"
	PageSettings = "settings"
)

// Nav asks the window to show a page (or, with View "new-app", the editor).
type Nav struct {
	View string
	ID   string
}

const (
	sidebarWidth   = 236
	titleBarHeight = 52
)

type toastMsg struct {
	text string
	err  bool
}

// confirm is a pending yes/no question (an alert dialog).
type confirm struct {
	title, body, yes string
	run              func()
}

// App is the window's state.
type App struct {
	svc *launcher.Service
	// post runs fn on the main thread and redraws (Window.Update); tests
	// replace it.
	post func(func())

	// state shown; replaced by Refresh.
	st launcher.State

	page     string
	hostID   string
	filter   int // 0 all, 1 pinned
	query    string
	selInst  string
	checking map[string]bool
	toasts   []toastMsg

	logText  string
	logInst  string
	logAt    time.Time
	logScrol ui.ScrollState

	iconPaths  []string
	iconLoaded bool
	cocoaIcon  ui.ImageSource // nil when not found

	ask     *confirm
	askOpen bool

	ed   *appEditor
	host *hostEditor

	// per frame
	c       *ui.Context
	p       *palette
	vibrant bool
}

// Options configure the window.
type Options struct {
	// CocoaIcons are candidate paths of Cocoa-Way's icon, tried in order
	// (empty entries are skipped). The image is read at run time, never
	// bundled; when none loads, a status dot is shown instead.
	CocoaIcons []string
}

// New builds the window state from the service.
func New(svc *launcher.Service, o Options) *App {
	return &App{svc: svc, st: svc.State(), page: PageApps, checking: map[string]bool{}, iconPaths: o.CocoaIcons}
}

// Attach connects the window so background work can reach the view.
func (a *App) Attach(w *mygo.Window) { a.post = w.Update }

// Refresh re-reads the service state on the main thread and redraws. It is
// safe to call from any goroutine.
func (a *App) Refresh() {
	a.later(func() { a.st = a.svc.State() })
}

// Navigate shows a page. Safe from any goroutine.
func (a *App) Navigate(n Nav) {
	a.later(func() { a.navigate(n) })
}

func (a *App) navigate(n Nav) {
	switch n.View {
	case "new-app":
		a.openEditor("")
		return
	case PageHost:
		if n.ID == "" && len(a.st.Hosts) > 0 {
			n.ID = a.st.Hosts[0].ID
		}
		a.hostID = n.ID
	case PageRunning:
		if n.ID != "" {
			a.selInst = n.ID
		}
	}
	if n.View == PageApps || n.View == PageRunning || n.View == PageHost || n.View == PageSettings {
		a.page = n.View
	}
}

// ---- background work ------------------------------------------------------

func (a *App) toast(msg string) { a.toasts = append(a.toasts, toastMsg{text: msg}) }
func (a *App) toastErr(err error) {
	a.toasts = append(a.toasts, toastMsg{text: err.Error(), err: true})
}
func (a *App) later(fn func()) {
	if a.post != nil {
		a.post(fn)
	}
}

// async runs fn off the main thread; a note is shown as a toast, an error as
// an error toast. then (optional) runs on the main thread afterwards.
func (a *App) async(fn func() (note string, err error), then func()) {
	go func() {
		note, err := fn()
		a.later(func() {
			if err != nil {
				a.toastErr(err)
			} else if note != "" {
				a.toast(note)
			}
			if then != nil {
				then()
			}
		})
	}()
}

func (a *App) drainToasts() {
	for _, t := range a.toasts {
		if t.err {
			a.c.AddToast(ui.Toast{Title: t.text, Type: "error", Timeout: 7 * time.Second})
		} else {
			a.c.AddToast(ui.Toast{Title: t.text, Timeout: 3800 * time.Millisecond})
		}
	}
	a.toasts = nil
}

func (a *App) confirmThen(title, body, yes string, run func()) {
	a.ask = &confirm{title: title, body: body, yes: yes, run: run}
	a.askOpen = true
}

// ---- the view -------------------------------------------------------------

// View builds one frame.
func (a *App) View(c *ui.Context) {
	sys := c.Theme()
	a.c = c
	a.p = newPalette(sys.Dark)
	a.loadCocoaIcon()
	a.vibrant = c.Vibrancy() && !c.Preferences().ReduceTransparency
	if a.vibrant {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(a.p.window)
	}
	if a.page == PageHost && a.hostID == "" && len(a.st.Hosts) > 0 {
		a.hostID = a.st.Hosts[0].ID
	}
	ui.Themed(c, a.p.theme(sys)).Fill().Background(ui.Transparent).Children(func() {
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			side := ui.Column(c).Width(sidebarWidth).Shrink(0).Padding(0, 10, 12)
			if !a.vibrant {
				side.Background(a.p.sidebarSolid)
			}
			side.Children(a.sidebar)
			ui.Box(c).Width(1).Shrink(0).Background(a.p.hair)
			ui.Column(c).Grow(1).MinWidth(0).Background(a.p.window).Children(a.main)
		})
		a.sheets()
	})
	a.drainToasts()
}

func (a *App) main() {
	switch a.page {
	case PageRunning:
		a.pageRunning()
	case PageHost:
		a.pageHost()
	case PageSettings:
		a.pageSettings()
	default:
		a.pageApps()
	}
}

func (a *App) sheets() {
	a.editorSheet()
	a.hostSheet()
	if a.ask != nil {
		ask := a.ask
		switch ui.AlertDialog(a.c, &a.askOpen, ask.title, ask.body, i18n.T("gui.main.common.cancel"), ask.yes) {
		case 1:
			ask.run()
			a.ask = nil
		case 0, ui.AlertDismissed:
			a.ask = nil
		}
		if !a.askOpen {
			a.ask = nil
		}
	}
}

// ---- small helpers --------------------------------------------------------

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func clock(t time.Time) string      { return t.Local().Format("15:04:05") }
func clockShort(t time.Time) string { return t.Local().Format("15:04") }

func ago(t time.Time) string {
	s := int(time.Since(t).Seconds())
	switch {
	case s < 60:
		return i18n.T("gui.main.ago.now")
	case s < 3600:
		return i18n.T("gui.main.ago.min", "n", (s+30)/60)
	case s < 86400:
		return i18n.T("gui.main.ago.hour", "n", (s+1800)/3600)
	}
	return i18n.T("gui.main.ago.day", "n", (s+43200)/86400)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
