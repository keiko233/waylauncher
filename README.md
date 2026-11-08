# cocoa-way-launcher

A small native macOS launcher for remote Linux applications using Cocoa-Way Rootless and Waypipe. The first version replaces manually starting a compositor and typing SSH commands with clickable application shortcuts and a simple application panel.

## Current version

- Swift/AppKit panel and menu bar entry.
- `Linux Terminal.app` (Konsole) and `Linux foot.app` shortcuts; drag them to the Dock if desired.
- Starts/reuses Cocoa-Way, checks readiness and SSH connectivity, then runs the existing software-rendered Waypipe path.
- Shows connecting/running state. Repeated clicks on the same profile reuse the active connection and activate Cocoa-Way; focusing the exact remote window is not implemented.
- JSON application profiles, reload button, private per-launch logs, and connection error dialogs. No unattended retry that could unexpectedly restart an application with unsaved work.
- All shortcuts route through one application manager using a local URL scheme. URL arguments select existing profile IDs only; they do not supply shell commands.

This is a simple first version, not a complete Coherence implementation. Windows/WSLg remains a future client. Remote windows still belong to Cocoa-Way rather than acquiring a distinct macOS application identity for each Linux process. A running state means the transport process is alive, not proof the remote application has painted a frame.

## Requirements

- Apple Silicon macOS, Xcode Command Line Tools (`swiftc`) and Python 3 for building.
- Cocoa-Way and waypipe-darwin already installed via Homebrew under `/opt/homebrew/bin`.
- `~/Applications/Cocoa-Way.app` configured for Rootless presentation. The existing homelab setup provides this; this installer does not replace it.
- SSH authentication and host-key trust already configured for the target Linux machine. GUI launches use BatchMode and cannot ask for a password or accept a new host key.
- Remote `waypipe` and `/usr/local/libexec/vm200-waypipe-app` from the existing VM 200 deployment. That wrapper scopes software rendering to forwarded applications. This version deliberately retains that dependency rather than touching NAS GPU configuration.

## Build and install

```sh
python3 scripts/install-vm200-launcher.py
```

Builds and installs `~/Applications/Linux Apps.app`, `Linux Terminal.app`, and `Linux foot.app`. Open Linux Apps for the panel, or open either shortcut directly. No Terminal window or manually started compositor is required.

Existing installed bundles are backed up to `~/Library/Application Support/VM200 Launcher/Backups/<timestamp>/`. Existing profiles are preserved. Reinstallation should be done after closing connected Linux applications and quitting Linux Apps; it does not manage a live upgrade of the running process. The app bundles receive a local ad-hoc code signature; they are not notarized distribution builds.

macOS may request Local Network access for Linux Apps. Allow it for connections to your Linux machine. If GUI SSH reports `No route to host` while Terminal works, inspect System Settings → Privacy & Security → Local Network. This is separate from SSH authentication; no privacy database is edited by the installer.

## Application configuration

Use **Edit applications…**, then **Reload applications**, or edit:

```text
~/.config/vm200-launcher/profiles.json
```

Example:

```json
[
  {
    "id": "konsole",
    "name": "Linux Terminal",
    "target": "keiko@10.0.0.9",
    "arguments": ["konsole", "--separate", "--nofork"]
  }
]
```

Commands are argument arrays, not evaluated by the launcher. For an explicit working directory or shell environment, use an intentional remote command such as `["bash", "-lc", "cd ~/projects/example && pnpm tauri dev"]`. Only run commands you trust. The default installer generates the two bundled shortcuts; custom profiles currently appear in the panel/menu only. Do not store credentials in profiles.

Logs live in `~/Library/Logs/VM200 Launcher/` (directory 0700, files 0600). Application stdout/stderr may contain application-specific information. Log rotation is not yet implemented. Closing a remote application ends its connection. Disconnects require starting it again; this does not restore unsaved application state. Known waypipe-darwin behavior observed during testing: a normal window close can return exit code 1 with `No child processes`, resulting in a disconnect dialog even though the window closed normally.

## Validation on 2026-11-08

- Compiled with the system Swift compiler; `--check-config` accepted the two default profiles.
- Tested native button launch, saved shortcut dispatch, and automatic Cocoa-Way startup while its compositor was stopped.
- Konsole and foot appeared as independent Cocoa-Way windows, with both application connections alive.
- Startup URL delivery before AppKit initialization is deferred until the interface is ready.
- Failed background SSH produces an error dialog. Initially observed GUI-only `No route to host`; after installing the locally signed build with a Local Network usage description, the OS permission was enabled and GUI connections succeeded. No system privacy settings were programmatically changed.
- No NAS reboot or changes to KWin, Sunshine, guest drivers, or GPU passthrough. No Tauri, clipboard, IME, reconnect-persistence, or Windows compatibility claims.

## Roadmap

1. Multiple instances per profile: distinct IDs, lifecycle, status, logs, start/stop and precise window activation.
2. Full GUI configuration: hosts, commands, working directories, environment variables, icons, validation, and shortcut creation.
3. Remote `.desktop` discovery and opt-in import, without blindly executing metadata supplied by applications.
4. Better diagnostics, log rotation, and distinguishing transport failure from normal application exit.
5. Packaging/notarization and a Windows WSLg launcher using the same profile model.

Persistent remote sessions, Chinese IME, clipboard and per-app Dock identities depend on the compositor/transport and require separate investigation. They are not provided by this thin launcher.

## Remove or roll back

Close connected Linux windows and quit Linux Apps, then move the three installed application bundles to Trash. Restore earlier bundles from the installer backup directory if needed. Profiles and logs can be retained. Cocoa-Way, Waypipe, SSH configuration, VM services and user data are left untouched.
