// Package supervisor provides a lightweight process wrapper that runs a
// command in the foreground while accepting restart/stop signals over a
// Unix socket. The user sees all output in their terminal; external
// callers (agents, MCP) control the lifecycle via the socket.
package supervisor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

var errSupervisorShuttingDown = errors.New("supervisor is shutting down")

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
	// Command is the active, interpolated command. CommandTemplate retains the
	// original config value so a port reallocation can refresh {port} tokens
	// without silently adopting a later commands.start edit.
	Command         string
	CommandTemplate string
	Dir             string
	SocketPath      string
	Port            int
	Env             map[string]string // extra env vars injected into the child process
	Log             func(format string, args ...any)
	// ConnWriteDeadline caps how long handleConn waits to write a response.
	// Defaults to 15s. Override in tests to avoid slow-test hangs.
	ConnWriteDeadline time.Duration
	// ChildStdout/ChildStderr receive the supervised process's output. They
	// default to this process's own stdout/stderr so the user sees the server
	// in their terminal. Tests override them: when these are os.Stdout, the
	// child inherits the fd directly, so a child that outlives its test keeps
	// the test binary's stdout pipe open and `go test` stalls waiting for EOF.
	ChildStdout io.Writer
	ChildStderr io.Writer
	// StopTimeout and KillTimeout bound graceful and forced process-group
	// teardown. Tests shorten them; production uses the defaults below.
	StopTimeout time.Duration
	KillTimeout time.Duration

	mu           sync.Mutex
	opMu         sync.Mutex // serializes lifecycle/configure operations
	child        *exec.Cmd
	childDone    chan struct{} // closed when current child's Wait() completes
	childPGID    int           // retained until the entire child group is gone
	stopping     bool          // true while stopChildLocked has released s.mu to wait
	terminal     bool          // shutdown has claimed exclusive lifecycle ownership
	listener     net.Listener
	done         chan struct{}
	shutdownOnce sync.Once
}

// configureRequest is deliberately a complete replacement, not a patch: a
// deleted environment key must disappear from the next child just as a changed
// value does. It is base64 encoded on the simple line-oriented socket protocol
// so values cannot be confused with command delimiters.
type configureRequest struct {
	Action string            `json:"action"`
	Env    map[string]string `json:"env"`
	Port   int               `json:"port"`
}

// ConfigureAndSend atomically replaces the supervisor environment and port,
// then performs action ("restart" or "start"). The complete map is accepted
// even when empty, which is how callers remove all managed variables.
func ConfigureAndSend(socketPath, action string, env map[string]string, port int) (string, error) {
	if env == nil {
		env = map[string]string{}
	}
	payload, err := json.Marshal(configureRequest{Action: action, Env: env, Port: port})
	if err != nil {
		return "", fmt.Errorf("encoding runtime configuration: %w", err)
	}
	resp, err := Send(socketPath, "configure-action:"+base64.RawStdEncoding.EncodeToString(payload))
	if err != nil {
		return "", err
	}
	resp = strings.TrimSpace(resp)
	if resp == "ok" || (action == "start" && resp == "already running") {
		return resp, nil
	}
	if strings.HasPrefix(resp, "unknown command: configure-action") {
		return "", errors.New("supervisor uses an older protocol; run gtl stop --kill then gtl start")
	}
	if strings.HasPrefix(resp, "error:") {
		return "", fmt.Errorf("supervisor returned runtime configuration error: %s", strings.TrimSpace(strings.TrimPrefix(resp, "error:")))
	}
	return "", fmt.Errorf("unexpected supervisor response to runtime configuration: %q", resp)
}

// InterpolateCommand expands the allocated port tokens used in commands.start.
// It lives with Supervisor because the raw command must be retained and
// refreshed when a running allocation receives a new port.
func InterpolateCommand(command string, port int) string {
	if !strings.Contains(command, "{port") {
		return command
	}
	command = strings.ReplaceAll(command, "{port}", strconv.Itoa(port))
	for i := 2; i <= 10; i++ {
		token := fmt.Sprintf("{port_%d}", i)
		command = strings.ReplaceAll(command, token, strconv.Itoa(port+i-1))
	}
	return command
}

