package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
)

type compositorReport struct {
	Reachable bool                `json:"reachable"`
	Error     string              `json:"error,omitempty"`
	Version   string              `json:"version,omitempty"`
	PID       int                 `json:"pid,omitempty"`
	Socket    string              `json:"socket"`
	Runtime   *compositor.Runtime `json:"runtime,omitempty"`
}

type hostReport struct {
	ID                string `json:"id"`
	Reachable         bool   `json:"reachable"`
	ErrorKind         string `json:"errorKind,omitempty"`
	Error             string `json:"error,omitempty"`
	LatencyMS         int64  `json:"latencyMs,omitempty"`
	WaypipeFound      bool   `json:"waypipeFound"`
	WrapperExecutable bool   `json:"wrapperExecutable"`
}

type statusReport struct {
	Compositor compositorReport `json:"compositor"`
	Hosts      []hostReport     `json:"hosts"`
}

// status is read-only: it never starts Cocoa-Way and never migrates profiles.
// "Reachable" means the control socket or ssh answered, not that any window exists.
func status(o options, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	comp := newCompositor(o)
	rep := statusReport{Hosts: []hostReport{}, Compositor: compositorReport{Socket: comp.SocketPath}}
	if st, err := comp.Status(ctx); err != nil {
		rep.Compositor.Error = err.Error()
	} else {
		rep.Compositor.Reachable, rep.Compositor.Version, rep.Compositor.PID = true, st.Version, st.PID
		if rt, err := comp.Runtime(ctx); err == nil {
			rep.Compositor.Runtime = &rt
		}
	}

	res, err := profile.Load(o.paths, true)
	if res == nil || res.File == nil {
		fmt.Fprintf(stderr, "profiles: %v\n", err)
		return 1
	}
	p := remote.Prober{}
	for _, h := range res.File.Hosts {
		hr := hostReport{ID: h.ID}
		if c, err := p.CheckRemote(ctx, h.SSH); err != nil {
			hr.ErrorKind, hr.Error = remote.KindOf(err).String(), err.Error()
		} else {
			hr.Reachable, hr.LatencyMS = true, c.Latency.Milliseconds()
			hr.WaypipeFound, hr.WrapperExecutable = c.WaypipeFound, c.WrapperExecutable
		}
		rep.Hosts = append(rep.Hosts, hr)
	}

	if o.json {
		printJSON(stdout, rep)
	} else {
		c := rep.Compositor
		if c.Reachable {
			fmt.Fprintf(stdout, "compositor: answering (Cocoa-Way %s, pid %d)\n", c.Version, c.PID)
			if c.Runtime != nil {
				fmt.Fprintf(stdout, "  runtime: %s/%s\n", c.Runtime.XDGRuntimeDir, c.Runtime.WaylandDisplay)
			} else {
				fmt.Fprintln(stdout, "  runtime: no Wayland socket found")
			}
		} else {
			fmt.Fprintf(stdout, "compositor: not reachable (%s)\n", c.Error)
		}
		for _, h := range rep.Hosts {
			if !h.Reachable {
				fmt.Fprintf(stdout, "host %s: %s\n", h.ID, h.Error)
				continue
			}
			fmt.Fprintf(stdout, "host %s: ssh ok in %d ms, waypipe %s, wrapper %s\n", h.ID, h.LatencyMS,
				yesNo(h.WaypipeFound, "found", "MISSING"), yesNo(h.WrapperExecutable, "executable", "MISSING"))
		}
	}
	if !rep.Compositor.Reachable {
		return 1
	}
	for _, h := range rep.Hosts {
		if !h.Reachable || !h.WaypipeFound || !h.WrapperExecutable {
			return 1
		}
	}
	return 0
}

func yesNo(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}
