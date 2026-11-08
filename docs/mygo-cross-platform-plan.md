# Cross-platform launcher on MyGo: plan

Status: 2026-11-08. The project is now called **waylauncher** (module
`github.com/keiko233/waylauncher`, binary and app "Waylauncher"). The macOS
core and the desktop UI of Phase 1 are implemented (see the README); the checks
of [section 15](#15-validation-checklist) against the real VM have not been
done, and nothing from Phases 2 to 5 exists yet. The Swift launcher
(`clients/macos/launcher/main.swift`, built with
`python3 scripts/install.py --legacy-swift`) is the legacy version and keeps its
own paths and `vm200-apps://` scheme.

## 1. Purpose and decisions

The goal is an experience as close as practical to Parallels Desktop's
Coherence mode: remote Linux applications that are discovered, launched and
managed from the host desktop and appear as ordinary windows. The launcher is
a thin client: it does not implement a Wayland compositor or a remote display
protocol.

Decisions already taken:

| Topic | Decision | Reason |
|---|---|---|
| Application framework | [MyGo](https://github.com/egoist/mygo) (Go, MIT) | Lowest total development cost for one codebase on macOS, Windows and Linux; tray, menus, deep links, single instance, notifications, packaging and updates are built in |
| UI fidelity | MyGo's own `ui` toolkit and glass plugin; strict AppKit/Fluent conformance is not required | Cost over platform purity |
| macOS display path | Cocoa-Way (Rootless) + Waypipe over SSH, unchanged | Already working on VM 200 |
| macOS compositor integration | Cocoa-Way's control socket, spoken natively from Go | Replaces `cocoa-wayctl` subprocesses; keeps the GPL-3.0 compositor in its own process |
| Windows display path | Xpra (primary) and wprs (optional, via WSLg) | Xpra has a mature native Windows client and session persistence; wprs covers Wayland-only applications |
| Go toolchain | Go 1.27.3 or later, as MyGo requires | Install/upgrade is a setup step only |

Not decided yet: see [Open questions](#14-open-questions).

## 2. Constraints carried over

These come from `AGENTS.md` and apply to every phase.

- Thin client around Cocoa-Way, Waypipe and the other transports. No changes to
  NAS/PVE, VM GPU drivers, KWin, Sunshine or unrelated homelab services as part
  of launcher work. Installing Xpra or wprs on VM 200 is a separate,
  deliberately approved step, not a side effect of a launcher install.
- Preserve user profiles and installed bundles; keep recoverable backups on
  reinstall and on profile-format migration.
- No credentials in source, profiles, fixtures or commits. SSH host-key
  checking stays on for every transport, including Xpra's own SSH handling.
- **Never restart a remote application automatically after a disconnect.**
  Retries are allowed only for pre-launch checks (compositor readiness, SSH
  reachability). Reattaching to a session that is still alive on the server
  (Xpra, wprs) is allowed only on an explicit user action.
- Profile commands are argument arrays. The launcher never evaluates them in a
  local shell, and builds the remote command with explicit quoting.
- Documentation and UI must keep two distinctions visible:
  - transport process alive versus a window actually shown;
  - normal SSH reconnect versus persistent application-session recovery.
- Generated binaries, user profiles and logs are not committed.

## 3. Baseline: what exists today

| Capability | Current Swift launcher |
|---|---|
| Start or reuse Cocoa-Way, wait for readiness | Yes, by polling `cocoa-wayctl --json status` |
| SSH pre-check (BatchMode, timeouts) | Yes |
| Launch through bundled `vm200-waypipe` script | Yes; remote side `/usr/local/libexec/vm200-waypipe-app` sets software rendering |
| Profiles (legacy Swift launcher) | JSON at `~/.config/vm200-launcher/profiles.json`: `id`, `name`, `target`, `arguments` |
| Shortcuts | `Linux Terminal.app`, `Linux foot.app`, routed through `vm200-apps://<id>` (legacy scheme) |
| States | connecting / running (transport alive) / disconnected dialog |
| Logs (legacy) | `~/Library/Logs/VM200 Launcher/`, 0700 directory, 0600 files, no rotation |
| One instance per profile | Yes (second click activates Cocoa-Way) |
| Window focus, discovery, GUI configuration, Windows client | No |

The research report's "Phase 1, eliminate manual launching" is therefore
already done. This plan starts from that baseline.

## 4. Architecture

```
┌──────────────────────────── MyGo application (one Go binary per OS) ───────────────────────────┐
│  UI (mygo/ui + glass)   Tray menu   Deep links   Notifications   Single instance              │
│                                   │                                                            │
│  core: profiles · discovery · instances/supervisor · logs · shortcuts · diagnostics           │
│                                   │                                                            │
│           ┌───────────────────────┼─────────────────────────┐                                  │
│     Transport: waypipe      Transport: xpra           Transport: wprs                          │
│           │                       │                         │                                  │
│  Compositor: cocoaway      (Xpra client draws        Compositor: WSLg                          │
│  (control socket)           its own windows)                                                   │
└───────────┼───────────────────────┼─────────────────────────┼──────────────────────────────────┘
            │                       │                         │
      Cocoa-Way.app           xpra client (Win)          WSL2 + wprsc
            │                       │                         │
            └──── SSH ──────────────┴────── SSH ──────────────┘
                                    │
                         VM 200 (Arch Linux): waypipe · xpra server · wprsd
```

The core knows nothing about a specific compositor or transport. Each backend
is an implementation of a small interface, so the Windows path can change
(for example to a future native Windows Wayland compositor) without touching
profiles, discovery or the UI.

### 4.1 Proposed Go layout

```
cmd/waylauncher/           main: app lifecycle, CLI (--check-config, launch, status)
internal/desktop/          MyGo glue: window, tray, notifications, deep links, quit
internal/launcher/         application core behind the UI (state, editing, diagnostics)
internal/gui/              the window: views drawn with MyGo's native ui toolkit (no web view)
internal/profile/          schema, load/validate, JSON→TOML migration, backups
internal/remote/           SSH probe, remote command quoting, host-key policy
internal/discovery/        remote .desktop scan, parsing, icon fetch/convert
internal/instance/         supervisor, state machine, per-instance logs
internal/transport/        Transport interface
internal/transport/waypipe
internal/transport/xpra
internal/transport/wprs
internal/compositor/       Compositor interface
internal/compositor/cocoaway   control-socket client (macOS)
internal/compositor/wslg       WSL availability checks (Windows)
internal/shortcut/         .app bundles (macOS), .lnk + .ico (Windows), .desktop (Linux)
scripts/                   install script (still the build entry point)
```

### 4.2 Interfaces (sketch)

```go
type Compositor interface {
    // Ensure starts the compositor if needed and waits for readiness,
    // bounded by ctx. Readiness means the compositor answers, not that
    // any application window exists.
    Ensure(ctx context.Context) error
    Status(ctx context.Context) (CompositorStatus, error)
    // Activate brings the compositor's windows forward. Precise
    // per-window activation needs upstream support (section 8.3).
    Activate(ctx context.Context, hint WindowHint) error
}

type Transport interface {
    Name() string                       // "waypipe", "xpra", "wprs"
    Supported(p Platform) bool
    Persistent() bool                   // true: detach/attach is possible
    Start(ctx context.Context, inst *Instance) (Process, error)
    Attach(ctx context.Context, inst *Instance) (Process, error) // persistent only
    Detach(ctx context.Context, inst *Instance) error            // persistent only
    Stop(ctx context.Context, inst *Instance) error             // ends the remote app
}
```

`Stop` and `Detach` must be distinct actions in the UI. For Waypipe, which has
no persistence, `Detach` is not offered, and closing the connection ends the
application.

## 5. Profile model

### 5.1 Schema (TOML, proposed)

```toml
version = 2

[[hosts]]
id = "vm200"
ssh = "keiko@10.0.0.9"          # user@host or an ~/.ssh/config alias

[[apps]]
id = "konsole"                   # [a-z0-9-], unique, used in deep links
name = "Linux Terminal"
host = "vm200"
command = ["konsole", "--separate", "--nofork"]
cwd = "/home/keiko"              # optional
icon = "konsole"                 # theme name, path, or cached file
platforms = ["macos", "windows"]
transport.macos = "waypipe"
transport.windows = "xpra"
display = "any"                  # "wayland" | "x11" | "any"
instances = "multiple"           # "single" | "multiple"
source = "manual"                # or "discovered:<desktop-file-id>"

[apps.env]
QT_QPA_PLATFORM = "wayland"
```

Validation rules:

- `id`: non-empty, ASCII letters, digits and `-`, unique (same as today).
- `ssh`: must not start with `-`. No passwords or keys anywhere in a profile.
- `command`: non-empty array of strings.
- `env` keys: `[A-Za-z_][A-Za-z0-9_]*`; values are plain strings.
- `display = "wayland"` with `transport.windows = "xpra"` is a validation
  error (Xpra's Wayland server is not usable yet).
- Unknown keys produce a warning, not silent acceptance.

### 5.2 Remote command construction

The launcher never passes a profile through a local shell. On the remote side
the command is built as:

```
/usr/local/libexec/vm200-waypipe-app env -C <cwd> KEY=VAL ... -- <command...>
```

Each element is quoted for the remote login shell the same way the current
script does (`printf %q`). `env -C` is GNU coreutils and is available on
Arch. Software rendering stays inside the remote wrapper and is not moved
into profiles.

### 5.3 Migration from `profiles.json`

1. If `~/.config/waylauncher/profiles.toml` exists, use it; otherwise read the
   legacy `~/.config/vm200-launcher/profiles.json` (read only).
2. Convert each entry into a host (deduplicated by `target`) and an app with
   `transport.macos = "waypipe"`.
3. Write the TOML file with mode 0600, created exclusively (no overwrite).
4. Keep `profiles.json` in place and copy it to
   `~/Library/Application Support/Waylauncher/Backups/<timestamp>/`.
5. `--check-config` validates the TOML file, and reports migration results.

The Swift launcher keeps reading `profiles.json` during the transition, so a
rollback is always possible.

## 6. Instance lifecycle

Each launch creates an instance with a unique ID (`<app-id>-<short-uuid>`), a
log file and a state:

```
           pre-checks (may retry)          transport started
 requested ──────────────────────► connecting ──────────────► transport-running
     │                                 │                            │
     │ failure                         │ failure                    ├─► window-shown (only if the
     ▼                                 ▼                            │   compositor reports it)
  failed ◄──────────────────────── failed                           │
                                                                    ├─► detached (persistent
                                                                    │   transports only)
                                                                    ▼
                                                     exited (code 0) / disconnected (code ≠ 0)
```

Rules:

- `transport-running` is labelled "connected", never "ready". `window-shown`
  is only shown when a compositor or transport reports a mapped window; until
  upstream support exists (section 8.3) it is not displayed at all.
- `disconnected` shows a notification with the exit code, a log link and a
  manual "Start again" action. No automatic restart.
- For persistent transports, a dropped connection moves the instance to
  `detached` if the server session is still alive, with a manual "Reattach"
  action. Reattach restores the same running application; it is not a
  restart.
- Known Waypipe-darwin behaviour: a normal window close can exit with code 1
  (`No child processes`). Classify it as normal exit when the log contains
  that marker and the exit followed a window close, otherwise as disconnect.
  Keep the heuristic in one place with a test fixture made from a redacted
  log.
- Quitting the launcher with live instances asks first. Waypipe instances end
  with the launcher only if the user confirms; persistent instances can be
  detached instead.

## 7. Transports

### 7.1 Waypipe (macOS)

Port of today's `scripts/vm200-waypipe`:

```
waypipe --no-gpu --compress=lz4 --threads=2 ssh \
  -o BatchMode=yes -o ConnectTimeout=5 -o ConnectionAttempts=1 \
  -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
  <ssh-target> <quoted remote command>
```

- `XDG_RUNTIME_DIR` and `WAYLAND_DISPLAY` come from the Cocoa-Way status
  response instead of globbing `$TMPDIR/cocoa-way/wayland-*`.
- Persistence: none. Disconnect ends the application.
- Window identity: all windows belong to Cocoa-Way.

### 7.2 Xpra (Windows, primary)

- Client: the official Xpra Windows build, installed separately and located by
  the launcher (`xpra_cmd.exe`). The launcher does not bundle it in the first
  release.
- Server: `xpra` on VM 200, using the X11 seamless server. Installing it is a
  separate approved change to VM 200.
- Start: one Xpra server session per host (shared by all apps), started on
  first use, then applications added with Xpra's start/control commands.
- Attach and detach map to Xpra's attach/detach; the session persists on the
  server across network loss.
- Limits: the Xpra Wayland seamless server is still a work in progress, so
  Wayland-only applications (for example `foot`) are not offered on this
  transport. Qt and GTK applications run as X11 clients.
- SSH: confirm which SSH implementation the Windows client uses and that it
  verifies host keys with the user's `known_hosts`. Do not use options that
  disable host-key checking.
- Exact command lines are to be fixed during Phase 3 against the installed
  Xpra version; record them here when verified.

### 7.3 wprs (Windows via WSLg, optional)

- `wprsc` is a Wayland client and needs a local compositor, so on Windows it
  runs inside WSL2 and draws through WSLg.
- Server: `wprsd` as a systemd user service on VM 200 (needs lingering).
  Separate approved change, as for Xpra.
- Commands (from upstream): `wprs <host> run <app>`, `wprs <host> detach`,
  `wprs <host> attach`, invoked through `wsl.exe`.
- Value: persistent sessions for Wayland-only applications on Windows.
- Limits: smaller protocol coverage than Waypipe, no DMA-BUF; requires WSL2.
- macOS: running `wprsc` against Cocoa-Way would add persistence on macOS too,
  but no macOS build is supported upstream. Research item only.

## 8. macOS backend: Cocoa-Way

### 8.1 Control socket client

- Path: `$COCOA_WAY_CONTROL_SOCKET`, else
  `$TMPDIR/cocoa-way/control/control.sock`.
- Framing: one JSON object per line over a Unix stream socket; one request,
  one response.
- Request: `{"command": "<name>", "session": <string|null>, "limit": <n>}`.
- Response: `{"protocol": 1, "ok": bool, "command": "...", "data": ..., "error": "..."}`.
  Reject responses whose `protocol` differs from the version the client
  supports, with a clear "update the launcher or Cocoa-Way" message.
- Timeouts: connect and read bounded (about 1–2 s for status calls).
- Commands used: `status` (readiness, runtime paths), `displays`,
  `diagnostics`, `features`. `launch`/`stop`/`check` are for Cocoa-Way's own
  container profiles and are not used for SSH applications.
- Mutating commands are queued by Cocoa-Way: acceptance is not completion.
- Starting the compositor: `open` on `~/Applications/Cocoa-Way.app`, then poll
  `status` until it answers or the deadline passes.

### 8.2 Shortcuts and identity: the Dock helper

Implemented in `internal/dock`, `cmd/waylauncher-dock-helper` and
`internal/desktop/dock.go`.

- One generated bundle per profile in `~/Applications/Waylauncher Apps/`:
  bundle id `com.keiko233.waylauncher.app.<id>`, `CFBundleName` = profile
  name, `.icns` built in Go from the profile `icon` (a local PNG path) or the
  generated glyph tile, ad-hoc signed. The executable is
  `waylauncher-dock-helper`, a windowless regular MyGo app (no cgo, no
  purego code of our own; MyGo already provides the reopen and quit hooks).
  The Dock identity comes from the bundle, so each bundle's executable is its
  own process. A bundle is rebuilt only when name, icon, helper binary or
  Waylauncher's location changed; the old one, and the bundle of a removed
  app, goes to `<backup>/<timestamp>/Waylauncher Apps/`.
- One helper process per application, started by Waylauncher (`open -g -a`)
  while the application has a live instance and ended when the count is 0.
  Setting: "Show running Linux apps in the Dock" (default on).
- IPC: Unix socket in a 0700 runtime directory (`$TMPDIR/waylauncher/helper.sock`,
  under the 104-byte limit), one JSON object per line. Helper to app:
  `hello{app,launch}`, `reopen` (Dock click), `quit-request` (Dock menu
  Quit). App to helper: `count{n}`, `exit`. A closed socket makes the helper
  exit, so a crashed Waylauncher leaves no helpers.
- The helper decides nothing: Dock click focuses the most recent instance
  (existing focus path, Cocoa-Way fallback) and never starts one; Quit is
  cancelled in the helper and becomes the existing Stop confirmation for all
  instances of the app; opening the bundle while nothing runs starts
  Waylauncher through the deep link if needed and launches the app. A
  disconnected instance never restarts anything.
- Honest limits: the windows still belong to Cocoa-Way. Its own Dock icon
  stays, Command-Tab shows Cocoa-Way, the helper's Dock menu lists no
  windows, and Dock click cannot raise one specific window. The running dot
  tracks the Waylauncher instance (the transport process), not a mapped
  window. Log out or restart while a helper holds a Quit pending is
  governed by Waylauncher's own quit policy, which tells helpers to leave.

### 8.3 Upstream requests to Cocoa-Way

Needed for Coherence-like behaviour; proposed to the Cocoa-Way project, not
patched locally:

1. `windows` command: list mapped toplevels with a stable ID, Wayland
   `app_id`, title, owning client and connection.
2. `activate-window <id>` command.
3. Events (subscription on the socket) for window map/unmap, so the launcher
   can show `window-shown` and detect a normal close.
4. Longer term: per-application window ownership so Linux apps get their own
   Dock and Command-Tab identity.
5. An option to hide Cocoa-Way's own Dock icon (accessory activation policy).
   With the per-application helpers of 8.2 this removes the duplicate icon;
   today it stays next to them. Requested upstream, not patched locally.

## 9. Windows backend

- Prerequisite checks with clear messages: Xpra client installed; for wprs,
  WSL2 available and WSLg enabled.
- Start Menu shortcuts: `.lnk` files under the user's Start Menu folder in a
  `Linux Apps` subfolder, target `waylauncher.exe launch <id>` or the deep
  link; `.ico` built from the remote icon.
- Windows are drawn by the Xpra client (or by WSLg for wprs). Taskbar grouping
  and Alt-Tab come from those clients and are recorded as observed, not
  promised.
- No Waypipe on Windows in the first release. WSLg + Waypipe stays a possible
  fallback behind the same Transport interface.

## 10. Remote application discovery

1. Over SSH (BatchMode), run a small read-only helper that lists desktop files
   from `$XDG_DATA_HOME/applications` and each `$XDG_DATA_DIRS/applications`
   (defaults `/usr/local/share`, `/usr/share`), with later directories
   overridden by earlier ones per the Desktop Entry spec.
2. Parse locally in Go. Keep only `Type=Application`; skip `NoDisplay=true`,
   `Hidden=true`, and entries whose `OnlyShowIn`/`NotShowIn` exclude the
   session; check `TryExec` remotely.
3. Turn `Exec` into an argument array using the spec's own quoting rules, never
   a shell. Drop field codes (`%f %F %u %U %i %c %k`); entries that need a
   file argument are imported without it.
4. Mark Wayland-only applications where detectable (known list plus user
   override) so the Xpra transport can exclude them.
5. Icons: resolve the `Icon` key through the icon theme (hicolor fallback),
   fetch the best PNG or SVG, rasterise SVG locally, cache under the
   launcher's cache directory, build `.icns`/`.ico`.
6. Import is opt-in: the user picks which discovered apps become profiles. A
   desktop file never creates or runs a profile on its own, and re-scans show
   changes without overwriting user edits.

## 11. UI

The window is drawn by MyGo's own native `ui` toolkit (GPU, no web view, no
HTML, no JavaScript, no frontend build) with the window's Liquid Glass
material behind the sidebar. `internal/gui` turns the state of
`internal/launcher` into elements and user input into service calls;
`internal/desktop` owns the window itself, tray, notifications, deep links and
the quit policy.

- Window: edge-to-edge glass sidebar (Applications, Running with a count
  badge, Discover disabled as "Coming soon", hosts with their last check,
  Settings, a Cocoa-Way status card), a unified toolbar title and flat content
  panels (radius 12, no stroke) over the window background. Sheets use radius
  16. Application cards keep a 1 px border. Light and dark palettes follow
  the Theme setting.
- Applications: grid of cards with status chips, Open / New window / Start
  again and a ⋯ menu (Open last log, Edit…, Pin to menu bar, Remove…).
- Running view: one row per instance with state, start time and actions
  (Focus window, Log, Start again, Dismiss, Stop… after confirmation) and a
  detail pane with a live log tail. Detach/Reattach appears only with a
  persistent transport.
- Hosts: diagnostics with Run checks, Cocoa-Way status, and host-key
  guidance (Copy ssh command). Application and host editors validate inline
  and show the exact local and remote commands.
- Tray/menu bar: pinned apps, running instances, open window, quit.
- Settings: theme, glass, quitting, multi-instance reopen, "No child
  processes", disconnect notifications, menu bar item, logs kept.
- Notifications: launch failure, disconnect, detached session available.
- Wording rule: "Connected" for transport-running; "Window open" only when
  reported.

## 12. Logging and diagnostics

- Per-instance log files, directory 0700 and files 0600, in the platform log
  directory.
- Rotation: keep the last N instances per app and cap total size.
- Diagnostics view: compositor status, SSH reachability, transport versions,
  recent exits with classification. Redact host-specific secrets; never log
  environment values marked secret.

## 13. Phases, effort and exit criteria

Effort is for one developer and excludes upstream Cocoa-Way work.

### Phase 0 — Setup (2–3 days)

- Go 1.27.3+, MyGo CLI, new module beside the Swift client
  (the module root, `cmd/waylauncher`).
- CI build for macOS and Windows.
- Exit: an empty MyGo tray app builds and runs on both systems.

### Phase 1 — Parity on macOS (2–3 weeks)

- Profile loader with JSON→TOML migration and `--check-config`.
- Cocoa-Way socket client; Waypipe transport; supervisor with today's rules.
- Tray menu, simple window, deep link `waylauncher://launch/<id>`, the two default
  shortcuts.
- `scripts/install.py` builds the Go app and keeps backups of replaced bundles.
- Exit: every item of the validation checklist (section 15) that passes with
  the Swift launcher passes with the MyGo build. Then the Swift launcher is
  retired (kept in git history).

### Phase 2 — Management on macOS (3–4 weeks)

- Multiple instances, Running view, stop, log rotation.
- Full settings UI with validation.
- Remote discovery and opt-in import; icon pipeline; per-app `.app`
  generation.
- Exit: Konsole and foot discovered, imported, launched from their own
  shortcuts, two instances of Konsole running and stoppable individually.

### Phase 3 — Windows with Xpra (2–3 weeks)

- Approved installation of the Xpra server on VM 200 (separate change).
- Xpra transport with attach/detach; Windows prerequisites; Start Menu
  shortcuts and `.ico`.
- Exit: Konsole runs from a Windows Start Menu shortcut; network loss leaves
  a detached session that reattaches manually with the application intact.

### Phase 4 — Optional wprs via WSLg (1–2 weeks)

- wprs transport through `wsl.exe`, approved `wprsd` installation.
- Exit: foot runs on Windows and survives detach/attach.

### Phase 5 — Packaging (about 1 week)

- macOS `.dmg` (ad-hoc signed first; notarisation when a Developer ID is
  available), Windows installer, update channel off by default.

Total: about 9–12 weeks for Phases 0–3 and 5; 10–14 weeks with Phase 4.

## 14. Open questions

1. Deep-link scheme: decided, `waylauncher://launch/<id>`; the Swift launcher
   keeps `vm200-apps://` until it is retired.
2. Should one Xpra session per host be shared by all apps, or one per app
   (simpler lifecycle, more resources)?
3. Does the launcher bundle the Xpra Windows client or require a separate
   install?
4. Which repository owns the remote helpers (`vm200-waypipe-app`, discovery
   helper): this repo, or the homelab deployment?
5. Profile ownership if Cocoa-Way later supports SSH application profiles.
6. License for this repository (none yet). Using the socket keeps the choice
   open; linking Cocoa-Way (GPL-3.0-only) would not.

## 15. Validation checklist

Run on the real VM 200 setup for each phase that touches the item. Do not
claim an item as supported until it passes.

| Test | Expected result | macOS | Windows |
|---|---|---|---|
| Compositor auto-start | Launch starts Cocoa-Way when stopped | ☐ | n/a |
| Compositor reuse | Second launch does not start another compositor | ☐ | n/a |
| Multiple apps | Konsole and foot in separate windows | ☐ | ☐ (foot via wprs only) |
| Multiple instances | Two Konsole instances, each stoppable | ☐ | ☐ |
| Window behaviour | Activation, maximise, popups, switching | ☐ | ☐ |
| HiDPI | Correct scale on Retina / high-DPI displays | ☐ | ☐ |
| Clipboard | Text and images both ways | ☐ | ☐ |
| Chinese IME | Composition and candidate placement | ☐ | ☐ |
| Drag and drop | Files and content | ☐ | ☐ |
| Tauri/WebKitGTK | Renders without GPU failures | ☐ | ☐ |
| Disconnect | Clear state, no automatic restart | ☐ | ☐ |
| Detach/reattach | Same application state after reattach | n/a | ☐ |
| Cleanup | No orphaned transport processes or stale sockets | ☐ | ☐ |
| Profile migration | JSON profiles preserved and backed up | ☐ | n/a |
| Host-key checking | Unknown host key refused with a clear message | ☐ | ☐ |

## 16. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| MyGo API churn (fast-moving project) | Rework on upgrades | Pin the version; keep UI code in `internal/gui` |
| Xpra Wayland server not ready | Wayland-only apps missing on Windows | Mark them; offer wprs via WSLg |
| Cocoa-Way control protocol changes | macOS backend breaks | Check `protocol`; keep the client small and tested |
| No window events upstream | Can't show real window state or precise focus | Label states honestly; pursue upstream requests |
| Waypipe exit-code noise | False disconnect alerts | Single classifier with fixture tests |
| Remote installs drift from launcher | Hard-to-debug failures | Diagnostics view reports remote helper versions |

## 17. Sources

- MyGo: <https://github.com/egoist/mygo> (README, `docs/architecture.md`,
  `docs/launchers.md`, `docs/plugins/glass.md`)
- Cocoa-Way 2.1.2: <https://github.com/J-x-Z/cocoa-way> (`src/control_protocol.rs`,
  `src/control_client.rs`, `src/control_api.rs`)
- Xpra 6.4 announcement: <https://lists.devloop.org.uk/pipermail/shifter-users/2025-December/003054.html>
- Xpra platforms: <https://github.com/Xpra-org/xpra/wiki/Platforms>
- wprs: <https://github.com/wayland-transpositor/wprs>
- WSLg: <https://github.com/microsoft/wslg>
- Desktop Entry Specification: <https://specifications.freedesktop.org/desktop-entry-spec/latest/>
