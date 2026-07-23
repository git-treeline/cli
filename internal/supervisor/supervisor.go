// Package supervisor provides a lightweight process wrapper that runs a
// command in the foreground while accepting restart/stop signals over a
// Unix socket. The user sees all output in their terminal; external
// callers (agents, MCP) control the lifecycle via the socket.
package supervisor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// errStopInProgress is returned by startChildLocked when a stop is mid-flight
// (s.stopping true, s.mu released to wait for the old child). The socket "start"
// handler surfaces this as a retry hint so the caller knows to try again rather
// than believing an "ok" that started nothing.
var errStopInProgress = errors.New("stop in progress")

// SocketPath returns a short, deterministic socket path under /tmp to avoid
// the ~104 byte macOS limit on Unix socket paths. The hash ensures uniqueness
// per worktree without depending on path length.
func SocketPath(worktreePath string) string {
	h := sha256.Sum256([]byte(worktreePath))
	return fmt.Sprintf("/tmp/gtl-%x.sock", h[:8])
}

// PidPath returns the supervisor PID file path corresponding to a socket path.
func PidPath(socketPath string) string {
	return strings.TrimSuffix(socketPath, ".sock") + ".pid"
}

// ChildPidPath returns the sidecar file path that records the child's process
// group id (pgid) corresponding to a socket path. It sits next to PidPath so a
// force-kill can reap the whole child process group even after every in-process
// handle is gone.
func ChildPidPath(socketPath string) string {
	return strings.TrimSuffix(socketPath, ".sock") + ".child.pid"
}

type Supervisor struct {
	Command    string
	Dir        string
	SocketPath string
	Port       int
	Env        map[string]string // extra env vars injected into the child process
	Log        func(format string, args ...any)
	// ConnWriteDeadline caps how long handleConn waits to write a response.
	// Defaults to 15s. Override in tests to avoid slow-test hangs.
	ConnWriteDeadline time.Duration

	mu           sync.Mutex
	child        *exec.Cmd
	childDone    chan struct{} // closed when current child's Wait() completes
	stopping     bool          // true while stopChildLocked has released s.mu to wait
	listener     net.Listener
	done         chan struct{}
	shutdownOnce sync.Once
}

func New(command, dir, socketPath string) *Supervisor {
	return &Supervisor{
		Command:           command,
		Dir:               dir,
		SocketPath:        socketPath,
		Log:               func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
		ConnWriteDeadline: 15 * time.Second,
		done:              make(chan struct{}),
	}
}

func (s *Supervisor) Run() error {
	if _, err := os.Stat(s.SocketPath); err == nil {
		if resp, dialErr := Send(s.SocketPath, "status"); dialErr == nil {
			return fmt.Errorf("supervisor already running (status: %s) on %s", resp, s.SocketPath)
		}
	}
	_ = os.Remove(s.SocketPath)

	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return fmt.Errorf("listening on socket: %w", err)
	}
	_ = os.Chmod(s.SocketPath, 0600)
	s.listener = ln

	pidPath := PidPath(s.SocketPath)
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600)
	defer func() {
		_ = ln.Close()
		_ = os.Remove(s.SocketPath)
		_ = os.Remove(pidPath)
		_ = os.Remove(ChildPidPath(s.SocketPath))
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go s.acceptLoop()

	if err := s.startChild(); err != nil {
		return err
	}

	for {
		select {
		case <-s.done:
			return nil
		case sig := <-sigs:
			s.Log("\n==> Received %s, shutting down...", sig)
			s.stopChild()
			return nil
		}
	}
}

