// Package compositor defines what the launcher needs from a local Wayland
// compositor. Readiness means the compositor answers, never that any
// application window exists.
package compositor

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrUnsupported is returned for capabilities the compositor does not offer
// yet (for example precise window focus). Callers fall back to Activate.
var ErrUnsupported = errors.New("not supported by this compositor")

// Runtime tells a transport where to attach: the values for XDG_RUNTIME_DIR
// and WAYLAND_DISPLAY.
type Runtime struct {
	XDGRuntimeDir  string
	WaylandDisplay string
}

type Status struct {
	Version string
	PID     int
	Socket  string          // control socket that answered
	Raw     json.RawMessage // full response data, for diagnostics
}

type WindowHint struct {
	InstanceID string
	AppID      string
	Title      string
}

type Compositor interface {
	// Ensure starts the compositor if needed and waits until it answers.
	Ensure(ctx context.Context) error
	Status(ctx context.Context) (Status, error)
	Runtime(ctx context.Context) (Runtime, error)
	// Activate brings the compositor's windows forward, using precise
	// window focus when the compositor supports it.
	Activate(ctx context.Context, hint WindowHint) error
}
