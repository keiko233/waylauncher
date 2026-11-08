package cocoaway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/keiko233/waylauncher/internal/compositor"
	"github.com/keiko233/waylauncher/internal/execx"
)

// shortDir returns a directory with a short path: unix socket paths are
// limited to about 104 bytes on macOS, which t.TempDir() can exceed.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "cw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// fakeServer answers each connection with reply(requestLine) and records requests.
type fakeServer struct {
	requests chan string
}

func serve(t *testing.T, path string, reply func(req string) string) *fakeServer {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	fs := &fakeServer{requests: make(chan string, 16)}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				fs.requests <- line
				if out := reply(line); out != "" {
					conn.Write([]byte(out))
				}
			}()
		}
	}()
	return fs
}

func newClient(t *testing.T) (*Client, string) {
	d := shortDir(t)
	c := &Client{SocketPath: filepath.Join(d, "c.sock"), RuntimeDir: d, CallTimeout: time.Second}
	return c, d
}

const okStatus = `{"protocol":1,"ok":true,"command":"status","data":{"version":"2.1.2","pid":4242,"control_socket":"/x/c.sock","active_sessions":[]}}` + "\n"

func TestStatusAndRequestFraming(t *testing.T) {
	c, _ := newClient(t)
	srv := serve(t, c.SocketPath, func(string) string { return okStatus })
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "2.1.2" || st.PID != 4242 || st.Socket != "/x/c.sock" || !strings.Contains(string(st.Raw), "active_sessions") {
		t.Fatalf("status = %+v", st)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(<-srv.requests), &req); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"command": "status", "session": nil, "limit": float64(100)}
	if !reflect.DeepEqual(req, want) {
		t.Fatalf("request = %v", req)
	}
}

func TestSessionAndLimitSent(t *testing.T) {
	c, _ := newClient(t)
	srv := serve(t, c.SocketPath, func(string) string { return okStatus })
	if _, err := c.Call(context.Background(), "logs", "s1", 7); err != nil {
		t.Fatal(err)
	}
	if got := <-srv.requests; !strings.Contains(got, `"session":"s1"`) || !strings.Contains(got, `"limit":7`) {
		t.Fatalf("request = %s", got)
	}
}

func TestCallErrors(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		check func(error) bool
	}{
		{"wrong protocol", `{"protocol":2,"ok":true,"command":"status","data":{}}` + "\n",
			func(e error) bool {
				var pe *ProtocolError
				return errors.As(e, &pe) && pe.Got == 2 && strings.Contains(e.Error(), "update the launcher or Cocoa-Way")
			}},
		{"ok false", `{"protocol":1,"ok":false,"command":"status","data":null,"error":"boom"}` + "\n",
			func(e error) bool {
				var re *RemoteError
				return errors.As(e, &re) && re.Message == "boom"
			}},
		{"garbage", "not json\n", func(e error) bool { return e != nil && strings.Contains(e.Error(), "invalid response") }},
		{"closed without reply", "", func(e error) bool { return e != nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newClient(t)
			serve(t, c.SocketPath, func(string) string { return tc.reply })
			_, err := c.Status(context.Background())
			if !tc.check(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestTimeoutWhenServerSilent(t *testing.T) {
	c, _ := newClient(t)
	c.CallTimeout = 100 * time.Millisecond
	l, err := net.Listen("unix", c.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() { // accept and never answer
		conn, _ := l.Accept()
		time.Sleep(time.Second)
		if conn != nil {
			conn.Close()
		}
	}()
	start := time.Now()
	if _, err := c.Status(context.Background()); err == nil || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("err=%v elapsed=%v", err, time.Since(start))
	}
}

func TestNoSocket(t *testing.T) {
	c, _ := newClient(t)
	if _, err := c.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot connect to Cocoa-Way") {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultSocketPath(t *testing.T) {
	if got := DefaultSocketPath(func(string) string { return "/custom.sock" }); got != "/custom.sock" {
		t.Fatal(got)
	}
	got := DefaultSocketPath(func(string) string { return "" })
	if !strings.HasSuffix(got, "cocoa-way/control/control.sock") || !strings.HasPrefix(got, os.TempDir()) {
		t.Fatal(got)
	}
}

func TestEnsureAlreadyRunningDoesNotOpen(t *testing.T) {
	c, _ := newClient(t)
	serve(t, c.SocketPath, func(string) string { return okStatus })
	c.Exec = func(context.Context, string, ...string) (execx.Result, error) {
		t.Fatal("open must not be called when the compositor answers")
		return execx.Result{}, nil
	}
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureOpensThenPolls(t *testing.T) {
	c, d := newClient(t)
	c.AppPath = d // any existing path
	c.PollInterval = 10 * time.Millisecond
	var opened []string
	c.Exec = func(_ context.Context, name string, args ...string) (execx.Result, error) {
		opened = append([]string{name}, args...)
		// The "app" starts listening shortly after open returns.
		go func() {
			time.Sleep(60 * time.Millisecond)
			serve(t, c.SocketPath, func(string) string { return okStatus })
		}()
		return execx.Result{}, nil
	}
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened, []string{"/usr/bin/open", d}) {
		t.Fatalf("opened = %v", opened)
	}
}

func TestEnsureDeadlineAndMissingApp(t *testing.T) {
	c, d := newClient(t)
	c.AppPath = filepath.Join(d, "missing.app")
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("err = %v", err)
	}
	c.AppPath = d
	c.EnsureTimeout = 80 * time.Millisecond
	c.PollInterval = 10 * time.Millisecond
	c.Exec = func(context.Context, string, ...string) (execx.Result, error) { return execx.Result{}, nil }
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureProtocolMismatchDoesNotOpen(t *testing.T) {
	c, _ := newClient(t)
	serve(t, c.SocketPath, func(string) string { return `{"protocol":9,"ok":true,"command":"status","data":{}}` + "\n" })
	c.Exec = func(context.Context, string, ...string) (execx.Result, error) {
		t.Fatal("must not open")
		return execx.Result{}, nil
	}
	var pe *ProtocolError
	if err := c.Ensure(context.Background()); !errors.As(err, &pe) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeFindsSocketNotLockFile(t *testing.T) {
	c, d := newClient(t)
	c.RuntimeTimeout = 100 * time.Millisecond
	if _, err := c.Runtime(context.Background()); err == nil {
		t.Fatal("expected error without a socket")
	}
	os.WriteFile(filepath.Join(d, "wayland-0.lock"), nil, 0o600) // regular file: ignored
	l, err := net.Listen("unix", filepath.Join(d, "wayland-0"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	rt, err := c.Runtime(context.Background())
	if err != nil || rt.WaylandDisplay != "wayland-0" || rt.XDGRuntimeDir != d {
		t.Fatalf("rt=%+v err=%v", rt, err)
	}
}

func TestFocusUnsupportedAndActivateFallsBack(t *testing.T) {
	c, d := newClient(t)
	c.AppPath = d
	if err := c.FocusWindow(context.Background(), compositor.WindowHint{}); !errors.Is(err, compositor.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	var got []string
	c.Exec = func(_ context.Context, name string, args ...string) (execx.Result, error) {
		got = append([]string{name}, args...)
		return execx.Result{}, nil
	}
	if err := c.Activate(context.Background(), compositor.WindowHint{AppID: "x"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"/usr/bin/open", "-a", d}) {
		t.Fatalf("got %v", got)
	}
}