// orWriter returns w, or fallback when w is nil. A nil cmd.Stdout means
// /dev/null, so a Supervisor built without New would silently swallow the
// server's output instead of showing it.
func orWriter(w, fallback io.Writer) io.Writer {
	if w == nil {
		return fallback
	}
	return w
}

func New(command, dir, socketPath string) *Supervisor {
	return &Supervisor{
		Command:           command,
		CommandTemplate:   command,
		Dir:               dir,
		SocketPath:        socketPath,
		Log:               func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
		ConnWriteDeadline: 15 * time.Second,
		ChildStdout:       os.Stdout,
		ChildStderr:       os.Stderr,
		StopTimeout:       10 * time.Second,
		KillTimeout:       5 * time.Second,
		done:              make(chan struct{}),
	}
}

func (s *Supervisor) Run() error {
	// Hold a lifetime lock before inspecting or unlinking the socket. Two fresh
	// starts can otherwise both observe a missing socket and one can unlink the
	// other's newly-bound listener between its check and net.Listen.
	lockPath := s.SocketPath + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening supervisor lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return fmt.Errorf("supervisor startup already in progress or running")
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()

	if _, err := os.Stat(s.SocketPath); err == nil {
		if resp, dialErr := Send(s.SocketPath, "status"); dialErr == nil {
			return fmt.Errorf("supervisor already running (status: %s) on %s", resp, s.SocketPath)
		}
		// A failed dial does not prove the socket is stale: another fresh
		// supervisor can have bound it before its accept loop/PID file is ready.
		// Only unlink when a recorded owner is definitely dead.
		data, readErr := os.ReadFile(PidPath(s.SocketPath))
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if readErr != nil || parseErr != nil || pid <= 1 || syscall.Kill(pid, 0) == nil || syscall.Kill(pid, 0) == syscall.EPERM {
			return fmt.Errorf("supervisor socket exists but is not reachable: %s", s.SocketPath)
		}
		_ = os.Remove(PidPath(s.SocketPath))
	}
	_ = os.Remove(s.SocketPath)

	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return fmt.Errorf("listening on socket: %w", err)
	}
	_ = os.Chmod(s.SocketPath, 0600)
	s.listener = ln
	defer s.shutdownOnce.Do(func() { close(s.done) })

	pidPath := PidPath(s.SocketPath)
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600)
	defer func() {
		_ = ln.Close()
		_ = os.Remove(s.SocketPath)
		_ = os.Remove(pidPath)
		s.mu.Lock()
		pgid := s.childPGID
		s.mu.Unlock()
		// Do not discard the only recovery handle if a process group survived a
		// failed shutdown. A later force-kill can still reap it.
		if pgid <= 1 || !processGroupAlive(pgid) {
			_ = os.Remove(ChildPidPath(s.SocketPath))
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

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
			return s.shutdown()
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
	if s.terminal {
		return errSupervisorShuttingDown
	}
	if s.childPGID > 1 && processGroupAlive(s.childPGID) {
		return fmt.Errorf("child process group %d is still running", s.childPGID)
	}

	s.refreshCommandLocked()
	s.Log("==> Starting: %s", s.Command)
	cmd := exec.Command("sh", "-c", s.Command)
	cmd.Dir = s.Dir
	cmd.Stdout = orWriter(s.ChildStdout, os.Stdout)
	cmd.Stderr = orWriter(s.ChildStderr, os.Stderr)
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
	s.childPGID = cmd.Process.Pid
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
			// A shell leader can exit while a background descendant continues
			// serving. Keep the group id until the complete group is gone.
			if !processGroupAlive(cmd.Process.Pid) {
				s.childPGID = 0
				_ = os.Remove(ChildPidPath(s.SocketPath))
			} else {
				go s.clearExitedProcessGroup(cmd.Process.Pid)
			}
		}
		s.mu.Unlock()
	}()

	return nil
}

