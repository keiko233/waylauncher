# Project scope

This is the standalone `cocoa-way-launcher` project. Keep it a thin client around Cocoa-Way and Waypipe. The current version is intentionally simple; multiple-instance management and full GUI configuration are the next major features.

- Do not change NAS/PVE, VM GPU drivers, KWin, Sunshine or unrelated homelab services as part of launcher work.
- Preserve existing user profiles and installed application bundles; keep recoverable backups on reinstall.
- No credentials in source, profiles, logs used as fixtures, or commits. Maintain SSH host-key checking.
- Do not automatically restart remote application commands after disconnect; unsaved work and duplicate processes matter.
- Build with `python3 scripts/install-vm200-launcher.py`; validate with the installed executable's `--check-config` option and actual macOS UI launch checks.
- Generated binaries, user profiles and logs are not source artifacts. Do not commit them.
- Keep documentation clear about transport-process state versus actual window readiness, and normal SSH reconnect versus persistent application-session recovery.
