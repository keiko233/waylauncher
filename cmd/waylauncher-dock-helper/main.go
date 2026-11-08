// Command waylauncher-dock-helper is the process behind one Linux
// application's Dock icon. Waylauncher generates a small .app bundle per
// application whose executable is a copy of this program; the Dock shows the
// bundle's icon and name with a running dot while the process lives. It has no
// window and starts nothing: it asks Waylauncher (over a Unix socket, see
// internal/dock) to focus, launch or stop the application, and exits when the
// application has no live instance left or Waylauncher goes away.
//
// The windows of Linux applications still belong to Cocoa-Way.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/egoist/mygo"

	"github.com/keiko233/waylauncher/internal/dock"
)

func main() {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	info, err := dock.ReadBundleInfo(exe)
	if err != nil || !dock.ValidApp(info.AppID) {
		fmt.Fprintln(os.Stderr, "waylauncher-dock-helper: not run from a Waylauncher application bundle")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("waylauncher-dock-helper", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // LaunchServices may add arguments such as -psn_...
	started := fs.Bool("started-by-waylauncher", false, "")
	socket := fs.String("socket", "", "")
	wlApp := fs.String("waylauncher-app", "", "")
	_ = fs.Parse(os.Args[1:])
	if *socket == "" {
		*socket = info.Socket
	}
	if *socket == "" {
		*socket = dock.SocketPath(dock.DefaultRuntimeDir())
	}
	if *wlApp == "" {
		*wlApp = info.WaylauncherApp
	}

	h := dock.NewHelper(dock.HelperConfig{
		App: info.AppID, Socket: *socket, StartedByApp: *started, WaylauncherApp: *wlApp,
	})

	var leaving atomic.Bool // set once the helper decided to exit: quit must go through
	name := info.Name
	if name == "" {
		name = info.AppID
	}
	mygo.App.SetName(name)
	// No window, ever: the process only holds the Dock icon.
	mygo.App.OnWindowAllClosed(func() {})
	// Dock click on the running helper (also Finder, Launchpad, Spotlight).
	mygo.App.OnActivate(func(bool) { h.Reopen() })
	// Quit from the Dock menu or Cmd-Q is a request to Waylauncher, which
	// asks the user and stops the application; the helper then exits because
	// its instance count reaches zero. Until then, it stays.
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if leaving.Load() {
			return
		}
		if h.QuitRequested() {
			leaving.Store(true)
			return
		}
		e.PreventDefault()
	})

	mygo.App.WhenReady(func() {
		go func() {
			reason := h.Run(context.Background())
			log.Printf("%s: leaving: %v", info.AppID, reason)
			leaving.Store(true)
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "waylauncher-dock-helper:", err)
		os.Exit(1)
	}
}
