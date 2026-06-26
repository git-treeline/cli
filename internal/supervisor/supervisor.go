// Package supervisor provides a lightweight process wrapper that runs a
// command in the foreground while accepting restart/stop signals over a
// Unix socket. The user sees all output in their terminal; external
// callers (agents, MCP) control the lifecycle via the socket.
package supervisor

import (
	"crypto/sha256"
	"fmt"
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

	go func() {
		_ = cmd.Wait()
		close(done)
		s.mu.Lock()
		if s.child == cmd {
			s.child = nil
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
	s.mu.Unlock()

	_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)

	select {
	case <-waitCh:
	case <-time.After(10 * time.Second):
		s.Log("==> Process didn't exit in 10s, sending SIGKILL")
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		<-waitCh
	}

	s.mu.Lock()
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
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	raw := strings.TrimSpace(string(buf[:n]))
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

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	return string(buf[:n]), nil
}
