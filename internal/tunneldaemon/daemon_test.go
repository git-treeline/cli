package tunneldaemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/tunnel"
)

// --- fake runner -------------------------------------------------------------

// fakeRunner records every Start call and returns a controllable handle.
// The handle's stderr can be fed synthetic cloudflared log lines via Feed.
type fakeRunner struct {
	mu      sync.Mutex
	starts  []startCall
	current *fakeHandle
	startCh chan struct{} // closed each time a new handle is created
}

type startCall struct {
	TunnelName string
	ConfigPath string
	Config     string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{startCh: make(chan struct{}, 8)}
}

func (r *fakeRunner) Start(_ context.Context, tunnelName, configPath string) (CloudflaredHandle, error) {
	data, _ := os.ReadFile(configPath)
	h := &fakeHandle{
		stderr: newPipeReader(),
		done:   make(chan struct{}),
	}
	r.mu.Lock()
	r.starts = append(r.starts, startCall{TunnelName: tunnelName, ConfigPath: configPath, Config: string(data)})
	r.current = h
	r.mu.Unlock()
	select {
	case r.startCh <- struct{}{}:
	default:
	}
	return h, nil
}

func (r *fakeRunner) Current() *fakeHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

func (r *fakeRunner) StartCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.starts)
}

func (r *fakeRunner) LastConfig() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.starts) == 0 {
		return ""
	}
	return r.starts[len(r.starts)-1].Config
}

type fakeHandle struct {
	stderr  *pipeReader
	done    chan struct{}
	stopped atomic.Bool
}

func (h *fakeHandle) Stderr() io.Reader { return h.stderr }

func (h *fakeHandle) Stop(_ time.Duration) error {
	if h.stopped.CompareAndSwap(false, true) {
		_ = h.stderr.Close()
		close(h.done)
	}
	return nil
}

func (h *fakeHandle) Wait() error {
	<-h.done
	return nil
}

func (h *fakeHandle) Feed(line string) {
	_, _ = h.stderr.Write([]byte(line + "\n"))
}

// pipeReader is a goroutine-safe in-memory pipe that survives multiple
// writes and a close.
type pipeReader struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	cond   *sync.Cond
	closed bool
}

func newPipeReader() *pipeReader {
	p := &pipeReader{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pipeReader) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, err := p.buf.Write(b)
	p.cond.Broadcast()
	return n, err
}

func (p *pipeReader) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.buf.Len() == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.buf.Len() == 0 && p.closed {
		return 0, io.EOF
	}
	return p.buf.Read(b)
}

func (p *pipeReader) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}

// --- test helpers ------------------------------------------------------------

