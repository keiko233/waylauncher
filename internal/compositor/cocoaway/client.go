// Package cocoaway talks to Cocoa-Way's control socket (protocol 1): one
// newline-terminated JSON request and one JSON response per connection.
package cocoaway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/execx"
)

const (
	ProtocolVersion  = 1
	maxResponseBytes = 4 << 20 // same frame limit as the server
)

// Client implements compositor.Compositor. Use New and adjust fields.
type Client struct {
	SocketPath     string
	AppPath        string        // Cocoa-Way.app to open when it is not running
	RuntimeDir     string        // directory holding wayland-* sockets
	CallTimeout    time.Duration // per request, default 2s
	EnsureTimeout  time.Duration // default 10s
	RuntimeTimeout time.Duration // wait for a Wayland socket, default 3s
	PollInterval   time.Duration // default 200ms
	Exec           execx.Exec
}

var _ compositor.Compositor = (*Client)(nil)

// New applies the documented defaults. COCOA_WAY_RUNTIME_DIR and
// WAYLAND_DISPLAY from the environment are deliberately ignored, as the Swift
// launcher also strips them: stale values would point at a dead compositor.
func New() *Client {
	home, _ := os.UserHomeDir()
	return &Client{
		SocketPath: DefaultSocketPath(os.Getenv),
		AppPath:    filepath.Join(home, "Applications", "Cocoa-Way.app"),
		RuntimeDir: filepath.Join(os.TempDir(), "cocoa-way"),
	}
}

func DefaultSocketPath(getenv func(string) string) string {
	if p := getenv("COCOA_WAY_CONTROL_SOCKET"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "cocoa-way", "control", "control.sock")
}

type request struct {
	Command string  `json:"command"`
	Session *string `json:"session"`
	Limit   int     `json:"limit"`
}

type Response struct {
	Protocol int             `json:"protocol"`
	OK       bool            `json:"ok"`
	Command  string          `json:"command"`
	Data     json.RawMessage `json:"data"`
	Error    string          `json:"error"`
}

// ProtocolError means the server speaks a different protocol version.
type ProtocolError struct{ Got int }

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("Cocoa-Way control protocol %d is not supported (this launcher speaks %d): update the launcher or Cocoa-Way", e.Got, ProtocolVersion)
}

// RemoteError is an ok=false response from Cocoa-Way.
type RemoteError struct{ Command, Message string }

func (e *RemoteError) Error() string {
	return fmt.Sprintf("Cocoa-Way rejected %q: %s", e.Command, e.Message)
}

func (c *Client) exec() execx.Exec {
	if c.Exec != nil {
		return c.Exec
	}
	return execx.Run
}

func dflt[T comparable](v, d T) T {
	var zero T
	if v == zero {
		return d
	}
	return v
}

// Call sends one command. session may be empty. limit <= 0 uses 100, the
// server default.
func (c *Client) Call(ctx context.Context, command, session string, limit int) (*Response, error) {
	timeout := dflt(c.CallTimeout, 2*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Cocoa-Way at %s: %w", c.SocketPath, err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)

	req := request{Command: command, Limit: dflt(limit, 100)}
	if session != "" {
		req.Session = &session
	}
	body, _ := json.Marshal(req)
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return nil, fmt.Errorf("send %q: %w", command, err)
	}

	line, err := bufio.NewReader(io.LimitReader(conn, maxResponseBytes+1)).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return nil, fmt.Errorf("read response to %q: %w", command, err)
	}
	if len(line) > maxResponseBytes {
		return nil, errors.New("control response exceeds the 4 MiB frame limit")
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("invalid response to %q: %w", command, err)
	}
	if resp.Protocol != ProtocolVersion {
		return nil, &ProtocolError{Got: resp.Protocol}
	}
	if !resp.OK {
		return &resp, &RemoteError{Command: command, Message: resp.Error}
	}
	return &resp, nil
}

// Status asks for "status". Only fields that are stable and useful are
// parsed; the complete payload stays in Raw.
func (c *Client) Status(ctx context.Context) (compositor.Status, error) {
	resp, err := c.Call(ctx, "status", "", 0)
	if err != nil {
		return compositor.Status{}, err
	}
	var d struct {
		Version       string `json:"version"`
		PID           int    `json:"pid"`
		ControlSocket string `json:"control_socket"`
	}
	if err := json.Unmarshal(resp.Data, &d); err != nil {
		return compositor.Status{}, fmt.Errorf("unexpected status payload: %w", err)
	}
	return compositor.Status{Version: d.Version, PID: d.PID, Socket: d.ControlSocket, Raw: resp.Data}, nil
}

// Ensure returns once Cocoa-Way answers on its control socket, opening the
// app first if it does not. This is compositor readiness only.
func (c *Client) Ensure(ctx context.Context) error {
	_, err := c.Status(ctx)
	if err == nil {
		return nil
	}
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return err // running but incompatible: opening it again would not help
	}
	if _, err := os.Stat(c.AppPath); err != nil {
		return fmt.Errorf("Cocoa-Way is not running and %s was not found: install it in ~/Applications", c.AppPath)
	}
	run := c.exec()
	if res, err := run(ctx, "/usr/bin/open", c.AppPath); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("cannot open %s: %v %s", c.AppPath, err, strings.TrimSpace(string(res.Stderr)))
	}
	ctx, cancel := context.WithTimeout(ctx, dflt(c.EnsureTimeout, 10*time.Second))
	defer cancel()
	tick := time.NewTicker(dflt(c.PollInterval, 200*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("Cocoa-Way did not become ready; check that Cocoa-Way.app starts")
		case <-tick.C:
			if _, err := c.Status(ctx); err == nil {
				return nil
			}
		}
	}
}

// Runtime locates the Wayland socket the transport must connect to. The
// status response does not currently publish it for the default compositor
// window, so this mirrors scripts/vm200-waypipe: the first socket matching
// <RuntimeDir>/wayland-*. It waits briefly because the socket can appear a
// moment after the control socket starts answering.
func (c *Client) Runtime(ctx context.Context) (compositor.Runtime, error) {
	ctx, cancel := context.WithTimeout(ctx, dflt(c.RuntimeTimeout, 3*time.Second))
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if name := findWaylandSocket(c.RuntimeDir); name != "" {
			return compositor.Runtime{XDGRuntimeDir: c.RuntimeDir, WaylandDisplay: name}, nil
		}
		select {
		case <-ctx.Done():
			return compositor.Runtime{}, fmt.Errorf("no Wayland socket in %s: is Cocoa-Way running?", c.RuntimeDir)
		case <-tick.C:
		}
	}
}

func findWaylandSocket(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "wayland-*"))
	sort.Strings(matches)
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Mode()&os.ModeSocket != 0 {
			return filepath.Base(m)
		}
	}
	return ""
}

// FocusWindow would raise one specific window. Cocoa-Way has no window
// commands yet (plan section 8.3), so this is always unsupported.
func (c *Client) FocusWindow(ctx context.Context, hint compositor.WindowHint) error {
	return compositor.ErrUnsupported
}

// Activate tries precise focus and falls back to bringing Cocoa-Way forward.
func (c *Client) Activate(ctx context.Context, hint compositor.WindowHint) error {
	err := c.FocusWindow(ctx, hint)
	if !errors.Is(err, compositor.ErrUnsupported) {
		return err
	}
	run := c.exec()
	res, err := run(ctx, "/usr/bin/open", "-a", c.AppPath)
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("cannot activate Cocoa-Way: %v %s", err, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}
