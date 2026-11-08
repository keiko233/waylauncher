package remote

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/keiko233/waylauncher/internal/execx"
)

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"konsole", "konsole"},
		{"--separate", "--separate"},
		{"/usr/bin/x-y_z.1", "/usr/bin/x-y_z.1"},
		{"", "''"},
		{"two words", "'two words'"},
		{"it's", `'it'\''s'`},
		{"$HOME", "'$HOME'"},
		{"`id`", "'`id`'"},
		{"a;b|c&d", "'a;b|c&d'"},
		{"line1\nline2", "'line1\nline2'"},
		{`back\slash`, `'back\slash'`},
		{`"dq"`, `'"dq"'`},
		{"*.txt", "'*.txt'"},
		{"~", "'~'"},
		{"é", "'é'"},
	}
	for _, tc := range tests {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestArgv(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want []string
	}{
		{"plain", Spec{Command: []string{"foot"}},
			[]string{Wrapper, "env", "--", "foot"}},
		{"cwd", Spec{Command: []string{"konsole", "--nofork"}, Cwd: "/home/k"},
			[]string{Wrapper, "env", "-C", "/home/k", "--", "konsole", "--nofork"}},
		{"env sorted", Spec{Command: []string{"x"}, Env: map[string]string{"B": "2", "A": "1"}},
			[]string{Wrapper, "env", "A=1", "B=2", "--", "x"}},
		{"dash command", Spec{Command: []string{"-weird", "a=b"}},
			[]string{Wrapper, "env", "--", "-weird", "a=b"}},
	}
	for _, tc := range tests {
		if got := Argv(tc.spec); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// The strongest quoting check: feed the string to a real POSIX shell and
// confirm every element survives byte for byte.
func TestCommandRoundTripThroughShell(t *testing.T) {
	nasty := []string{"plain", "with space", "it's", `say "hi"`, "$HOME", "$(id)", "`id`",
		"a\nb", "tab\there", `back\slash`, "*", "~", "", "semi;colon", "-dash", "ünï"}
	spec := Spec{Command: nasty, Cwd: "/tmp/dir with space", Env: map[string]string{"K": "v a'l$"}}
	argv := Argv(spec)
	// Replace the wrapper with printf so the shell prints each word NUL-terminated.
	cmdline := "printf '%s\\0' " + Join(argv[1:])
	out, err := exec.Command("/bin/sh", "-c", cmdline).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if !reflect.DeepEqual(got, argv[1:]) {
		t.Fatalf("round trip mismatch:\n got %q\nwant %q", got, argv[1:])
	}
	if !strings.HasPrefix(Command(spec), Wrapper+" env -C '/tmp/dir with space' ") {
		t.Fatalf("command = %s", Command(spec))
	}
}

func fakeExec(res execx.Result, err error, got *[]string) execx.Exec {
	return func(ctx context.Context, name string, args ...string) (execx.Result, error) {
		*got = append([]string{name}, args...)
		return res, err
	}
}

func TestProbeArgvAndSuccess(t *testing.T) {
	var got []string
	p := Prober{SSH: "/usr/bin/ssh", Exec: fakeExec(execx.Result{}, nil, &got)}
	r, err := p.Probe(context.Background(), "keiko@10.0.0.9")
	if err != nil || r.Latency < 0 {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", "keiko@10.0.0.9", "true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q", got)
	}
	for _, a := range got {
		if strings.Contains(a, "StrictHostKeyChecking") {
			t.Fatal("host-key checking must never be disabled")
		}
	}
}

func TestProbeErrorClassification(t *testing.T) {
	tests := []struct {
		stderr string
		kind   ErrKind
		retry  bool
	}{
		{"Host key verification failed.\n", KindHostKeyUnknown, false},
		{"@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@\nHost key verification failed.\n", KindHostKeyChanged, false},
		{"ssh: connect to host 10.0.0.9 port 22: No route to host", KindUnreachable, true},
		{"ssh: connect to host 10.0.0.9 port 22: Connection refused", KindUnreachable, true},
		{"ssh: Could not resolve hostname nope: nodename nor servname provided", KindUnreachable, true},
		{"keiko@10.0.0.9: Permission denied (publickey).", KindAuth, false},
		{"something odd", KindOther, true},
	}
	for _, tc := range tests {
		var got []string
		p := Prober{Exec: fakeExec(execx.Result{ExitCode: 255, Stderr: []byte(tc.stderr)}, nil, &got)}
		_, err := p.Probe(context.Background(), "h")
		var e *Error
		if !errors.As(err, &e) || e.Kind != tc.kind || e.Retryable() != tc.retry {
			t.Errorf("stderr %q: err = %v (kind %v)", tc.stderr, err, KindOf(err))
		}
		if err != nil && e.Detail == "" {
			t.Errorf("detail missing for %q", tc.stderr)
		}
	}
}

func TestProbeTimeoutAndBadTarget(t *testing.T) {
	timeout := func(ctx context.Context, name string, args ...string) (execx.Result, error) {
		<-ctx.Done()
		return execx.Result{}, ctx.Err()
	}
	p := Prober{Exec: timeout, Timeout: 20_000_000}
	if _, err := p.Probe(context.Background(), "h"); KindOf(err) != KindTimeout {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"", "-oProxyCommand=x"} {
		if _, err := p.Probe(context.Background(), bad); err == nil || KindOf(err) == KindTimeout {
			t.Fatalf("target %q should be rejected: %v", bad, err)
		}
	}
}

func TestCheckRemote(t *testing.T) {
	tests := []struct {
		out           string
		waypipe, wrap bool
	}{
		{"waypipe=yes\nwrapper=yes\n", true, true},
		{"waypipe=no\nwrapper=yes\n", false, true},
		{"waypipe=yes\nwrapper=no\n", true, false},
	}
	for _, tc := range tests {
		var got []string
		p := Prober{Exec: fakeExec(execx.Result{Stdout: []byte(tc.out)}, nil, &got)}
		c, err := p.CheckRemote(context.Background(), "h")
		if err != nil || c.WaypipeFound != tc.waypipe || c.WrapperExecutable != tc.wrap {
			t.Errorf("%q: %+v %v", tc.out, c, err)
		}
		last := got[len(got)-1]
		if !strings.HasPrefix(last, "sh -c ") || !strings.Contains(last, "command -v waypipe") || !strings.Contains(last, Wrapper) {
			t.Errorf("remote command = %s", last)
		}
	}
}
