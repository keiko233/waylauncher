package waypipe

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
)

func spec() transport.Spec {
	return transport.Spec{
		InstanceID: "konsole-ab12",
		SSHTarget:  "keiko@10.0.0.9",
		Remote:     remote.Spec{Command: []string{"konsole", "--separate", "--nofork"}, Cwd: "/home/keiko"},
		Runtime:    compositor.Runtime{XDGRuntimeDir: "/tmp/cocoa-way", WaylandDisplay: "wayland-0"},
	}
}

func TestBuildMatchesScript(t *testing.T) {
	tr := Transport{Environ: func() []string {
		return []string{"HOME=/Users/x", "WAYLAND_DISPLAY=stale", "COCOA_WAY_RUNTIME_DIR=/stale", "PATH=/weird", "XDG_RUNTIME_DIR=/stale"}
	}}
	cmd, err := tr.Build(spec())
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != "/opt/homebrew/bin/waypipe" {
		t.Fatalf("path = %s", cmd.Path)
	}
	want := []string{"--no-gpu", "--compress=lz4", "--threads=2", "ssh",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"keiko@10.0.0.9",
		"/usr/local/libexec/vm200-waypipe-app env -C /home/keiko -- konsole --separate --nofork"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args:\n got %q\nwant %q", cmd.Args, want)
	}
	for _, a := range cmd.Args {
		if strings.Contains(a, "StrictHostKeyChecking") {
			t.Fatal("host-key checking must stay on")
		}
	}
	wantEnv := []string{"HOME=/Users/x",
		"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_RUNTIME_DIR=/tmp/cocoa-way", "WAYLAND_DISPLAY=wayland-0"}
	if !reflect.DeepEqual(cmd.Env, wantEnv) {
		t.Fatalf("env = %q", cmd.Env)
	}
}

func TestBuildRejects(t *testing.T) {
	tests := map[string]func(*transport.Spec){
		"dash target":    func(s *transport.Spec) { s.SSHTarget = "-oProxyCommand=x" },
		"empty target":   func(s *transport.Spec) { s.SSHTarget = "" },
		"empty command":  func(s *transport.Spec) { s.Remote.Command = nil },
		"no wayland":     func(s *transport.Spec) { s.Runtime.WaylandDisplay = "" },
		"no runtime dir": func(s *transport.Spec) { s.Runtime.XDGRuntimeDir = "" },
	}
	for name, mutate := range tests {
		s := spec()
		mutate(&s)
		if _, err := (Transport{}).Build(s); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestNoPersistence(t *testing.T) {
	tr := Transport{}
	if tr.Persistent() || tr.Name() != "waypipe" {
		t.Fatal("waypipe is a non-persistent transport")
	}
	if _, err := tr.Attach(context.Background(), spec()); !errors.Is(err, transport.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := tr.Detach(context.Background(), spec()); !errors.Is(err, transport.ErrUnsupported) {
		t.Fatal(err)
	}
}