func (s *Supervisor) clearExitedProcessGroup(pgid int) {
	for processGroupAlive(pgid) {
		select {
		case <-s.done:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.childPGID == pgid {
		s.childPGID = 0
		_ = os.Remove(ChildPidPath(s.SocketPath))
	}
}

func (s *Supervisor) refreshCommandLocked() {
	if s.CommandTemplate != "" {
		s.Command = InterpolateCommand(s.CommandTemplate, s.Port)
	}
}

func (s *Supervisor) stopTimeout() time.Duration {
	if s.StopTimeout > 0 {
		return s.StopTimeout
	}
	return 10 * time.Second
}

func (s *Supervisor) killTimeout() time.Duration {
	if s.KillTimeout > 0 {
		return s.KillTimeout
	}
	return 5 * time.Second
}

func processGroupAlive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

func waitForProcessGroupExit(pgid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for processGroupAlive(pgid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
	return true
}

func (s *Supervisor) startChild() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startChildLocked()
}

// shutdown marks the supervisor terminal before stopping its child and closing
// done, all while opMu is held. Requests already accepted by the listener can
// therefore never start a replacement child after shutdown begins.
func (s *Supervisor) shutdown() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return errSupervisorShuttingDown
	}
	s.terminal = true
	err := s.stopChildLocked()
	s.shutdownOnce.Do(func() { close(s.done) })
	s.mu.Unlock()
	return err
}

// stopChildLocked sends SIGTERM to the child process group and waits for the
// *group*, rather than just its shell leader. Caller must hold s.mu.
// Caller must hold s.mu; the lock is released during the wait to avoid
// blocking status queries.
func (s *Supervisor) stopChildLocked() error {
	child := s.child
	pgid := s.childPGID
	if pgid <= 1 && child != nil && child.Process != nil {
		pgid = child.Process.Pid
	}
	if pgid <= 1 {
		return nil
	}
	s.child = nil
	s.childDone = nil
	s.stopping = true
	s.mu.Unlock()

	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	exited := waitForProcessGroupExit(pgid, s.stopTimeout())
	if !exited {
		s.Log("==> Process didn't exit in 10s, sending SIGKILL")
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		exited = waitForProcessGroupExit(pgid, s.killTimeout())
	}
	// The leader may have exited well before a descendant. The sidecar remains
	// valid until the whole group is gone.
	s.mu.Lock()
	if exited {
		s.childPGID = 0
		_ = os.Remove(ChildPidPath(s.SocketPath))
	}
	s.stopping = false
	if !exited {
		return fmt.Errorf("process group %d did not exit after SIGKILL", pgid)
	}
	return nil
}

func (s *Supervisor) stopChild() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return errSupervisorShuttingDown
	}
	err := s.stopChildLocked()
	s.mu.Unlock()
	return err
}