// startDaemon spins up a daemon listening on a temp Unix socket and returns
// the daemon, the runner, and a cleanup function.
func startDaemon(t *testing.T) (*Daemon, *fakeRunner, string, func()) {
	t.Helper()

	// macOS caps Unix socket paths at ~104 bytes; t.TempDir() under
	// /var/folders/... already eats most of that, so put the socket
	// directly under /tmp with a per-test random suffix.
	f, err := os.CreateTemp("/tmp", "gtl-tunneldaemon-test-*.sock")
	if err != nil {
		t.Fatalf("temp socket: %v", err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	cfgDir := t.TempDir()
	runner := newFakeRunner()

	d := New("test-tunnel")
	d.Runner = runner
	d.LogSink = io.Discard
	d.WriteConfig = func(name string, routes []tunnel.HostRoute) (string, error) {
		path := filepath.Join(cfgDir, "config.yml")
		return path, os.WriteFile(path, []byte(tunnel.GenerateMultiHostConfig(name, "/tmp/cred.json", routes)), 0o600)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneRun := make(chan error, 1)
	go func() { doneRun <- d.Run(ctx, ln) }()

	cleanup := func() {
		cancel()
		_ = ln.Close()
		select {
		case <-doneRun:
		case <-time.After(2 * time.Second):
			t.Logf("daemon did not exit in 2s")
		}
	}

	return d, runner, sock, cleanup
}

// dialAndRegister dials the socket, sends a register message, and returns
// the connection and a decoder positioned past the "registered" event.
func dialAndRegister(t *testing.T, sock, hostname string, port int) (net.Conn, *json.Decoder) {
	t.Helper()
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	b, _ := json.Marshal(Register{Op: OpRegister, Hostname: hostname, Port: port})
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatalf("write register: %v", err)
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	var ev Event
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("decode registered: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if ev.Kind != EventRegistered {
		t.Fatalf("expected registered, got %+v", ev)
	}
	return conn, dec
}

func waitFor(t *testing.T, name string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", name)
}

// --- tests -------------------------------------------------------------------

func TestSocketPath_ShortAndDeterministic(t *testing.T) {
	p1 := SocketPath("gtl")
	p2 := SocketPath("gtl")
	if p1 != p2 {
		t.Errorf("expected deterministic path, got %q vs %q", p1, p2)
	}
	if len(p1) > 90 {
		t.Errorf("socket path too long for macOS (%d > 90): %s", len(p1), p1)
	}
	if SocketPath("gtl-work") == SocketPath("gtl") {
		t.Error("expected different paths for different tunnel names")
	}
}

func TestSocketPath_UnderUserConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := SocketPath("gtl")
	wantDir := filepath.Join(home, ".cloudflared")
	if filepath.Dir(got) != wantDir {
		t.Errorf("expected socket under %s, got parent %s (full: %s)", wantDir, filepath.Dir(got), got)
	}
}

func TestEnsureSocketDir_CreatesWith0700(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := EnsureSocketDir(); err != nil {
		t.Fatalf("EnsureSocketDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, ".cloudflared"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("expected mode 0700, got %o", mode)
	}
}

func TestEnsureSocketDir_TightensExistingMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfDir := filepath.Join(dir, ".cloudflared")
	if err := os.MkdirAll(cfDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSocketDir(); err != nil {
		t.Fatalf("EnsureSocketDir: %v", err)
	}

	info, _ := os.Stat(cfDir)
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("expected tightened to 0700, got %o", mode)
	}
}

func TestDaemon_RegisterStartsCloudflared(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	conn, _ := dialAndRegister(t, sock, "a.example.dev", 3050)
	defer func() { _ = conn.Close() }()

	waitFor(t, "cloudflared start", func() bool { return runner.StartCount() >= 1 })

	got := runner.LastConfig()
	if !contains(got, `hostname: "a.example.dev"`) {
		t.Errorf("config missing first hostname:\n%s", got)
	}
	if !contains(got, "http://localhost:3050") {
		t.Errorf("config missing first port:\n%s", got)
	}
}

func TestDaemon_TwoClientsBothInIngress(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	c1, _ := dialAndRegister(t, sock, "a.example.dev", 3050)
	defer func() { _ = c1.Close() }()
	waitFor(t, "first start", func() bool { return runner.StartCount() >= 1 })

	c2, _ := dialAndRegister(t, sock, "b.example.dev", 3060)
	defer func() { _ = c2.Close() }()
	waitFor(t, "restart for second client", func() bool { return runner.StartCount() >= 2 })

	got := runner.LastConfig()
	if !contains(got, `hostname: "a.example.dev"`) || !contains(got, "http://localhost:3050") {
		t.Errorf("config missing first route:\n%s", got)
	}
	if !contains(got, `hostname: "b.example.dev"`) || !contains(got, "http://localhost:3060") {
		t.Errorf("config missing second route:\n%s", got)
	}
}

func TestDaemon_DuplicateHostnameRejected(t *testing.T) {
	_, _, sock, cleanup := startDaemon(t)
	defer cleanup()

	c1, _ := dialAndRegister(t, sock, "dup.example.dev", 3050)
	defer func() { _ = c1.Close() }()

	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	b, _ := json.Marshal(Register{Op: OpRegister, Hostname: "dup.example.dev", Port: 3060})
	_, _ = conn.Write(append(b, '\n'))
	dec := json.NewDecoder(bufio.NewReader(conn))
	var ev Event
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := dec.Decode(&ev); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != EventError {
		t.Errorf("expected error event, got %+v", ev)
	}
}

func TestDaemon_DisconnectRegeneratesConfig(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	c1, _ := dialAndRegister(t, sock, "a.example.dev", 3050)
	c2, _ := dialAndRegister(t, sock, "b.example.dev", 3060)
	defer func() { _ = c2.Close() }()
	waitFor(t, "two starts", func() bool { return runner.StartCount() >= 2 })

	_ = c1.Close()

	// Eventually the daemon should restart cloudflared with only b.example.dev.
	waitFor(t, "config rewritten without a", func() bool {
		cfg := runner.LastConfig()
		return contains(cfg, `hostname: "b.example.dev"`) && !contains(cfg, `hostname: "a.example.dev"`)
	})
}

func TestDaemon_LastDisconnectStopsCloudflaredAndExits(t *testing.T) {
	prev := IdleShutdown
	IdleShutdown = 100 * time.Millisecond
	defer func() { IdleShutdown = prev }()

	d, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	c1, _ := dialAndRegister(t, sock, "a.example.dev", 3050)
	waitFor(t, "started", func() bool { return runner.StartCount() >= 1 })
	h := runner.Current()

	_ = c1.Close()

	waitFor(t, "cloudflared stopped", func() bool { return h.stopped.Load() })

	select {
	case <-d.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not signal Done after idle timeout")
	}
}

func TestDaemon_BroadcastsErrorLine(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	conn, dec := dialAndRegister(t, sock, "a.example.dev", 3050)
	defer func() { _ = conn.Close() }()
	waitFor(t, "started", func() bool { return runner.StartCount() >= 1 })

	runner.Current().Feed("2024 ERR cloudflared failed to authenticate")

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ev Event
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.Kind != EventLog || ev.Stream != StreamStderr {
		t.Errorf("expected stderr log event, got %+v", ev)
	}
	if !contains(ev.Line, "ERR") {
		t.Errorf("expected ERR in line, got %q", ev.Line)
	}
}

// TestDaemon_UnexpectedCloudflaredExitNotifiesClients verifies that when
// cloudflared dies on its own (auth fail, crash, network), the daemon
// broadcasts EventTunnelDown and drops all client connections so the CLI
// can return instead of pretending the tunnel is live.
func TestDaemon_UnexpectedCloudflaredExitNotifiesClients(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	conn, dec := dialAndRegister(t, sock, "a.example.dev", 3050)
	defer func() { _ = conn.Close() }()
	waitFor(t, "started", func() bool { return runner.StartCount() >= 1 })

	// Simulate cloudflared crashing on its own.
	_ = runner.Current().Stop(0)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	sawDown := false
	for !sawDown {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if ev.Kind == EventTunnelDown {
			sawDown = true
		}
	}
	if !sawDown {
		t.Fatal("client did not receive EventTunnelDown after cloudflared exit")
	}

	// Subsequent reads should EOF (connection closed by daemon).
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var ev Event
	if err := dec.Decode(&ev); err == nil {
		t.Errorf("expected connection closed, got further event: %+v", ev)
	}
}

// TestDaemon_ExpectedStopDoesNotBroadcastDown verifies that the routine
// restart path (registration change) does NOT emit EventTunnelDown.
func TestDaemon_ExpectedStopDoesNotBroadcastDown(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	cA, decA := dialAndRegister(t, sock, "a.example.dev", 3050)
	defer func() { _ = cA.Close() }()
	waitFor(t, "started", func() bool { return runner.StartCount() >= 1 })

	cB, _ := dialAndRegister(t, sock, "b.example.dev", 3060)
	defer func() { _ = cB.Close() }()
	waitFor(t, "restart for B", func() bool { return runner.StartCount() >= 2 })

	// Client A must NOT see an EventTunnelDown from the routine restart.
	_ = cA.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		var ev Event
		if err := decA.Decode(&ev); err != nil {
			break
		}
		if ev.Kind == EventTunnelDown {
			t.Fatalf("unexpected EventTunnelDown during routine restart: %+v", ev)
		}
	}
}

func TestDaemon_RequestLogRoutedByHostname(t *testing.T) {
	_, runner, sock, cleanup := startDaemon(t)
	defer cleanup()

	cA, decA := dialAndRegister(t, sock, "a.example.dev", 3050)
	defer func() { _ = cA.Close() }()
	cB, decB := dialAndRegister(t, sock, "b.example.dev", 3060)
	defer func() { _ = cB.Close() }()
	waitFor(t, "second start", func() bool { return runner.StartCount() >= 2 })

	runner.Current().Feed("GET https://a.example.dev/health 200 5ms")

	// A should receive the log; B should not (timeout).
	_ = cA.SetReadDeadline(time.Now().Add(1 * time.Second))
	var ev Event
	if err := decA.Decode(&ev); err != nil {
		t.Fatalf("A decode: %v", err)
	}
	if !contains(ev.Line, "a.example.dev") {
		t.Errorf("A got wrong line: %q", ev.Line)
	}

	_ = cB.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if err := decB.Decode(&ev); err == nil {
		t.Errorf("B should not have received the log, got %+v", ev)
	} else if !isTimeout(err) && !errors.Is(err, io.EOF) {
		t.Errorf("B unexpected error: %v", err)
	}
}

// --- utilities --------------------------------------------------------------

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}
