package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/remote"
	"github.com/keiko233/waylauncher/internal/transport/waypipe"
)

// LocalWaypipe reports the local waypipe binary and its version. It runs
// `waypipe --version`, which is local and read-only.
func LocalWaypipe(ctx context.Context) (string, string, error) {
	path := waypipe.DefaultBinary
	if _, err := os.Stat(path); err != nil {
		return path, "", errors.New("not found")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := execx.Run(ctx, path, "--version")
	if err != nil {
		return path, "", err
	}
	v := strings.TrimSpace(string(res.Stdout))
	if v == "" {
		v = strings.TrimSpace(string(res.Stderr))
	}
	return path, v, nil
}

// SSHCommand is what the user can run in Terminal to verify and accept a host
// key. The launcher itself never accepts keys.
func SSHCommand(target string) string { return "ssh " + remote.Quote(target) }

// RunChecks probes a host (one ssh call, BatchMode) and reports every check
// individually. It does not start Cocoa-Way or any remote application.
func (s *Service) RunChecks(hostID string) (HostReport, error) {
	s.mu.RLock()
	h := s.file.HostByID(hostID)
	s.mu.RUnlock()
	if h == nil {
		return HostReport{}, fmt.Errorf("unknown host %q", hostID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rep := HostReport{HostID: h.ID, SSH: h.SSH, At: time.Now(), Checks: []Check{}}
	chk, err := s.cfg.Prober.CheckRemote(ctx, h.SSH)
	switch {
	case err == nil:
		rep.Checks = append(rep.Checks,
			Check{ID: "ssh", Title: "SSH reachable", Kind: KindOK, Result: fmt.Sprintf("%d ms", chk.Latency.Milliseconds()),
				Detail: "BatchMode, 5 s timeout. No password or key prompts in the app."},
			Check{ID: "hostkey", Title: "Host key verified", Kind: KindOK, Result: "Trusted",
				Detail: "Matched your known_hosts. Strict checking stays on."},
			boolCheck("waypipe", "Remote waypipe", chk.WaypipeFound, "Found on PATH on "+h.ID+".", "Not found on PATH on "+h.ID+"."),
			boolCheck("wrapper", "Launch wrapper", chk.WrapperExecutable, remote.Wrapper+" (sets software rendering for forwarded apps).", remote.Wrapper+" is missing or not executable."),
		)
	default:
		kind := remote.KindOf(err)
		rep.Checks = append(rep.Checks,
			Check{ID: "ssh", Title: "SSH reachable", Kind: KindErr, Result: "Failed", Detail: err.Error()})
		hk := Check{ID: "hostkey", Title: "Host key verified", Kind: KindIdle, Result: "Not checked", Detail: "The connection did not get that far."}
		switch kind {
		case remote.KindHostKeyUnknown:
			hk.Kind, hk.Result, hk.Detail = KindErr, "Not trusted", "The key is not in your known_hosts."
			rep.Failure = &Failure{Kind: kind.String(), Title: "Host key not trusted",
				Message:    "The key for " + h.SSH + " isn’t in your known_hosts, so the connection was refused. Waylauncher never accepts new keys. Connect once from Terminal and verify the fingerprint, then run the checks again.",
				SSHCommand: SSHCommand(h.SSH)}
		case remote.KindHostKeyChanged:
			hk.Kind, hk.Result, hk.Detail = KindErr, "CHANGED", "The host key differs from the one in known_hosts."
			rep.Failure = &Failure{Kind: kind.String(), Title: "Host key has changed",
				Message:    "The key for " + h.SSH + " differs from the one you trusted. The connection was refused. Verify the host yourself, then fix known_hosts manually. Waylauncher will not override this.",
				SSHCommand: SSHCommand(h.SSH)}
		case remote.KindUnreachable, remote.KindTimeout:
			rep.Failure = &Failure{Kind: kind.String(), Title: "Host unreachable",
				Message:    "Check that the VM is running and the address is right. If Terminal reaches it but Waylauncher reports “No route to host”, allow Local Network access in System Settings › Privacy & Security › Local Network.",
				SSHCommand: SSHCommand(h.SSH)}
		case remote.KindAuth:
			rep.Failure = &Failure{Kind: kind.String(), Title: "Authentication failed",
				Message:    "Waylauncher can’t type passwords. Use a key in your ssh-agent or ~/.ssh/config, and check that Terminal logs in without a prompt.",
				SSHCommand: SSHCommand(h.SSH)}
		}
		rep.Checks = append(rep.Checks, hk)
	}

	lw := s.cfg.LocalWaypipeVersion
	if lw == nil {
		lw = LocalWaypipe
	}
	if path, ver, lerr := lw(ctx); lerr != nil {
		rep.Checks = append(rep.Checks, Check{ID: "local-waypipe", Title: "Local waypipe", Kind: KindErr, Result: "Missing",
			Detail: path + ": " + lerr.Error() + ". Install waypipe-darwin with Homebrew."})
	} else {
		rep.Checks = append(rep.Checks, Check{ID: "local-waypipe", Title: "Local waypipe", Kind: KindOK, Result: ver, Detail: path})
	}
	rep.Checks = append(rep.Checks, Check{ID: "persistence", Title: "Session persistence", Kind: KindIdle, Result: "None",
		Detail: "Waypipe has none: a dropped connection ends the app. A normal SSH reconnect starts a new copy, not the old one."})

	rep.Cocoa = s.RefreshCocoa(ctx)
	if rep.Cocoa.Reachable {
		if rt, rerr := s.cfg.Compositor.Runtime(ctx); rerr == nil {
			rep.Cocoa.Display = rt.WaylandDisplay
		}
	}
	s.mu.Lock()
	s.reports[h.ID] = rep
	s.cocoa = rep.Cocoa
	s.mu.Unlock()
	s.notify()
	return rep, nil
}

func boolCheck(id, title string, ok bool, okDetail, badDetail string) Check {
	if ok {
		return Check{ID: id, Title: title, Kind: KindOK, Result: "Found", Detail: okDetail}
	}
	return Check{ID: id, Title: title, Kind: KindErr, Result: "Missing", Detail: badDetail}
}

// Report returns the last diagnostics of a host, if any.
func (s *Service) Report(hostID string) (HostReport, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.reports[hostID]
	return r, ok
}
