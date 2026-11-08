package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor/cocoaway"
	"github.com/keiko233/waylauncher/internal/instance"
	"github.com/keiko233/waylauncher/internal/profile"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport"
	"github.com/keiko233/waylauncher/internal/transport/waypipe"
)

func newCompositor(o options) *cocoaway.Client {
	c := cocoaway.New()
	if o.cocoaWayApp != "" {
		c.AppPath = o.cocoaWayApp
	}
	return c
}

// launch runs one application in the foreground until it ends. Ctrl-C stops
// it. The remote application is never restarted.
func launch(o options, appID string, stdout, stderr io.Writer) int {
	res, err := profile.Load(o.paths, false)
	if err != nil {
		fmt.Fprintf(stderr, "profiles: %v\n", err)
		return 1
	}
	app := res.File.AppByID(appID)
	if app == nil {
		fmt.Fprintf(stderr, "unknown application %q\n", appID)
		return 1
	}
	host := res.File.HostByID(app.Host)
	settings, _, err := profile.LoadSettings(o.paths)
	if err != nil {
		fmt.Fprintf(stderr, "settings: %v\n", err)
		return 1
	}
	logDir := o.logDir
	if logDir == "" {
		if logDir, err = instance.DefaultLogDir(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	comp := newCompositor(o)
	m := instance.New(instance.Config{
		Compositor: comp,
		Transports: map[string]transport.Transport{"waypipe": waypipe.Transport{}},
		Prober:     remote.Prober{},
		Settings:   func() profile.Settings { return settings },
		Logs:       &instance.LogStore{Dir: logDir},
	})
	m.Subscribe(func(e instance.Event) {
		s := e.Instance
		if o.json {
			printJSON(stdout, s)
			return
		}
		line := fmt.Sprintf("%s: %s", s.ID, s.State)
		if s.Reason != "" {
			line += " (" + s.Reason + ")"
		}
		fmt.Fprintln(stdout, line)
	})

	lr, err := m.Launch(*app, *host)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if lr.Action == instance.ActionFocus { // cannot happen in a fresh process; kept for the UI contract
		_ = m.Focus(context.Background(), lr.Instance.ID)
		return 0
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-sig
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = m.StopAll(stopCtx)
	}()
	final, err := m.Wait(ctx, lr.Instance.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	m.Drain(drainCtx)
	drainCancel()
	if final.State == instance.Exited {
		return 0
	}
	if final.LogPath != "" {
		fmt.Fprintf(stderr, "log: %s\n", final.LogPath)
	}
	return 1
}
