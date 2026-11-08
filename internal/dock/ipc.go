// Package dock gives each running Linux application its own Dock icon.
//
// All Linux windows are drawn by the single Cocoa-Way process, so macOS shows
// one Dock icon for all of them. To get a Dock entry per application,
// Waylauncher generates a small windowless app bundle per profile (bundle.go)
// and runs one helper process per application while it has live instances.
// The helper holds the icon and the running dot; Waylauncher owns every
// decision. They talk over a Unix socket (this file): newline-delimited JSON,
// one object per line.
//
//	helper -> Waylauncher   hello{app,launch}  reopen  quit-request
//	Waylauncher -> helper   count{n}           exit
//
// The helper exits when the connection closes, so a crashed or killed
// Waylauncher never leaves helpers behind.
package dock

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"
)

// Message types.
const (
	TypeHello       = "hello"        // helper -> app: identifies the helper
	TypeReopen      = "reopen"       // helper -> app: Dock icon clicked while running
	TypeQuitRequest = "quit-request" // helper -> app: Quit chosen in the Dock menu
	TypeCount       = "count"        // app -> helper: number of live instances
	TypeExit        = "exit"         // app -> helper: leave now
)

// maxLine bounds one message; anything longer is a protocol error.
const maxLine = 4096

// maxSocketPath is below the 104-byte sun_path limit of macOS.
const maxSocketPath = 100

type Message struct {
	Type string `json:"type"`
	// App is the profile ID (hello).
	App string `json:"app,omitempty"`
	// Launch asks the app to start the application when it is not running
	// (hello): set when the user opened the bundle from Finder or the Dock.
	Launch bool `json:"launch,omitempty"`
	// N is the number of live instances (count).
	N *int `json:"n,omitempty"`
}

func Hello(app string, launch bool) Message {
	return Message{Type: TypeHello, App: app, Launch: launch}
}
func Count(n int) Message { return Message{Type: TypeCount, N: &n} }

var appIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ValidApp reports whether s can be a profile ID (same syntax as profiles).
func ValidApp(s string) bool { return appIDRE.MatchString(s) }

// Validate checks a decoded message.
func (m Message) Validate() error {
	switch m.Type {
	case TypeHello:
		if !ValidApp(m.App) {
			return fmt.Errorf("hello: invalid app %q", m.App)
		}
	case TypeReopen, TypeQuitRequest, TypeExit:
	case TypeCount:
		if m.N == nil || *m.N < 0 || *m.N > 1<<20 {
			return errors.New("count: missing or invalid n")
		}
	default:
		return fmt.Errorf("unknown message type %q", m.Type)
	}
	return nil
}

// Encode returns the wire form: one JSON object and a newline.
func (m Message) Encode() []byte {
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

// Reader reads messages from a stream.
type Reader struct{ r *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{bufio.NewReaderSize(r, maxLine)} }

// Read returns the next valid message, or io.EOF when the peer closed.
func (r *Reader) Read() (Message, error) {
	line, err := r.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return Message{}, errors.New("message too long")
	}
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return Message{}, fmt.Errorf("bad message: %w", err)
	}
	return m, m.Validate()
}

// ---- paths -----------------------------------------------------------------------

// DefaultRuntimeDir is $TMPDIR/waylauncher: per user and not world-readable.
func DefaultRuntimeDir() string { return filepath.Join(os.TempDir(), "waylauncher") }

// SocketPath is the helper socket inside a runtime directory.
func SocketPath(runtimeDir string) string { return filepath.Join(runtimeDir, "helper.sock") }

