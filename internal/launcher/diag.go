package launcher

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/i18n"
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
		return HostReport{}, errors.New(i18n.T("launcher.diag.err.unknownHost", "host", hostID))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rep := HostReport{HostID: h.ID, SSH: h.SSH, At: time.Now(), Checks: []Check{}}
	chk, err := s.cfg.Prober.CheckRemote(ctx, h.SSH)
	switch {
	case err == nil:
		rep.Checks = append(rep.Checks,
			Check{ID: "ssh", Title: i18n.T("launcher.diag.ssh.title"), Kind: KindOK, Result: i18n.T("launcher.diag.ssh.latency", "ms", chk.Latency.Milliseconds()),
				Detail: i18n.T("launcher.diag.ssh.okDetail")},
			Check{ID: "hostkey", Title: i18n.T("launcher.diag.hostkey.title"), Kind: KindOK, Result: i18n.T("launcher.diag.hostkey.trusted"),
				Detail: i18n.T("launcher.diag.hostkey.trustedDetail")},
			boolCheck("waypipe", i18n.T("launcher.diag.waypipe.title"), chk.WaypipeFound, i18n.T("launcher.diag.waypipe.found", "host", h.ID), i18n.T("launcher.diag.waypipe.missing", "host", h.ID)),
			boolCheck("wrapper", i18n.T("launcher.diag.wrapper.title"), chk.WrapperExecutable, i18n.T("launcher.diag.wrapper.found", "path", remote.Wrapper), i18n.T("launcher.diag.wrapper.missing", "path", remote.Wrapper)),
		)
	default:
		kind := remote.KindOf(err)
		rep.Checks = append(rep.Checks,
			Check{ID: "ssh", Title: i18n.T("launcher.diag.ssh.title"), Kind: KindErr, Result: i18n.T("launcher.diag.ssh.failed"), Detail: err.Error()})
		hk := Check{ID: "hostkey", Title: i18n.T("launcher.diag.hostkey.title"), Kind: KindIdle, Result: i18n.T("launcher.diag.hostkey.notChecked"), Detail: i18n.T("launcher.diag.hostkey.notCheckedDetail")}
		switch kind {
		case remote.KindHostKeyUnknown:
			hk.Kind, hk.Result, hk.Detail = KindErr, i18n.T("launcher.diag.hostkey.untrusted"), i18n.T("launcher.diag.hostkey.untrustedDetail")
			rep.Failure = &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.untrusted.title"),
				Message:    i18n.T("launcher.diag.fail.untrusted.message", "target", h.SSH),
				SSHCommand: SSHCommand(h.SSH)}
		case remote.KindHostKeyChanged:
			hk.Kind, hk.Result, hk.Detail = KindErr, i18n.T("launcher.diag.hostkey.changed"), i18n.T("launcher.diag.hostkey.changedDetail")
			rep.Failure = &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.changed.title"),
				Message:    i18n.T("launcher.diag.fail.changed.message", "target", h.SSH),
				SSHCommand: SSHCommand(h.SSH)}
		case remote.KindUnreachable, remote.KindTimeout:
			rep.Failure = &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.unreachable.title"),
				Message:    i18n.T("launcher.diag.fail.unreachable.message"),
				SSHCommand: SSHCommand(h.SSH)}
		case remote.KindAuth:
			rep.Failure = &Failure{Kind: kind.String(), Title: i18n.T("launcher.diag.fail.auth.title"),
				Message:    i18n.T("launcher.diag.fail.auth.message"),
				SSHCommand: SSHCommand(h.SSH)}
		}
		rep.Checks = append(rep.Checks, hk)
	}

	lw := s.cfg.LocalWaypipeVersion
	if lw == nil {
		lw = LocalWaypipe
	}
	if path, ver, lerr := lw(ctx); lerr != nil {
		rep.Checks = append(rep.Checks, Check{ID: "local-waypipe", Title: i18n.T("launcher.diag.local.title"), Kind: KindErr, Result: i18n.T("launcher.diag.missing"),
			Detail: i18n.T("launcher.diag.local.missingDetail", "path", path, "error", lerr.Error())})
	} else {
		rep.Checks = append(rep.Checks, Check{ID: "local-waypipe", Title: i18n.T("launcher.diag.local.title"), Kind: KindOK, Result: ver, Detail: path})
	}
	rep.Checks = append(rep.Checks, Check{ID: "persistence", Title: i18n.T("launcher.diag.persistence.title"), Kind: KindIdle, Result: i18n.T("launcher.diag.persistence.none"),
		Detail: i18n.T("launcher.diag.persistence.detail")})

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
		return Check{ID: id, Title: title, Kind: KindOK, Result: i18n.T("launcher.diag.found"), Detail: okDetail}
	}
	return Check{ID: id, Title: title, Kind: KindErr, Result: i18n.T("launcher.diag.missing"), Detail: badDetail}
}

// Report returns the last diagnostics of a host, if any.
func (s *Service) Report(hostID string) (HostReport, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.reports[hostID]
	return r, ok
}
