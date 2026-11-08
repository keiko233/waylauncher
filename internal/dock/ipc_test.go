package dock

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// shortDir is a temp directory with a path short enough for a Unix socket
// (t.TempDir includes the test name and can exceed the 104-byte limit).
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "wl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestMessageEncodeAndRead(t *testing.T) {
	var buf bytes.Buffer
	for _, m := range []Message{Hello("konsole", true), {Type: TypeReopen}, {Type: TypeQuitRequest}, Count(0), Count(3), {Type: TypeExit}} {
		buf.Write(m.Encode())
	}
	if !strings.Contains(buf.String(), `{"type":"count","n":0}`+"\n") {
		t.Fatalf("count 0 must carry n: %q", buf.String())
	}
	r := NewReader(&buf)
	var types []string
	for {
		m, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, m.Type)
	}
	if got := strings.Join(types, " "); got != "hello reopen quit-request count count exit" {
		t.Fatalf("got %q", got)
	}
}

func TestMessageValidation(t *testing.T) {
	for _, line := range []string{
		`{"type":"hello","app":"../etc"}`,
		`{"type":"hello"}`,
		`{"type":"count"}`,
		`{"type":"count","n":-1}`,
		`{"type":"bogus"}`,
		`not json`,
		`{"type":"hello","app":"` + strings.Repeat("a", 70) + `"}`,
	} {
		if _, err := NewReader(strings.NewReader(line + "\n")).Read(); err == nil {
			t.Errorf("accepted %s", line)
		}
	}
	long := `{"type":"hello","app":"x","pad":"` + strings.Repeat("a", maxLine) + `"}` + "\n"
	if _, err := NewReader(strings.NewReader(long)).Read(); err == nil {
		t.Error("accepted an over-long message")
	}
}

func TestRuntimeDirPermissions(t *testing.T) {
	d := filepath.Join(shortDir(t), "rt")
	if err := EnsureRuntimeDir(d); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", fi.Mode())
	}
	os.Chmod(d, 0o755)
	if err := EnsureRuntimeDir(d); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode not tightened: %v", fi.Mode())
	}
	if err := EnsureRuntimeDir(filepath.Join(d, strings.Repeat("x", 120))); err == nil {
		t.Error("over-long socket path accepted")
	}
	link := filepath.Join(shortDir(t), "l")
	os.Symlink(d, link)
	if err := EnsureRuntimeDir(link); err == nil {
		t.Error("symlinked runtime dir accepted")
	}
	if got := DefaultRuntimeDir(); len(SocketPath(got)) > maxSocketPath {
		t.Errorf("default socket path too long (%d): %s", len(SocketPath(got)), SocketPath(got))
	}
}

type recorder struct {
	mu      sync.Mutex
	keep    bool
	apps    map[string]bool
	events  []string
	changed chan struct{}
}

func newRecorder() *recorder {
	return &recorder{keep: true, changed: make(chan struct{}, 64)}
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.events = append(r.events, s)
	r.mu.Unlock()
	r.changed <- struct{}{}
}
func (r *recorder) Hello(app string, launch bool) bool {
	if r.apps != nil && !r.apps[app] {
		return false
	}
	if launch {
		r.add("launch " + app)
	}
	return r.keep
}
func (r *recorder) Reopen(app string)      { r.add("reopen " + app) }
func (r *recorder) QuitRequest(app string) { r.add("quit " + app) }
func (r *recorder) Gone(app string)        { r.add("gone " + app) }
func (r *recorder) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		r.mu.Lock()
		for _, e := range r.events {
			if e == want {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		select {
		case <-r.changed:
		case <-deadline:
			t.Fatalf("no %q in %v", want, r.events)
		}
	}
}

func recv(t *testing.T, c *Client) Message {
	t.Helper()
	type res struct {
		m   Message
		err error
	}
	ch := make(chan res, 1)
	go func() { m, err := c.Recv(); ch <- res{m, err} }()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.m
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a message")
	}
	return Message{}
}

func TestServerClientProtocol(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	rec := newRecorder()
	srv, err := Listen(sock, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if fi, _ := os.Stat(sock); fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", fi.Mode())
	}
	srv.SetCount("konsole", 2) // known before the helper connects

	c, err := Dial(sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Send(Hello("konsole", true))
	if m := recv(t, c); m.Type != TypeCount || *m.N != 2 {
		t.Fatalf("first message %+v", m)
	}
	rec.wait(t, "launch konsole")
	if !srv.Connected("konsole") {
		t.Fatal("not connected")
	}

	srv.SetCount("konsole", 1)
	if m := recv(t, c); *m.N != 1 {
		t.Fatalf("%+v", m)
	}
	srv.SetCount("konsole", 1) // unchanged: nothing is sent
	srv.SetCount("konsole", 0)
	if m := recv(t, c); m.Type != TypeCount || *m.N != 0 {
		t.Fatalf("%+v", m)
	}

	c.Send(Message{Type: TypeReopen})
	rec.wait(t, "reopen konsole")
	c.Send(Message{Type: TypeQuitRequest})
	rec.wait(t, "quit konsole")

	srv.Exit("konsole")
	if m := recv(t, c); m.Type != TypeExit {
		t.Fatalf("%+v", m)
	}
	c.Close()
	rec.wait(t, "gone konsole")
	if srv.Connected("konsole") {
		t.Fatal("still connected after close")
	}
}

func TestServerRejectsAndReplaces(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	rec := newRecorder()
	rec.apps = map[string]bool{"foot": true}
	srv, err := Listen(sock, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Unknown application: answered with exit.
	u, _ := Dial(sock, time.Second)
	u.Send(Hello("nope", false))
	if m := recv(t, u); m.Type != TypeExit {
		t.Fatalf("%+v", m)
	}
	u.Close()

	// A first message that is not hello closes the connection.
	b, _ := Dial(sock, time.Second)
	b.Send(Message{Type: TypeReopen})
	if _, err := b.Recv(); err == nil {
		t.Fatal("connection stayed open")
	}

	// A second helper for the same app replaces the first, which is told to exit.
	a1, _ := Dial(sock, time.Second)
	a1.Send(Hello("foot", false))
	recv(t, a1) // count
	a2, _ := Dial(sock, time.Second)
	a2.Send(Hello("foot", false))
	if m := recv(t, a1); m.Type != TypeExit {
		t.Fatalf("old helper got %+v", m)
	}
	recv(t, a2)
	srv.SetCount("foot", 4)
	if m := recv(t, a2); *m.N != 4 {
		t.Fatalf("%+v", m)
	}
	a2.Close()
}

func TestServerCloseTellsHelpersAndRemovesSocket(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	srv, err := Listen(sock, newRecorder())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := Dial(sock, time.Second)
	c.Send(Hello("foot", false))
	recv(t, c)
	srv.Close()
	if m, err := c.Recv(); err != nil || m.Type != TypeExit {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := c.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Fatal("socket file left behind")
	}
	if _, err := Dial(sock, 200*time.Millisecond); err == nil {
		t.Fatal("still accepting")
	}
}

func TestListenReplacesStaleSocketButNotLiveOne(t *testing.T) {
	sock := SocketPath(filepath.Join(shortDir(t), "rt"))
	srv, err := Listen(sock, newRecorder())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(sock, newRecorder()); err == nil {
		t.Fatal("second server took over a live socket")
	}
	// Simulate a crash: the file stays, nobody listens.
	srv.ln.Close()
	srv2, err := Listen(sock, newRecorder())
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	srv2.Close()
}
