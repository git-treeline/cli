package tunneldaemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/tunnel"
)

// IdleShutdown is how long the daemon waits with zero registrations before
// stopping cloudflared and exiting. Short enough that an unused daemon goes
// away quickly; long enough that fast unregister/re-register cycles don't
// cause flapping.
var IdleShutdown = 5 * time.Second

// SocketPath returns the per-user, per-tunnel-config Unix socket path used
// to talk to the daemon. The socket lives in cloudflared's config dir
// (typically ~/.cloudflared, mode 0700) so any local process needs the
// user's permissions to connect. The hash keeps the leaf short to stay
// within macOS's ~104-byte socket name limit.
func SocketPath(tunnelName string) string {
	h := sha256.Sum256([]byte(tunnelName))
	return filepath.Join(tunnel.ConfigDir(), fmt.Sprintf("gtl-tunnel-%x.sock", h[:8]))
}

// EnsureSocketDir creates the parent directory for SocketPath with
// user-only permissions (0700). Callers should invoke this before
// Listen so an attacker can't pre-create a world-accessible parent.
func EnsureSocketDir() error {
	dir := tunnel.ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// If the dir already existed with looser permissions, tighten them.
	return os.Chmod(dir, 0o700)
}

// Runner abstracts cloudflared execution so tests can inject a fake.
type Runner interface {
	Start(ctx context.Context, tunnelName, configPath string) (CloudflaredHandle, error)
}

// CloudflaredHandle controls a running cloudflared instance.
type CloudflaredHandle interface {
	Stderr() io.Reader
	Stop(timeout time.Duration) error
	Wait() error
}

// Daemon owns one cloudflared process and tracks the set of active
// registrations keyed by client connection.
type Daemon struct {
	TunnelName string
	Runner     Runner

	// WriteConfig writes the cloudflared config for the given routes and
	// returns its on-disk path. Defaults to tunnel.WriteMultiHostConfig.
	WriteConfig func(tunnelName string, routes []tunnel.HostRoute) (string, error)

	// LogSink receives daemon's own log lines (defaults to stderr).
	LogSink io.Writer

	mu      sync.Mutex
	applyMu sync.Mutex // serializes config writes + cloudflared (re)starts
	clients map[*client]struct{}
	session *cloudflaredSession

	idleTimer *time.Timer
	done      chan struct{}
	doneOnce  sync.Once
}

// cloudflaredSession bundles a running cloudflared with the metadata we
// need to tell intentional stops apart from unexpected exits.
type cloudflaredSession struct {
	handle   CloudflaredHandle
	done     chan struct{} // closed when handle.Wait returns
	expected atomic.Bool   // set before any clean stop
}

type client struct {
	conn     net.Conn
	enc      *json.Encoder
	encMu    sync.Mutex
	hostname string
	port     int
}

// New returns a daemon ready to serve. Call Run with a listening socket.
func New(tunnelName string) *Daemon {
	return &Daemon{
		TunnelName:  tunnelName,
		Runner:      cloudflaredRunner{},
		WriteConfig: tunnel.WriteMultiHostConfig,
		LogSink:     os.Stderr,
		clients:     make(map[*client]struct{}),
		done:        make(chan struct{}),
	}
}

// Run accepts connections from the given listener until the daemon's idle
// timer fires (after the last client disconnects) or ctx is cancelled.
func (d *Daemon) Run(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	d.armIdleTimer()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.done:
				return d.shutdown()
			case <-ctx.Done():
				return d.shutdown()
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return d.shutdown()
			}
			d.logf("accept: %v", err)
			continue
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	c := &client{
		conn: conn,
		enc:  json.NewEncoder(conn),
	}
	defer d.dropClient(c)

	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}

	var reg Register
	if err := json.Unmarshal(line, &reg); err != nil || reg.Op != OpRegister {
		c.send(Event{Kind: EventError, Error: "invalid register message"})
		_ = conn.Close()
		return
	}
	if reg.Hostname == "" || reg.Port <= 0 || reg.Port > 65535 {
		c.send(Event{Kind: EventError, Error: "hostname and valid port required"})
		_ = conn.Close()
		return
	}
	c.hostname = reg.Hostname
	c.port = reg.Port

	if err := d.addClient(c); err != nil {
		c.send(Event{Kind: EventError, Error: err.Error()})
		_ = conn.Close()
		return
	}
	c.send(Event{Kind: EventRegistered, Hostname: c.hostname, Port: c.port})

	// Block until the client closes the connection; that's how we detect
	// "unregister" (Ctrl+C on the client side).
	buf := make([]byte, 1)
	for {
		if _, err := r.Read(buf); err != nil {
			return
		}
	}
}