// EnsureRuntimeDir creates the directory with mode 0700 and refuses one that
// is a symlink or belongs to someone else.
func EnsureRuntimeDir(dir string) error {
	if len(SocketPath(dir)) > maxSocketPath {
		return fmt.Errorf("runtime directory path is too long for a Unix socket: %s", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// ---- server (Waylauncher side) ----------------------------------------------------

// Handler receives what helpers ask for. Methods are called on server
// goroutines and must not block for long.
type Handler interface {
	// Hello decides whether to keep a helper. false answers it with exit
	// (unknown app, or the Dock setting is off). launch is true when the user
	// opened the bundle and the application should be started.
	Hello(app string, launch bool) (keep bool)
	Reopen(app string)
	QuitRequest(app string)
	// Gone is called after a helper's connection closed.
	Gone(app string)
}

type serverConn struct {
	app string
	c   net.Conn
	wmu sync.Mutex
}

func (sc *serverConn) send(m Message) error {
	sc.wmu.Lock()
	defer sc.wmu.Unlock()
	_ = sc.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := sc.c.Write(m.Encode())
	return err
}

// Server accepts helper connections: at most one per application.
type Server struct {
	ln   net.Listener
	path string
	h    Handler

	mu     sync.Mutex
	conns  map[string]*serverConn
	counts map[string]int
	closed bool
	wg     sync.WaitGroup
}

// Listen creates the socket (mode 0600) in an existing 0700 directory,
// replacing a stale socket file.
func Listen(path string, h Handler) (*Server, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("socket path too long: %s", path)
	}
	if err := EnsureRuntimeDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if c, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
		c.Close()
		return nil, fmt.Errorf("%s is already served by another process", path)
	}
	_ = os.Remove(path) // stale file from a crashed run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	s := &Server{ln: ln, path: path, h: h, conns: map[string]*serverConn{}, counts: map[string]int{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.serve(c)
	}
}

func (s *Server) serve(c net.Conn) {
	defer s.wg.Done()
	defer c.Close()
	r := NewReader(c)
	// The first message must be hello, and soon.
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	m, err := r.Read()
	if err != nil || m.Type != TypeHello {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	sc := &serverConn{app: m.App, c: c}
	if !s.h.Hello(m.App, m.Launch) {
		_ = sc.send(Message{Type: TypeExit})
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	old := s.conns[m.App]
	s.conns[m.App] = sc
	n := s.counts[m.App]
	s.mu.Unlock()
	if old != nil { // one helper per application: the newer one wins
		_ = old.send(Message{Type: TypeExit})
		old.c.Close()
	}
	_ = sc.send(Count(n))

	for {
		m, err := r.Read()
		if err != nil {
			break
		}
		switch m.Type {
		case TypeReopen:
			s.h.Reopen(sc.app)
		case TypeQuitRequest:
			s.h.QuitRequest(sc.app)
		}
	}
	s.mu.Lock()
	current := s.conns[sc.app] == sc
	if current {
		delete(s.conns, sc.app)
	}
	s.mu.Unlock()
	if current {
		s.h.Gone(sc.app)
	}
}

// SetCount records the live-instance count of an application and sends it to
// its helper when it changed.
func (s *Server) SetCount(app string, n int) {
	s.mu.Lock()
	prev, had := s.counts[app]
	s.counts[app] = n
	sc := s.conns[app]
	s.mu.Unlock()
	if sc != nil && (!had || prev != n) {
		_ = sc.send(Count(n))
	}
}

// Connected reports whether a helper is bound for the application.
func (s *Server) Connected(app string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[app] != nil
}

// Apps lists applications with a connected helper.
func (s *Server) Apps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.conns))
	for a := range s.conns {
		out = append(out, a)
	}
	return out
}

// Exit tells one application's helper to leave.
func (s *Server) Exit(app string) {
	s.mu.Lock()
	sc := s.conns[app]
	s.mu.Unlock()
	if sc != nil {
		_ = sc.send(Message{Type: TypeExit})
	}
}

// ExitAll tells every helper to leave.
func (s *Server) ExitAll() {
	for _, a := range s.Apps() {
		s.Exit(a)
	}
}

// Close tells helpers to exit, stops accepting and removes the socket.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := make([]*serverConn, 0, len(s.conns))
	for _, sc := range s.conns {
		conns = append(conns, sc)
	}
	s.mu.Unlock()
	for _, sc := range conns {
		_ = sc.send(Message{Type: TypeExit})
	}
	s.ln.Close()
	_ = os.Remove(s.path)
	for _, sc := range conns {
		sc.c.Close()
	}
	s.wg.Wait()
}

// ---- client (helper side) ---------------------------------------------------------

type Client struct {
	c   net.Conn
	r   *Reader
	wmu sync.Mutex
}

// Dial connects to Waylauncher's helper socket.
func Dial(path string, timeout time.Duration) (*Client, error) {
	c, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return nil, err
	}
	return &Client{c: c, r: NewReader(c)}, nil
}

func (c *Client) Send(m Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := c.c.Write(m.Encode())
	return err
}

// Recv blocks for the next message; io.EOF means Waylauncher went away.
func (c *Client) Recv() (Message, error) { return c.r.Read() }

func (c *Client) Close() error { return c.c.Close() }