// startChildLocked starts the child process. Caller must hold s.mu.
func (s *Supervisor) startChildLocked() error {
	// A stop is in progress and has released s.mu to wait for the old child to
	// exit. Spawning here would race the restart's own start and leave an
	// untracked child fighting for the port. Skip — restart starts the fresh
	// child itself once the stop completes.
	if s.stopping {
		s.Log("==> Ignoring start: a stop is in progress")
		return errStopInProgress
	}

	s.Log("==> Starting: %s", s.Command)
	cmd := exec.Command("sh", "-c", s.Command)
	cmd.Dir = s.Dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if len(s.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range s.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting command: %w", err)
	}
	s.child = cmd
	done := make(chan struct{})
	s.childDone = done

	// Persist the child's pgid (== child pid because Setpgid gives it a fresh
	// group) so a force-kill can reap the whole group even if this process is
	// gone. Removed when the child exits, on stop, and on supervisor shutdown.
	_ = os.WriteFile(ChildPidPath(s.SocketPath), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)

	go func() {
		_ = cmd.Wait()
		close(done)
		s.mu.Lock()
		if s.child == cmd {
			s.child = nil
			_ = os.Remove(ChildPidPath(s.SocketPath))
		}
		s.mu.Unlock()
	}()

	return nil
}

func (s *Supervisor) startChild() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startChildLocked()
}

// stopChildLocked sends SIGTERM to the child process group and waits.
// Caller must hold s.mu; the lock is released during the wait to avoid
// blocking status queries.
func (s *Supervisor) stopChildLocked() {
	child := s.child
	waitCh := s.childDone
	if child == nil || child.Process == nil {
		return
	}
	s.child = nil
	s.childDone = nil
	s.stopping = true
	s.mu.Unlock()

	_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)

	exited := false
	select {
	case <-waitCh:
		exited = true
	case <-time.After(10 * time.Second):
		s.Log("==> Process didn't exit in 10s, sending SIGKILL")
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		select {
		case <-waitCh:
			exited = true
		case <-time.After(5 * time.Second):
			s.Log("==> Process did not exit after SIGKILL — proceeding")
		}
	}
	// The wait goroutine only removes the sidecar on a self-exit (s.child ==
	// cmd); on this path s.child is already nil, so the removal is ours. Skip
	// it if the child never exited — the pgid is still needed for a later
	// force-kill.
	if exited {
		_ = os.Remove(ChildPidPath(s.SocketPath))
	}

	s.mu.Lock()
	s.stopping = false
}

func (s *Supervisor) stopChild() {
	s.mu.Lock()
	s.stopChildLocked()
	s.mu.Unlock()
}

// restart atomically stops the current child and starts a new one.
// Holds the lock for the entire sequence to prevent concurrent restarts
// from spawning duplicate processes.
func (s *Supervisor) restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Log("\n==> Restarting server...")
	s.stopChildLocked()
	return s.startChildLocked()
}

