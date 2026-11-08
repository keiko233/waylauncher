#!/usr/bin/env python3
"""Build the native macOS launcher; preserve existing profiles and app bundles."""
import datetime
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parent.parent
home = Path.home()
apps = home / "Applications"
config = home / ".config/vm200-launcher/profiles.json"
source = root / "clients/macos/launcher"
if os.uname().sysname != "Darwin":
    raise SystemExit("This installer requires macOS and Xcode Command Line Tools")


def bundle(directory, name, executable, identifier, **extra):
    contents = directory / "Contents"
    (contents / "MacOS").mkdir(parents=True)
    (contents / "Resources").mkdir()
    info = dict(CFBundleIdentifier=identifier, CFBundleName=name,
                CFBundleDisplayName=name, CFBundleExecutable=executable,
                CFBundlePackageType="APPL", NSHighResolutionCapable=True,
                LSUIElement=True,
                NSLocalNetworkUsageDescription="Connect to your Linux machine over SSH to display its applications.",
                **extra)
    (contents / "Info.plist").write_bytes(plistlib.dumps(info))
    return contents


apps.mkdir(exist_ok=True)
stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S-%f")
with tempfile.TemporaryDirectory(prefix="vm200-launcher-") as temp:
    temp = Path(temp)
    main = temp / "Linux Apps.app"
    contents = bundle(main, "Linux Apps", "linux-apps", "local.homelab.vm200-launcher",
                      CFBundleURLTypes=[dict(CFBundleURLName="VM200 Applications",
                                            CFBundleURLSchemes=["vm200-apps"])])
    subprocess.run(["swiftc", "-swift-version", "5", "-O", str(source / "main.swift"),
                    "-o", str(contents / "MacOS/linux-apps")], check=True)
    shutil.copy2(root / "scripts/vm200-waypipe", contents / "Resources/vm200-waypipe")
    (contents / "Resources/vm200-waypipe").chmod(0o755)
    # Use one manager for all shortcuts, so repeated clicks share running state.
    for profile in json.loads((source / "profiles.json").read_text()):
        directory = temp / (profile["name"] + ".app")
        content = bundle(directory, profile["name"], "launch",
                         "local.homelab.vm200-shortcut." + profile["id"])
        script = content / "MacOS/launch"
        script.write_text('#!/bin/sh\nexec /usr/bin/open -a "$HOME/Applications/Linux Apps.app" '
                          + "'vm200-apps://" + profile["id"] + "'\n")
        script.chmod(0o755)
    if not config.exists():
        config.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        # Exclusive creation avoids replacing user edits on repeated installs.
        with config.open("x") as f:
            f.write((source / "profiles.json").read_text())
        config.chmod(0o600)
    for built in temp.glob("*.app"):
        target = apps / built.name
        if target.exists():
            backup = home / "Library/Application Support/VM200 Launcher/Backups" / stamp
            backup.mkdir(parents=True, exist_ok=True)
            shutil.move(str(target), str(backup / target.name))
        shutil.copytree(built, target)
        subprocess.run(["codesign", "--force", "--sign", "-", str(target)], check=True)
    subprocess.run([str(apps / "Linux Apps.app/Contents/MacOS/linux-apps"), "--check-config"], check=True)
print("Installed Linux Apps, Linux Terminal and Linux foot in ~/Applications")
