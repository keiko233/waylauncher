// Command waylauncher is the Waylauncher desktop application. Without a
// subcommand it opens the window; --check-config, launch and status are
// command-line tools for validation and scripting.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/keiko233/waylauncher/internal/profile"
)

const usage = `usage: waylauncher [flags]                   (no command: open the application)
       waylauncher [flags] --check-config
       waylauncher [flags] launch <app-id>
       waylauncher [flags] status

flags:
  --json                 machine-readable output
  --dry-run              with --check-config: report what migration would do, write nothing
  --config-dir DIR       default ~/.config/waylauncher
  --legacy-dir DIR       default ~/.config/vm200-launcher (read-only profiles.json source for migration)
  --backup-dir DIR       default ~/Library/Application Support/Waylauncher/Backups
  --log-dir DIR          default ~/Library/Logs/Waylauncher
  --dock-helper PATH     waylauncher-dock-helper to put in the per-application Dock bundles
                         (default: Contents/Resources of the bundle or next to this executable;
                         none found: no per-application Dock icons)
  --dock-bundle-dir DIR  default ~/Applications/Waylauncher Apps
  --runtime-dir DIR      helper socket directory, mode 0700 (default $TMPDIR/waylauncher)
  --cocoa-way-app PATH   default ~/Applications/Cocoa-Way.app
  --cocoa-way-icon PATH  PNG shown beside Cocoa-Way in the window (default: $WAYLAUNCHER_COCOAWAY_ICON,
                         then <config-dir>/cocoa-way-icon.png, then Cocoa-Way.app/Contents/Resources/icon.png;
                         none found: a status dot). The icon is never bundled: Cocoa-Way is GPL-3.0.
`

type options struct {
	json, dryRun, checkConfig bool
	paths                     profile.Paths
	logDir, cocoaWayApp       string
	cocoaIcon                 string
	dockHelper, dockBundleDir string
	runtimeDir                string
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	defaults, err := profile.DefaultPaths()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var o options
	fs := flag.NewFlagSet("waylauncher", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.dryRun, "dry-run", false, "")
	fs.BoolVar(&o.checkConfig, "check-config", false, "")
	fs.StringVar(&o.paths.ConfigDir, "config-dir", defaults.ConfigDir, "")
	fs.StringVar(&o.paths.LegacyDir, "legacy-dir", defaults.LegacyDir, "")
	fs.StringVar(&o.paths.BackupDir, "backup-dir", defaults.BackupDir, "")
	fs.StringVar(&o.logDir, "log-dir", "", "")
	fs.StringVar(&o.cocoaWayApp, "cocoa-way-app", "", "")
	fs.StringVar(&o.cocoaIcon, "cocoa-way-icon", "", "")
	fs.StringVar(&o.dockHelper, "dock-helper", "", "")
	fs.StringVar(&o.dockBundleDir, "dock-bundle-dir", "", "")
	fs.StringVar(&o.runtimeDir, "runtime-dir", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	cmd := ""
	if len(rest) > 0 { // allow flags after the subcommand too
		cmd = rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		rest = fs.Args()
	}

	switch {
	case cmd == "" && !o.checkConfig && !o.dryRun && !o.json:
		return startUI(o, defaults, stderr)
	case cmd == "dev" && fakeRequested() && len(rest) >= 1 && !o.checkConfig:
		// Development only: forwards to the running fake-transport instance.
		return startUI(o, defaults, stderr)
	case o.checkConfig && cmd == "":
		return checkConfig(o, stdout, stderr)
	case cmd == "launch" && len(rest) == 1 && !o.dryRun:
		return launch(o, rest[0], stdout, stderr)
	case cmd == "status" && len(rest) == 0 && !o.dryRun:
		return status(o, stdout, stderr)
	}
	fs.Usage()
	return 2
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
