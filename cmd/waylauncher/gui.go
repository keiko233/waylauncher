package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/keiko233/waylauncher/internal/desktop"
	"github.com/keiko233/waylauncher/internal/dock"
	"github.com/keiko233/waylauncher/internal/instance"
	"github.com/keiko233/waylauncher/internal/launcher"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
	"github.com/keiko233/waylauncher/internal/transport/waypipe"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

// startUI runs the desktop application. On first start without a
// profiles.toml, a legacy profiles.json is migrated: copied to the backup
// directory first, then converted; the original is never modified.
func startUI(o options, defaults profile.Paths, stderr io.Writer) int {
	logDir := o.logDir
	if logDir == "" {
		d, err := instance.DefaultLogDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		logDir = d
	}
	comp := newCompositor(o)
	cfg := launcher.Config{
		Version:     version,
		Paths:       o.paths,
		LogDir:      logDir,
		Compositor:  comp,
		Transports:  map[string]transport.Transport{"waypipe": waypipe.Transport{}},
		Prober:      remote.Prober{},
		CocoaSocket: comp.SocketPath,
		CocoaApp:    comp.AppPath,
	}
	if fakeRequested() { // development only; see fake.go
		if err := checkFakeIsolated(o, defaults); err != nil {
			fmt.Fprintln(stderr, "waylauncher:", err)
			return 2
		}
		fmt.Fprintf(stderr, "waylauncher: %s=1: fake compositor, SSH and transport (sleep 600). Nothing real is started.\n", fakeEnv)
		cfg.Compositor, cfg.Transports, cfg.Prober = fakeCompositor{}, map[string]transport.Transport{"waypipe": fakeTransport{}}, fakeProber{}
		cfg.CocoaSocket, cfg.CocoaApp = "fake", "fake"
	}
	svc := launcher.New(cfg, true)
	icons := []string{o.cocoaIcon, os.Getenv("WAYLAUNCHER_COCOAWAY_ICON"),
		filepath.Join(o.paths.ConfigDir, "cocoa-way-icon.png"),
		filepath.Join(comp.AppPath, "Contents", "Resources", "icon.png")}
	if err := desktop.Run(desktop.Options{Service: svc, Version: version, CocoaIcons: icons, Dock: dockOptions(o), Dev: fakeRequested()}); err != nil {
		fmt.Fprintln(stderr, "waylauncher:", err)
		return 1
	}
	return 0
}

// dockOptions locates the Dock helper: --dock-helper, else the
// waylauncher-dock-helper in the bundle's Resources (or next to this executable).
func dockOptions(o options) desktop.DockOptions {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	helper := o.dockHelper
	if helper == "" && exe != "" {
		// Installed layout: Waylauncher.app/Contents/Resources/; a build next
		// to the binary works too.
		for _, cand := range []string{
			filepath.Join(filepath.Dir(exe), "..", "Resources", dock.HelperExecutable),
			filepath.Join(filepath.Dir(exe), dock.HelperExecutable),
		} {
			if _, err := os.Stat(cand); err == nil {
				helper = filepath.Clean(cand)
				break
			}
		}
	}
	dir := o.dockBundleDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Applications", dock.BundleSubdir)
	}
	// Waylauncher.app, when running from the installed bundle.
	app := ""
	if exe != "" {
		if d := filepath.Dir(filepath.Dir(filepath.Dir(exe))); filepath.Ext(d) == ".app" {
			app = d
		}
	}
	if fakeRequested() {
		// A helper must never reach the installed Waylauncher.app (and its
		// real profiles) from a development run: `open -a` of this path fails.
		app = "/nonexistent/Waylauncher-fake-transport.app"
	}
	return desktop.DockOptions{
		HelperBinary: helper, BundleDir: dir, BackupDir: o.paths.BackupDir, RuntimeDir: o.runtimeDir,
		WaylauncherApp: app, IconBase: o.paths.ConfigDir, Version: version,
	}
}
