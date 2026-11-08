package main

// DEVELOPMENT ONLY. WAYLAUNCHER_FAKE_TRANSPORT=1 replaces Cocoa-Way, SSH and
// Waypipe with stand-ins so the launcher (instances, Dock helpers, quitting)
// can be exercised without a Linux machine: the "transport" is
// `sleep 600`, the compositor always answers, the SSH probe always succeeds
// and activating the compositor does nothing. No remote application is ever
// started. To keep it away from real data, it refuses to run unless every
// directory flag points somewhere other than the real defaults.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

const fakeEnv = "WAYLAUNCHER_FAKE_TRANSPORT"

func fakeRequested() bool { return os.Getenv(fakeEnv) == "1" }

// checkFakeIsolated returns an error naming a flag that still has its real default.
func checkFakeIsolated(o options, def profile.Paths) error {
	for _, c := range []struct{ name, got, def string }{
		{"--config-dir", o.paths.ConfigDir, def.ConfigDir},
		{"--legacy-dir", o.paths.LegacyDir, def.LegacyDir},
		{"--backup-dir", o.paths.BackupDir, def.BackupDir},
		{"--cache-dir", o.paths.CacheDir, def.CacheDir},
		{"--log-dir", o.logDir, ""},
		{"--dock-bundle-dir", o.dockBundleDir, ""},
		{"--runtime-dir", o.runtimeDir, ""},
	} {
		if c.got == "" || c.got == c.def {
			return fmt.Errorf("%s=1 is for development and needs an explicit %s (not the real default)", fakeEnv, c.name)
		}
	}
	return nil
}

// fakeExec stands in for ssh in application discovery: a scan answers with
// two canned desktop files; every other call (icon lookup) answers with
// nothing, so the entries keep generated tiles. Nothing is executed.
func fakeExec(_ context.Context, _ string, args ...string) (execx.Result, error) {
	if len(args) > 0 && strings.Contains(args[len(args)-1], "WLEND") {
		var b strings.Builder
		b.WriteString("\x01WLENV KDE\n")
		for _, f := range []struct{ id, body string }{
			{"org.fake.Editor", "[Desktop Entry]\nType=Application\nName=Fake Editor\nComment=Canned entry (fake transport)\nExec=fake-editor %F\n"},
			{"org.fake.Terminal", "[Desktop Entry]\nType=Application\nName=Fake Terminal\nExec=fake-terminal --new\nTerminal=false\n"},
		} {
			fmt.Fprintf(&b, "\x01WLD 1 %d %s\n%s\n", len(f.body), f.id, f.body)
		}
		b.WriteString("\x01WLEND 2\n")
		return execx.Result{Stdout: []byte(b.String())}, nil
	}
	return execx.Result{}, nil
}

type fakeCompositor struct{}

func (fakeCompositor) Ensure(context.Context) error { return nil }
func (fakeCompositor) Status(context.Context) (compositor.Status, error) {
	return compositor.Status{Version: "fake", Socket: "fake"}, nil
}
func (fakeCompositor) Runtime(context.Context) (compositor.Runtime, error) {
	return compositor.Runtime{XDGRuntimeDir: os.TempDir(), WaylandDisplay: "fake-0"}, nil
}
func (fakeCompositor) Activate(context.Context, compositor.WindowHint) error { return nil }

type fakeProber struct{}

func (fakeProber) Probe(context.Context, string) (remote.ProbeResult, error) {
	return remote.ProbeResult{Latency: time.Millisecond}, nil
}
func (fakeProber) CheckRemote(context.Context, string) (remote.Check, error) {
	return remote.Check{}, errors.New("fake transport: no remote checks")
}

type fakeTransport struct{}

func (fakeTransport) Name() string     { return "waypipe" }
func (fakeTransport) Persistent() bool { return false }
func (fakeTransport) Build(transport.Spec) (transport.Command, error) {
	return transport.Command{Path: "/bin/sleep", Args: []string{"600"}, Env: os.Environ()}, nil
}
func (fakeTransport) Attach(context.Context, transport.Spec) (transport.Command, error) {
	return transport.Command{}, transport.ErrUnsupported
}
func (fakeTransport) Detach(context.Context, transport.Spec) error { return transport.ErrUnsupported }
