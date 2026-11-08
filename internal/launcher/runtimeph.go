package launcher

import "github.com/keiko233/waylauncher/internal/compositor"

// runtimePlaceholder lets the preview build a command before Cocoa-Way has
// told the real runtime directory and display.
var runtimePlaceholder = compositor.Runtime{XDGRuntimeDir: "$TMPDIR/cocoa-way", WaylandDisplay: "wayland-N"}
