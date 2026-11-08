// Package transport describes how a remote application is forwarded.
package transport

import (
	"context"
	"errors"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/remote"
)

// ErrUnsupported marks operations a transport cannot do (for example Detach
// on a transport without server-side sessions).
var ErrUnsupported = errors.New("not supported by this transport")

// Spec is everything a transport needs to start one instance.
type Spec struct {
	InstanceID string
	SSHTarget  string
	Remote     remote.Spec
	Runtime    compositor.Runtime
}

// Command is a process to run. The supervisor owns its lifecycle, stdio and
// process group; Env is the complete environment.
type Command struct {
	Path string
	Args []string
	Env  []string
}

type Transport interface {
	Name() string
	// Persistent reports whether the remote session survives a dropped
	// connection and can be reattached.
	Persistent() bool
	Build(Spec) (Command, error)
	// Attach and Detach apply to persistent transports only.
	Attach(ctx context.Context, s Spec) (Command, error)
	Detach(ctx context.Context, s Spec) error
}
