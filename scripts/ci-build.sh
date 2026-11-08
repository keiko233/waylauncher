#!/bin/bash
# Build in an isolated home so CI never reads or modifies a developer's data.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${RELEASE_VERSION:?Set RELEASE_VERSION to MAJOR.MINOR.PATCH}"
work=$(mktemp -d "${TMPDIR:-/tmp}/waylauncher-ci.XXXXXX")
trap 'rm -rf "$work"' EXIT
export GOPATH="$(go env GOPATH)" GOCACHE="$(go env GOCACHE)"
mkdir -p "$work/home/.config/waylauncher" "$work/apps" dist
cat > "$work/home/.config/waylauncher/profiles.toml" <<'PROFILE'
version = 2
[[hosts]]
id = "example"
ssh = "example.invalid"
[[apps]]
id = "example"
name = "Example"
host = "example"
command = ["example"]
PROFILE
HOME="$work/home" python3 scripts/install.py --dest "$work/apps" --backup-dir "$work/backups" --version "$RELEASE_VERSION"
app="$work/apps/Waylauncher.app"
HOME="$work/home" "$app/Contents/MacOS/waylauncher" --check-config --dry-run
codesign --verify --deep --strict "$app"
[[ $(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist") == "$RELEASE_VERSION" ]]
[[ $(/usr/bin/lipo -archs "$app/Contents/MacOS/waylauncher") == arm64 ]]
archive="Waylauncher-${RELEASE_VERSION}-macos-arm64.zip"
ditto -c -k --sequesterRsrc --keepParent "$app" "dist/$archive"
(cd dist && shasum -a 256 "$archive" > "$archive.sha256")