// restart atomically stops the current child and starts a new one.
// Holds the lock for the entire sequence to prevent concurrent restarts
// from spawning duplicate processes.
func (s *Supervisor) restart() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return errSupervisorShuttingDown
	}
	s.Log("\n==> Restarting server...")
	if err := s.stopChildLocked(); err != nil {
		return err
	}
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
		s.opMu.Lock()
		defer s.opMu.Unlock()
		s.mu.Lock()
		if s.terminal {
			s.mu.Unlock()
			_, _ = fmt.Fprintf(conn, "error: %s", errSupervisorShuttingDown)
			return
		}
		if s.childPGID > 1 && processGroupAlive(s.childPGID) {
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
		if err := s.stopChild(); err != nil {
			_, _ = fmt.Fprintf(conn, "error: %s", err)
			return
		}
		_, _ = fmt.Fprint(conn, "ok")
	case "shutdown":
		s.Log("\n==> Shutting down supervisor...")
		if err := s.shutdown(); err != nil {
			_, _ = fmt.Fprintf(conn, "error: %s", err)
			return
		}
		_, _ = fmt.Fprint(conn, "ok")
	case "status":
		s.mu.Lock()
		running := s.childPGID > 1 && processGroupAlive(s.childPGID)
		s.mu.Unlock()
		if running {
			_, _ = fmt.Fprint(conn, "running")
		} else {
			_, _ = fmt.Fprint(conn, "stopped")
		}
	case "get-command":
		s.mu.Lock()
		command := s.Command
		s.mu.Unlock()
		_, _ = fmt.Fprint(conn, command)
	case "update-env":
		s.opMu.Lock()
		defer s.opMu.Unlock()
		if len(parts) < 2 || parts[1] == "" {
			s.mu.Lock()
			terminal := s.terminal
			s.mu.Unlock()
			if terminal {
				_, _ = fmt.Fprintf(conn, "error: %s", errSupervisorShuttingDown)
				return
			}
			_, _ = fmt.Fprint(conn, "ok")
			return
		}
		s.mu.Lock()
		if s.terminal {
			s.mu.Unlock()
			_, _ = fmt.Fprintf(conn, "error: %s", errSupervisorShuttingDown)
			return
		}
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
	case "configure-action":
		if len(parts) < 2 {
			_, _ = fmt.Fprint(conn, "error: missing runtime configuration")
			return
		}
		payload, err := base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			_, _ = fmt.Fprintf(conn, "error: invalid runtime configuration: %s", err)
			return
		}
		var request configureRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			_, _ = fmt.Fprintf(conn, "error: invalid runtime configuration: %s", err)
			return
		}
		resp, err := s.configureAndAct(request)
		if err != nil {
			_, _ = fmt.Fprintf(conn, "error: %s", err)
			return
		}
		_, _ = fmt.Fprint(conn, resp)
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

func (s *Supervisor) configureAndAct(request configureRequest) (string, error) {
	if request.Action != "restart" && request.Action != "start" {
		return "", fmt.Errorf("unknown runtime action %q", request.Action)
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return "", errSupervisorShuttingDown
	}
	// Copy before publishing it so a caller cannot mutate state after the
	// request was encoded/decoded. A nil JSON map is also an empty replacement.
	s.Env = make(map[string]string, len(request.Env))
	for k, v := range request.Env {
		s.Env[k] = v
	}
	s.Port = request.Port
	s.refreshCommandLocked()

	if request.Action == "start" {
		if s.childPGID > 1 && processGroupAlive(s.childPGID) {
			return "already running", nil
		}
		if err := s.startChildLocked(); err != nil {
			return "", err
		}
		return "ok", nil
	}

	s.Log("\n==> Restarting server...")
	if err := s.stopChildLocked(); err != nil {
		return "", err
	}
	if err := s.startChildLocked(); err != nil {
		return "", err
	}
	return "ok", nil
}

func (s *Supervisor) handleWaitReady(conn net.Conn, timeout time.Duration) {
	s.mu.Lock()
	port := s.Port
	s.mu.Unlock()
	if port == 0 {
		_, _ = fmt.Fprint(conn, "error: no port configured")
		return
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		port = s.Port
		s.mu.Unlock()
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			_, _ = fmt.Fprint(conn, "ok")
			return
		}

		s.mu.Lock()
		childDone := s.childDone
		running := s.childPGID > 1 && processGroupAlive(s.childPGID)
		s.mu.Unlock()

		if !running {
			_, _ = fmt.Fprint(conn, "error: server not running")
			return
		}

		if childDone != nil {
			select {
			case <-childDone:
				// A shell can exit before its background server. Recheck the group
				// on the next loop instead of declaring the surviving child dead.
			case <-ticker.C:
			}
		} else {
			<-ticker.C
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
	// The dial is bounded too: a wedged supervisor with a full accept backlog
	// would otherwise block connect() indefinitely, before the deadline applies.
	conn, err := net.DialTimeout("unix", socketPath, timeout)
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
