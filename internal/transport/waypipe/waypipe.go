// Package waypipe ports scripts/vm200-waypipe: Waypipe over SSH, forwarding
// to the local Cocoa-Way compositor. There is no session persistence; closing
// the connection ends the remote application.
package waypipe

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

const DefaultBinary = "/opt/homebrew/bin/waypipe"

// SearchPath is the PATH the Swift launcher gives the script, so that waypipe
// finds the same ssh and helpers regardless of how the launcher was started.
const SearchPath = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// sshOptions match the script run with VM200_SSH_BATCH=1.
var sshOptions = []string{
	"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1",
	"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
}

type Transport struct {
	Binary  string
	Environ func() []string // defaults to os.Environ
}

var _ transport.Transport = Transport{}

func (Transport) Name() string     { return "waypipe" }
func (Transport) Persistent() bool { return false }

func (t Transport) Build(s transport.Spec) (transport.Command, error) {
	if s.SSHTarget == "" || strings.HasPrefix(s.SSHTarget, "-") {
		return transport.Command{}, errors.New("invalid ssh target")
	}
	if len(s.Remote.Command) == 0 {
		return transport.Command{}, errors.New("empty command")
	}
	if s.Runtime.XDGRuntimeDir == "" || s.Runtime.WaylandDisplay == "" {
		return transport.Command{}, errors.New("compositor runtime directory or Wayland display is unknown; start Cocoa-Way first")
	}
	bin := t.Binary
	if bin == "" {
		bin = DefaultBinary
	}
	args := []string{"--no-gpu", "--compress=lz4", "--threads=2", "ssh"}
	args = append(args, sshOptions...)
	args = append(args, s.SSHTarget, remote.Command(s.Remote))

	environ := t.Environ
	if environ == nil {
		environ = os.Environ
	}
	return transport.Command{Path: bin, Args: args, Env: buildEnv(environ(), s)}, nil
}

// buildEnv drops variables that could point at a stale compositor (the Swift
// launcher does the same) and sets the ones waypipe needs.
func buildEnv(base []string, s transport.Spec) []string {
	drop := map[string]bool{
		"WAYLAND_DISPLAY": true, "COCOA_WAY_RUNTIME_DIR": true,
		"XDG_RUNTIME_DIR": true, "PATH": true,
	}
	var env []string
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			env = append(env, kv)
		}
	}
	return append(env,
		"PATH="+SearchPath,
		"XDG_RUNTIME_DIR="+s.Runtime.XDGRuntimeDir,
		"WAYLAND_DISPLAY="+s.Runtime.WaylandDisplay,
	)
}

func (Transport) Attach(context.Context, transport.Spec) (transport.Command, error) {
	return transport.Command{}, transport.ErrUnsupported
}

func (Transport) Detach(context.Context, transport.Spec) error { return transport.ErrUnsupported }
