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

type Supervisor struct {
	Command    string
	Dir        string
	SocketPath string
	Env        map[string]string // extra env vars injected into the child process
	Log        func(format string, args ...any)

	mu           sync.Mutex
	child        *exec.Cmd
	listener     net.Listener
	done         chan struct{}
	shutdownOnce sync.Once
}

func New(command, dir, socketPath string) *Supervisor {
	return &Supervisor{
		Command:    command,
		Dir:        dir,
		SocketPath: socketPath,
		Log:        func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
		done:       make(chan struct{}),
	}
}

func (s *Supervisor) Run() error {
	_ = os.Remove(s.SocketPath)

	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return fmt.Errorf("listening on socket: %w", err)
	}
	_ = os.Chmod(s.SocketPath, 0600)
	s.listener = ln
	defer func() {
		ln.Close()
		_ = os.Remove(s.SocketPath)
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

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

	go func() {
		_ = cmd.Wait()
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
	if child == nil || child.Process == nil {
		return
	}
	s.child = nil
	s.mu.Unlock()

	_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)

	exited := make(chan struct{})
	go func() {
		child.Wait()
		close(exited)
	}()

	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		s.Log("==> Process didn't exit in 10s, sending SIGKILL")
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		<-exited
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
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	cmd := strings.TrimSpace(string(buf[:n]))
	switch cmd {
	case "restart":
		if err := s.restart(); err != nil {
			fmt.Fprintf(conn, "error: %s", err)
			return
		}
		fmt.Fprint(conn, "ok")
	case "start":
		s.mu.Lock()
		if s.child != nil {
			s.mu.Unlock()
			fmt.Fprint(conn, "already running")
			return
		}
		err := s.startChildLocked()
		s.mu.Unlock()
		if err != nil {
			fmt.Fprintf(conn, "error: %s", err)
			return
		}
		fmt.Fprint(conn, "ok")
	case "stop":
		s.Log("\n==> Server stopped. Supervisor waiting...")
		s.stopChild()
		fmt.Fprint(conn, "ok")
	case "shutdown":
		s.Log("\n==> Shutting down supervisor...")
		s.stopChild()
		fmt.Fprint(conn, "ok")
		s.shutdownOnce.Do(func() { close(s.done) })
	case "status":
		s.mu.Lock()
		running := s.child != nil && s.child.Process != nil
		s.mu.Unlock()
		if running {
			fmt.Fprint(conn, "running")
		} else {
			fmt.Fprint(conn, "stopped")
		}
	default:
		fmt.Fprintf(conn, "unknown command: %s", cmd)
	}
}

// Send connects to a supervisor socket and sends a command.
// Returns the response string.
func Send(socketPath, command string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("server not running (no socket at %s)", socketPath)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))
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