func (s *Supervisor) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Supervisor) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	// Read the whole request until the client half-closes its write side (EOF).
	// A fixed-size Read truncated large payloads (e.g. an update-env >4096B cut a
	// value mid-pair). io.ReadAll returns whatever it buffered even when the read
	// deadline fires, which is the compat backstop for OLD clients that never
	// CloseWrite: they write once and never signal EOF, so ReadAll blocks until
	// the 5s deadline, then hands back the same bytes today's single read saw —
	// correct, just ~5s later. New clients CloseWrite and return immediately.
	rawBytes, _ := io.ReadAll(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if len(rawBytes) == 0 {
		return
	}

	raw := strings.TrimSpace(string(rawBytes))
	parts := strings.SplitN(raw, ":", 2)
	cmd := parts[0]

	// wait-ready manages its own deadline via the timeout embedded in the command.
	// All other commands must respond quickly — cap the write side to prevent a
	// hung goroutine from holding s.mu indefinitely if the client disappears.
	if cmd != "wait-ready" {
		_ = conn.SetWriteDeadline(time.Now().Add(s.ConnWriteDeadline))
	}

	switch cmd {
	case "restart":
		if err := s.restart(); err != nil {
			_, _ = fmt.Fprintf(conn, "error: %s", err)
			return
		}
		_, _ = fmt.Fprint(conn, "ok")
	case "start":
		s.mu.Lock()
		if s.child != nil {
			s.mu.Unlock()
			_, _ = fmt.Fprint(conn, "already running")
			return
		}
		err := s.startChildLocked()
		s.mu.Unlock()
		if err != nil {
			// A stop released s.mu to wait and set s.child=nil, so this start slid
			// in but startChildLocked refused to spawn. Tell the client to retry
			// instead of replying "ok" for a child that was never started.
			if errors.Is(err, errStopInProgress) {
				_, _ = fmt.Fprint(conn, "error: stop in progress — retry in a moment")
				return
			}
			_, _ = fmt.Fprintf(conn, "error: %s", err)
			return
		}
		_, _ = fmt.Fprint(conn, "ok")
	case "stop":
		s.Log("\n==> Server stopped. Supervisor waiting...")
		s.stopChild()
		_, _ = fmt.Fprint(conn, "ok")
	case "shutdown":
		s.Log("\n==> Shutting down supervisor...")
		s.stopChild()
		_, _ = fmt.Fprint(conn, "ok")
		s.shutdownOnce.Do(func() { close(s.done) })
	case "status":
		s.mu.Lock()
		running := s.child != nil && s.child.Process != nil
		s.mu.Unlock()
		if running {
			_, _ = fmt.Fprint(conn, "running")
		} else {
			_, _ = fmt.Fprint(conn, "stopped")
		}
	case "get-command":
		// s.Command is set at construction and never mutated — no lock needed.
		_, _ = fmt.Fprint(conn, s.Command)
	case "update-env":
		if len(parts) < 2 || parts[1] == "" {
			_, _ = fmt.Fprint(conn, "ok")
			return
		}
		s.mu.Lock()
		if s.Env == nil {
			s.Env = make(map[string]string)
		}
		for _, pair := range strings.Split(parts[1], "\x00") {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				s.Env[kv[0]] = kv[1]
			}
		}
		s.mu.Unlock()
		_, _ = fmt.Fprint(conn, "ok")
	case "wait-ready":
		timeout := 60 * time.Second
		if len(parts) > 1 {
			if secs, err := strconv.Atoi(parts[1]); err == nil && secs > 0 {
				timeout = time.Duration(secs) * time.Second
			}
		}
		s.handleWaitReady(conn, timeout)
	default:
		_, _ = fmt.Fprintf(conn, "unknown command: %s", raw)
	}
}

func (s *Supervisor) handleWaitReady(conn net.Conn, timeout time.Duration) {
	if s.Port == 0 {
		_, _ = fmt.Fprint(conn, "error: no port configured")
		return
	}

	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			_, _ = fmt.Fprint(conn, "ok")
			return
		}

		s.mu.Lock()
		childDone := s.childDone
		s.mu.Unlock()

		if childDone == nil {
			_, _ = fmt.Fprint(conn, "error: server not running")
			return
		}

		select {
		case <-childDone:
			_, _ = fmt.Fprint(conn, "error: server exited before becoming ready")
			return
		case <-ticker.C:
		}

		if time.Now().After(deadline) {
			_, _ = fmt.Fprint(conn, "error: timeout waiting for port")
			return
		}
	}
}

// Send connects to a supervisor socket and sends a command.
// Returns the response string. Uses a 30-second deadline.
func Send(socketPath, command string) (string, error) {
	return SendWithTimeout(socketPath, command, 30*time.Second)
}

// SendWithTimeout is like Send but with a caller-specified deadline.
// Used by --await which may need to wait longer than the default 30s.
func SendWithTimeout(socketPath, command string, timeout time.Duration) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("server not running (no socket at %s)", socketPath)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(command)); err != nil {
		return "", fmt.Errorf("sending command: %w", err)
	}

	// Half-close the write side so the server knows the request is complete and
	// can read it whole without a fixed-size buffer. This stays compatible with
	// OLD servers: they single-read the request (short commands are unaffected)
	// and close the conn after replying, so the ReadAll below still ends at EOF.
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	// Read the full reply until EOF instead of a single 256-byte read, which
	// truncated long responses (e.g. a get-command over 256B) into false
	// "command changed" warnings.
	resp, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	return string(resp), nil
}
