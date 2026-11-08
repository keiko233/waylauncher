package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/execx"
)

// SSHOptions are the non-interactive options used for every probe. Host-key
// checking is deliberately left at the user's ssh configuration (on).
var SSHOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1"}

type ErrKind int

const (
	KindOther ErrKind = iota
	KindHostKeyUnknown
	KindHostKeyChanged
	KindUnreachable
	KindAuth
	KindTimeout
)

func (k ErrKind) String() string {
	return [...]string{"other", "host-key-unknown", "host-key-changed", "unreachable", "auth", "timeout"}[k]
}

// Error is a classified SSH failure.
type Error struct {
	Kind   ErrKind
	Target string
	Detail string // ssh stderr, trimmed
}

func (e *Error) Error() string {
	var msg string
	switch e.Kind {
	case KindHostKeyUnknown:
		msg = "host key is not trusted yet; connect once in Terminal to verify and accept it"
	case KindHostKeyChanged:
		msg = "host key has CHANGED; refusing to connect. Verify the host, then fix known_hosts manually"
	case KindUnreachable:
		msg = "host is unreachable (check the VM and network; on macOS also Local Network permission)"
	case KindAuth:
		msg = "authentication failed (BatchMode cannot prompt; check your key or ssh-agent)"
	case KindTimeout:
		msg = "timed out"
	default:
		msg = "ssh failed"
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s: %s", e.Target, msg, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Target, msg)
}

// Retryable reports whether trying again could plausibly succeed. Trust and
// authentication problems need user action, so they are not retried.
func (e *Error) Retryable() bool {
	return e.Kind == KindUnreachable || e.Kind == KindTimeout || e.Kind == KindOther
}

func KindOf(err error) ErrKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindOther
}

// Classify maps ssh stderr to an error kind. The "changed" marker is checked
// first because ssh prints "Host key verification failed." in both cases.
func Classify(stderr string) ErrKind {
	switch {
	case strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return KindHostKeyChanged
	case strings.Contains(stderr, "Host key verification failed"):
		return KindHostKeyUnknown
	case strings.Contains(stderr, "Permission denied"):
		return KindAuth
	case strings.Contains(stderr, "No route to host"),
		strings.Contains(stderr, "Network is unreachable"),
		strings.Contains(stderr, "Connection refused"),
		strings.Contains(stderr, "Could not resolve hostname"),
		strings.Contains(stderr, "Connection timed out"),
		strings.Contains(stderr, "Operation timed out"):
		return KindUnreachable
	}
	return KindOther
}

// Prober runs ssh checks. The zero value uses /usr/bin/ssh and a real exec.
type Prober struct {
	SSH     string
	Exec    execx.Exec
	Timeout time.Duration // overall bound per call; default 8s
}

type ProbeResult struct{ Latency time.Duration }

func (p Prober) run(ctx context.Context, target string, remoteArgs ...string) (execx.Result, time.Duration, error) {
	if target == "" || strings.HasPrefix(target, "-") {
		return execx.Result{}, 0, fmt.Errorf("invalid ssh target %q", target)
	}
	ssh, run, timeout := p.SSH, p.Exec, p.Timeout
	if ssh == "" {
		ssh = "/usr/bin/ssh"
	}
	if run == nil {
		run = execx.Run
	}
	if timeout == 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append(append([]string{}, SSHOptions...), target)
	args = append(args, remoteArgs...)
	start := time.Now()
	res, err := run(ctx, ssh, args...)
	lat := time.Since(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return res, lat, &Error{Kind: KindTimeout, Target: target}
		}
		return res, lat, err
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(string(res.Stderr))
		return res, lat, &Error{Kind: Classify(detail), Target: target, Detail: detail}
	}
	return res, lat, nil
}

// Probe runs `ssh <opts> <target> true` and measures round-trip time.
func (p Prober) Probe(ctx context.Context, target string) (ProbeResult, error) {
	_, lat, err := p.run(ctx, target, "true")
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Latency: lat}, nil
}

// Check reports whether waypipe is on the remote PATH and the wrapper is
// executable, using a single ssh call.
type Check struct {
	WaypipeFound      bool
	WrapperExecutable bool
	Latency           time.Duration
}

func (p Prober) CheckRemote(ctx context.Context, target string) (Check, error) {
	// `sh -c` keeps this independent of the user's login shell.
	script := "command -v waypipe >/dev/null 2>&1 && echo waypipe=yes || echo waypipe=no; " +
		"test -x " + Quote(Wrapper) + " && echo wrapper=yes || echo wrapper=no"
	res, lat, err := p.run(ctx, target, "sh -c "+Quote(script))
	if err != nil {
		return Check{}, err
	}
	out := string(res.Stdout)
	return Check{
		WaypipeFound:      strings.Contains(out, "waypipe=yes"),
		WrapperExecutable: strings.Contains(out, "wrapper=yes"),
		Latency:           lat,
	}, nil
}