// addClient registers a new client, rewrites the config, and starts or
// restarts cloudflared so its ingress includes the new hostname.
func (d *Daemon) addClient(c *client) error {
	d.mu.Lock()

	// Reject duplicate hostname registrations (single repo running gtl tunnel
	// twice would collide on subdomain anyway). Different port for same
	// hostname is also rejected — caller should restart cleanly.
	for existing := range d.clients {
		if existing.hostname == c.hostname {
			d.mu.Unlock()
			return fmt.Errorf("hostname %s is already registered (port %d)", c.hostname, existing.port)
		}
	}

	d.clients[c] = struct{}{}
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
	routes := d.routesLocked()
	d.mu.Unlock()

	return d.applyRoutes(routes)
}

func (d *Daemon) dropClient(c *client) {
	d.mu.Lock()
	if _, ok := d.clients[c]; !ok {
		d.mu.Unlock()
		_ = c.conn.Close()
		return
	}
	delete(d.clients, c)
	routes := d.routesLocked()
	empty := len(d.clients) == 0
	d.mu.Unlock()

	_ = c.conn.Close()

	if empty {
		d.armIdleTimer()
		// Stop cloudflared eagerly so we don't keep an idle tunnel up.
		d.stopCloudflared()
		return
	}

	if err := d.applyRoutes(routes); err != nil {
		d.logf("apply routes after drop: %v", err)
	}
}

func (d *Daemon) routesLocked() []tunnel.HostRoute {
	routes := make([]tunnel.HostRoute, 0, len(d.clients))
	for c := range d.clients {
		routes = append(routes, tunnel.HostRoute{Hostname: c.hostname, Port: c.port})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Hostname < routes[j].Hostname })
	return routes
}

// applyRoutes writes a fresh cloudflared config and (re)starts cloudflared
// so the new ingress takes effect. Restart-on-change is the explicit
// tradeoff: SIGHUP-based reload isn't reliable across cloudflared versions,
// so adding or removing one branch briefly interrupts the others sharing
// this daemon. applyMu serializes concurrent callers.
func (d *Daemon) applyRoutes(routes []tunnel.HostRoute) error {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()

	if len(routes) == 0 {
		d.stopCloudflaredLocked()
		return nil
	}

	configPath, err := d.WriteConfig(d.TunnelName, routes)
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	d.stopCloudflaredLocked()

	handle, err := d.Runner.Start(context.Background(), d.TunnelName, configPath)
	if err != nil {
		return fmt.Errorf("start cloudflared: %w", err)
	}
	sess := &cloudflaredSession{handle: handle, done: make(chan struct{})}
	d.mu.Lock()
	d.session = sess
	d.mu.Unlock()

	go d.pumpStderr(handle.Stderr())
	go d.watchSession(sess)
	return nil
}

// watchSession waits for cloudflared to exit. If the session is still the
// active one and wasn't marked as an expected stop, that's an unexpected
// exit (auth failure, crash, network blip) — notify clients and drop their
// connections so the CLI returns instead of pretending the tunnel is live.
func (d *Daemon) watchSession(sess *cloudflaredSession) {
	_ = sess.handle.Wait()
	close(sess.done)
	if sess.expected.Load() {
		return
	}
	d.mu.Lock()
	current := d.session == sess
	if current {
		d.session = nil
	}
	d.mu.Unlock()
	if !current {
		return
	}
	d.handleUnexpectedExit()
}

func (d *Daemon) handleUnexpectedExit() {
	d.broadcast(Event{Kind: EventTunnelDown, Error: "cloudflared exited unexpectedly"})

	d.mu.Lock()
	for c := range d.clients {
		_ = c.conn.Close()
	}
	d.clients = map[*client]struct{}{}
	d.mu.Unlock()

	d.armIdleTimer()
}

func (d *Daemon) stopCloudflared() {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	d.stopCloudflaredLocked()
}

// stopCloudflaredLocked stops the running cloudflared, if any. Caller must
// hold d.applyMu so we don't race with a concurrent start. The session's
// expected flag is set before Stop so the watcher knows this isn't a crash.
func (d *Daemon) stopCloudflaredLocked() {
	d.mu.Lock()
	sess := d.session
	d.session = nil
	d.mu.Unlock()
	if sess == nil {
		return
	}
	sess.expected.Store(true)
	_ = sess.handle.Stop(5 * time.Second)
	<-sess.done
}

