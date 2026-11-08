# waylauncher

Waylauncher starts and manages Linux applications from a remote machine on your Mac, using [Cocoa-Way](https://github.com/J-x-Z/cocoa-way) (a Wayland compositor for macOS) and Waypipe over SSH. It is a thin client: it does not implement a compositor or a display protocol.

There are two clients in this repository:

- **Waylauncher** (the Go application, built on [MyGo](https://github.com/egoist/mygo)): the current development line. Profiles, several instances per application, a Running view, diagnostics, menu bar item, notifications. Everything below is about this one unless it says otherwise.
- **Legacy Swift launcher** (`clients/macos/launcher`, installed with `python3 scripts/install.py --legacy-swift`): the earlier single-file version (`Linux Apps.app`, `vm200-apps://` links, `~/.config/vm200-launcher/profiles.json`). It keeps working and keeps its own paths; Waylauncher never modifies them.

Direction and phases: [docs/mygo-cross-platform-plan.md](docs/mygo-cross-platform-plan.md).

## Build and install

```sh
python3 scripts/install.py
```

Requirements: Apple Silicon macOS, Go (any recent version; `go.mod` asks for Go 1.27.3, which `GOTOOLCHAIN=auto` downloads on first build), Xcode command line tools (`codesign`, `iconutil`) and Python 3. No Node, npm or CGO is needed: the window is drawn by MyGo's own native `ui` toolkit (GPU rendering, no web view, no HTML or JavaScript) and MyGo is pure Go (pinned to v0.8.3).

The installer builds `~/Applications/Waylauncher.app` (bundle id `com.keiko233.waylauncher`, ad-hoc signed, with a Local Network usage description and the `waylauncher://` URL scheme). An existing `Waylauncher.app` is moved to `~/Library/Application Support/Waylauncher/Backups/<timestamp>/` first. It then runs `--check-config --dry-run`, which reads your configuration and writes nothing.

## CI and releases

GitHub Actions runs the Go tests and `go vet` on macOS arm64 for pushes and pull requests, and builds an application ZIP as a workflow artifact using the same installer. The build checks an isolated example configuration, verifies the ad-hoc signature and bundle version, and writes a SHA-256 checksum. The CI build does not test a real remote desktop or prove a window is visible.

To prepare a release, run **Actions → Prepare release draft** on the default branch and enter a new tag such as `v1.2.3`. The workflow creates the tag at the selected commit and a draft containing commits since the nearest reachable version tag (or the full history if none exists). Review the draft before publishing. Publishing the release triggers **Release**, which builds from the release tag and uploads `Waylauncher-1.2.3-macos-arm64.zip` and its `.sha256` file. Re-running that workflow replaces assets of the same name. Repository Actions must be enabled and allowed to write repository contents. GitHub-hosted execution and release uploads require the workflows to be pushed to GitHub; release events require the workflow on the default branch.

The ZIP contains an ad-hoc signed, non-notarized Apple Silicon app; macOS may require the user to explicitly allow it to open. Release tags currently use numeric `vMAJOR.MINOR.PATCH` versions; use `python3 scripts/install.py --version 1.2.3` to set the same bundle version for a local build.

Runtime requirements are those of the Swift launcher: `~/Applications/Cocoa-Way.app`, `waypipe` in `/opt/homebrew/bin`, working SSH key authentication and a trusted host key for the Linux machine, and on the remote side `waypipe` and `/usr/local/libexec/vm200-waypipe-app` (the existing deployment; software rendering is scoped by that wrapper). The installer does not touch any of them.

## Using it

The interface is drawn with MyGo's native `ui` toolkit (`internal/gui`) over the window's Liquid Glass material; it contains no web view. Open Waylauncher. The sidebar has Applications, Running, Discover, your hosts, and Settings.

- **Applications**: one card per profile with a status chip. *Open* starts the application; if a single-instance application is already running, it focuses it instead. Multiple-instance applications follow Settings, and *New window* always starts another. The ⋯ menu has Start again, Open last log, Edit…, Pin to menu bar and Remove….
- **Running**: every instance with its state, host, transport, command and a live log tail. Actions: Focus window, Stop… (confirmation: it ends the program on the host, unsaved work is lost, it is not restarted), Log, Start again, Dismiss, Clear ended.
- **Discover**: lists the applications installed on a host (desktop files from the XDG application folders, read over SSH in BatchMode with one read-only script; nothing is started or changed on the host). Opening the page scans the chosen host once; *Scan* / *Scan again* repeat it, and *Cancel* stops a running scan. Each row shows the icon, name and comment, an *Imported* badge when a profile with `source = "discovered:<desktop-id>"` already exists on that host, and a checkbox. *Import selected* creates profiles (single instance, the entry's name, command and working folder, this host) through the normal atomic save with a backup of the previous `profiles.toml`; profile IDs are made unique. A re-scan never overwrites or changes a profile you already imported or edited. An untrusted or changed host key is reported with *Copy ssh command*; the app never accepts host keys.
- **Hosts**: *Run checks* tests SSH (BatchMode), the host key, remote `waypipe`, the launch wrapper and local `waypipe`, and shows Cocoa-Way's status. A host-key problem is reported with a *Copy ssh command* button; the app never accepts host keys itself.
- **New app / Edit…**: name, ID, host, command as separate argument chips, working folder, environment table, single or multiple instances, and a preview of the exact local and remote commands built by the same code that launches them. Problems are shown inline and block saving. Saving writes `profiles.toml` atomically (temporary file, then rename, mode 0600) after copying the previous file to the backup directory.
- **Menu bar item** (Settings › Show in menu bar): pinned applications, running instances (Focus window, Open log, Stop…), Open Waylauncher, Diagnostics…, Quit.
- **Dock icons for running apps** (Settings › *Show running Linux apps in the Dock*, on by default): while an application has a live instance, its own icon appears in the Dock with a running dot. See "Dock icons" below.
- **Deep links**: `waylauncher://launch/<profile id>` opens an existing profile. Only IDs of existing profiles are accepted; a URL can never supply a command or host.
- **Quit**: by default Waylauncher stops every running instance and quits. Settings › Quitting › *Ask first* shows a confirmation when instances are live. Closing the window only hides it.

Command-line use of the same binary (`~/Applications/Waylauncher.app/Contents/MacOS/waylauncher`): `--check-config [--dry-run] [--json]`, `launch <id>`, `status`; see `--help`. `--config-dir`, `--legacy-dir`, `--backup-dir`, `--log-dir`, `--cache-dir`, `--dock-bundle-dir` and `--runtime-dir` redirect every path, which is how to try it without touching your real configuration.

## Dock icons

Every window of every Linux application is drawn by the one Cocoa-Way process, so macOS knows only Cocoa-Way. To give each application its own Dock entry, Waylauncher generates one small bundle per profile in `~/Applications/Waylauncher Apps/<App name>.app` (bundle id `com.keiko233.waylauncher.app.<id>`, name from the profile, ad-hoc signed, icon from the profile's `icon` PNG path or else the same letters-on-a-tile icon the window shows). The bundles are rebuilt when a profile is saved, renamed or removed; whatever they replace, and the bundle of a removed application, is moved to the backup directory (`.../Backups/<timestamp>/Waylauncher Apps/`), never deleted. Bundles that are not Waylauncher's are left alone.

The executable of each bundle is `waylauncher-dock-helper`, a windowless regular app (about 10 MB, installed in `Waylauncher.app/Contents/Resources/`). It starts nothing by itself and talks to Waylauncher over a Unix socket (`$TMPDIR/waylauncher/helper.sock`, directory mode 0700, one JSON object per line):

- Waylauncher starts **one helper per application** (not per instance) when its first instance starts, and tells it how many instances are live. The helper exits when that count reaches 0 (exited, disconnected, failed or stopped). A disconnected instance never restarts anything.
- Clicking the icon while the application runs asks Waylauncher to focus its most recent instance (Cocoa-Way forward for now). It never starts another instance.
- **Quit** in the Dock menu does not quit the helper: it asks Waylauncher, which shows the Stop confirmation (the program ends on the host, unsaved work is lost) for all instances of that application. On Stop they end and the helper leaves; on Cancel it stays.
- Opening the bundle from Finder, Launchpad, Spotlight or a pinned Dock icon while nothing runs starts the helper, which starts Waylauncher if needed (via `waylauncher://launch/<id>`) and launches the application. If that fails, it exits.
- If Waylauncher quits, crashes or the socket closes, every helper exits. Switching the setting off ends the helpers and leaves the applications running.

What this is not: the windows still belong to Cocoa-Way. Its own Dock icon stays (it cannot be hidden without upstream support), Command-Tab shows Cocoa-Way, and the helper's Dock menu lists no windows. Exact window focus needs Cocoa-Way support (plan section 8.3). The running dot means a Waylauncher instance is live, i.e. the transport process exists; it is not evidence that a window is visible.

## Development: fake transport

`WAYLAUNCHER_FAKE_TRANSPORT=1` (development only) replaces Cocoa-Way, SSH and Waypipe with stand-ins: the "transport" is `sleep 600`, nothing remote is ever started (Discover answers with two canned entries). It refuses to run unless `--config-dir`, `--legacy-dir`, `--backup-dir`, `--log-dir`, `--cache-dir`, `--dock-bundle-dir` and `--runtime-dir` are all given (none may be the real default), and it disables the helper's ability to open the installed Waylauncher.app. A running fake instance can be driven without clicking: `waylauncher <same flags> dev launch|stop|focus|confirm|dock|quit ...` (see `internal/desktop/dev.go`). Never use it with your real directories.

## Files

| What | Where |
|---|---|
| Profiles | `~/.config/waylauncher/profiles.toml` (schema in the plan, section 5) |
| Settings | `~/.config/waylauncher/settings.toml` |
| Backups of replaced profiles and bundles | `~/Library/Application Support/Waylauncher/Backups/<timestamp>/` |
| Per-application Dock bundles | `~/Applications/Waylauncher Apps/` (generated; replaced ones go to the backup directory) |
| Cached application icons (Discover) | `~/Library/Caches/Waylauncher/<host id>/<hash>.png` (regenerable, files 0600; `--cache-dir`) |
| Dock helper socket | `$TMPDIR/waylauncher/helper.sock` (mode 0600 in a 0700 directory) |
| Logs | `~/Library/Logs/Waylauncher/` (directory 0700, files 0600, last N per application) |
| Legacy profiles (read only) | `~/.config/vm200-launcher/profiles.json` |

On first start without a `profiles.toml`, Waylauncher converts the legacy `profiles.json`: it is copied to the backup directory first, never modified, moved or deleted, and the Swift launcher keeps using it. `profiles.toml` is rewritten from the in-memory profile on every save, so comments in a hand-edited file are not preserved (the backup keeps them). Do not store credentials in profiles.

## Icons

A profile's `icon` is a local PNG path (`~/` and paths relative to the config directory work). The window (Applications cards, Running rows, the edit sheet) and the Dock bundle resolve it with the same code (`internal/iconpath`: regular file, at most 16 MB; the window also refuses images over 4096 px), so an application has the same icon in both. Imported applications point at the PNG Discover cached; the Dock `.icns` is built from that file. With no icon, or one that cannot be loaded, both show the same generated tile (glyph and colour from the profile). The window re-reads an icon file when its modification time changes. The editor keeps the `icon` value unchanged on save; there is no field to change it yet.

## States and what they mean

- **Connecting**: pre-launch checks (Cocoa-Way readiness, SSH probe) are running. Only these checks are retried.
- **Connected**: the Waypipe *transport process* is alive. This is not evidence that a window is on screen; Cocoa-Way does not report windows yet, so the UI never claims "window open".
- **Disconnected**: the transport exited with an error. The remote application is **never restarted automatically**: unsaved work and duplicate processes matter. *Start again* is a manual action that starts a **new** instance; the old session cannot be recovered.
- **Exited normally / Stopped**: exit code 0, a stop requested by you, or (setting on by default) waypipe-darwin's exit 1 with "No child processes" after a window closes.
- **Normal SSH reconnect versus session recovery**: Waypipe has no server-side session. A dropped connection ends the remote application, and reconnecting means starting a new copy. Reattaching to a still-running application needs a persistent transport (Xpra or wprs in the plan), which does not exist here yet.
- **Focus window** currently brings Cocoa-Way forward, because Cocoa-Way has no per-window commands. Exact window focus needs upstream support.

## Status

Implemented and covered by `go test ./...`: profile schema, validation, atomic save and backups, legacy migration, Cocoa-Way control-socket client, Waypipe command construction and quoting, SSH probe and error classification, the instance supervisor (states, stop, no auto-restart, log rotation), and the application core behind the UI (open/focus/new window rules, editor validation, diagnostics, pin/remove).

Verified on this machine: `go vet`, `gofmt`, the build and ad-hoc signing, `--check-config`, the app starting from the installed bundle with a temporary configuration (native window opening in light and dark, migration into a temporary directory), the `waylauncher` URL scheme being registered, and every page and sheet of the native UI rendered in light and dark and exercised by view tests (`go test ./internal/gui`; set `WAYLAUNCHER_SHOTS=<dir>` to write screenshots).

**Not verified** (needs a person at the screen and the real VM): clicking through every control in the real window; launching Konsole and foot and the full validation checklist of the plan; window glass in light mode and with *Clear*; the tray menu and its submenus; notifications (MyGo has no notification actions, so a click opens the Running view where *Start again* and *Open log* are); the Quit confirmation with live instances; Local Network permission behaviour; the Dock helpers on the real VM and the Dock menu's Quit confirmation dialog (checked with the fake transport only; the Dock icon and running dot were seen in a screenshot); starting Waylauncher from a helper when it is not running; Discover, import and icon fetching against the real VM (covered by tests with fake ssh output only; the scan script and icon lookup have never run on a real host), and the real-icon Dock bundle seen in the Dock; Windows and Xpra/wprs (not implemented).

## Remove or roll back

Quit Waylauncher and move `~/Applications/Waylauncher.app` to the Trash; restore an earlier bundle from the backup directory if needed. Profiles, settings and logs stay where they are. The legacy Swift launcher, Cocoa-Way, Waypipe, SSH configuration and the VM are untouched.