// pumpStderr fans out cloudflared stderr lines to connected clients,
// filtering by relevance. Errors/warnings broadcast to everyone; request
// logs route only to the client whose hostname appears in the line; INF
// spam is dropped.
func (d *Daemon) pumpStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		d.fanout(line)
	}
}

var requestMethodRe = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)

func (d *Daemon) fanout(line string) {
	switch classifyLine(line) {
	case lineError:
		d.broadcast(Event{Kind: EventLog, Stream: StreamStderr, Line: line})
	case lineRequest:
		d.routeRequestLog(line)
	case lineRegistered:
		d.broadcast(Event{Kind: EventTunnelUp, Line: line})
	default:
		// dropped
	}
}

type lineClass int

const (
	lineDrop lineClass = iota
	lineError
	lineRequest
	lineRegistered
)

func classifyLine(line string) lineClass {
	switch {
	case strings.Contains(line, "ERR"),
		strings.Contains(line, "WRN"),
		strings.Contains(line, "failed"),
		strings.Contains(line, "error"):
		return lineError
	case requestMethodRe.MatchString(line):
		return lineRequest
	case strings.Contains(line, "INF") && strings.Contains(line, "Registered"):
		return lineRegistered
	}
	return lineDrop
}

func (d *Daemon) routeRequestLog(line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.clients {
		if strings.Contains(line, c.hostname) {
			c.send(Event{Kind: EventLog, Stream: StreamStdout, Line: line})
			return
		}
	}
	// No matching client — broadcast as fallback so a stray log isn't lost.
	for c := range d.clients {
		c.send(Event{Kind: EventLog, Stream: StreamStdout, Line: line})
	}
}

func (d *Daemon) broadcast(ev Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.clients {
		c.send(ev)
	}
}

func (c *client) send(ev Event) {
	c.encMu.Lock()
	defer c.encMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = c.enc.Encode(ev)
	_ = c.conn.SetWriteDeadline(time.Time{})
}

func (d *Daemon) armIdleTimer() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.clients) != 0 {
		return
	}
	if d.idleTimer != nil {
		d.idleTimer.Stop()
	}
	d.idleTimer = time.AfterFunc(IdleShutdown, func() {
		d.mu.Lock()
		stillEmpty := len(d.clients) == 0
		d.mu.Unlock()
		if stillEmpty {
			d.doneOnce.Do(func() { close(d.done) })
		}
	})
}

func (d *Daemon) shutdown() error {
	d.stopCloudflared()
	d.mu.Lock()
	for c := range d.clients {
		_ = c.conn.Close()
	}
	d.clients = map[*client]struct{}{}
	d.mu.Unlock()
	return nil
}

// Done returns a channel closed when the daemon decides to exit (idle
// shutdown or listener closed by ctx cancel).
func (d *Daemon) Done() <-chan struct{} { return d.done }

func (d *Daemon) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(d.LogSink, "tunneldaemon: "+format+"\n", args...)
}

// --- real cloudflared runner ---

type cloudflaredRunner struct{}

func (cloudflaredRunner) Start(ctx context.Context, tunnelName, configPath string) (CloudflaredHandle, error) {
	cfPath, err := tunnel.ResolveCloudflared()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, cfPath, "tunnel", "--config", configPath, "run", tunnelName)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &realHandle{cmd: cmd, stderr: stderr}, nil
}

type realHandle struct {
	cmd     *exec.Cmd
	stderr  io.ReadCloser
	once    sync.Once
	done    chan struct{} // closed when cmd.Wait returns
	waitErr error         // populated before done is closed
}

func (h *realHandle) Stderr() io.Reader { return h.stderr }

func (h *realHandle) Stop(timeout time.Duration) error {
	h.ensureWaiter()
	if h.cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-h.done:
		return nil
	case <-time.After(timeout):
		_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL)
		<-h.done
		return nil
	}
}

func (h *realHandle) Wait() error {
	h.ensureWaiter()
	<-h.done
	return h.waitErr
}

func (h *realHandle) ensureWaiter() {
	h.once.Do(func() {
		h.done = make(chan struct{})
		go func() {
			h.waitErr = h.cmd.Wait()
			close(h.done)
		}()
	})
}

