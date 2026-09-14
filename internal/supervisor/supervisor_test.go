package supervisor

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestSupervisor_StopAndResume(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	marker := filepath.Join(dir, "started")

	cmd := "echo $$ >> " + marker + " && sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	resp, err := Send(sock, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if resp != "running" {
		t.Errorf("expected running, got %s", resp)
	}

	// Stop child — supervisor stays alive
	resp, err = Send(sock, "stop")
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	time.Sleep(200 * time.Millisecond)

	resp, err = Send(sock, "status")
	if err != nil {
		t.Fatalf("status after stop failed: %v", err)
	}
	if resp != "stopped" {
		t.Errorf("expected stopped after stop, got %s", resp)
	}

	// Resume via start
	resp, err = Send(sock, "start")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok from start, got %s", resp)
	}

	time.Sleep(500 * time.Millisecond)

	resp, err = Send(sock, "status")
	if err != nil {
		t.Fatalf("status after resume failed: %v", err)
	}
	if resp != "running" {
		t.Errorf("expected running after resume, got %s", resp)
	}

	data, _ := os.ReadFile(marker)
	lines := splitNonEmpty(string(data))
	if len(lines) < 2 {
		t.Errorf("expected at least 2 PIDs (start + resume), got %d", len(lines))
	}

	// Shutdown supervisor entirely
	_, _ = Send(sock, "shutdown")
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}
}

func TestSupervisor_Shutdown(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)

	sv := newTestSupervisor(t, "sleep 60", dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "shutdown")
	if err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("supervisor returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("expected socket to be cleaned up")
	}
}

func TestSupervisor_Restart(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	marker := filepath.Join(dir, "started")

	// Command creates a marker file with PID, then sleeps
	cmd := "echo $$ >> " + marker + " && sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	resp, err := Send(sock, "restart")
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	// Wait for second start
	time.Sleep(500 * time.Millisecond)

	data, _ := os.ReadFile(marker)
	lines := splitNonEmpty(string(data))
	if len(lines) < 2 {
		t.Errorf("expected at least 2 PIDs (start + restart), got %d: %q", len(lines), string(data))
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_UpdateEnv(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	envOut := filepath.Join(dir, "env.out")

	cmd := "env > " + envOut + " && sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)
	sv.Env = map[string]string{"GTL_TEST": "original"}

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, envOut, 2*time.Second)

	data, _ := os.ReadFile(envOut)
	if !strings.Contains(string(data), "GTL_TEST=original") {
		t.Fatalf("expected GTL_TEST=original in initial env, got:\n%s", data)
	}

	// Update env via socket
	resp, err := Send(sock, "update-env:GTL_TEST=updated\x00GTL_NEW=added")
	if err != nil {
		t.Fatalf("update-env failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	// Restart so the child picks up the new env
	resp, err = Send(sock, "restart")
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok from restart, got %s", resp)
	}

	time.Sleep(500 * time.Millisecond)

	data, _ = os.ReadFile(envOut)
	if !strings.Contains(string(data), "GTL_TEST=updated") {
		t.Errorf("expected GTL_TEST=updated after restart, got:\n%s", data)
	}
	if !strings.Contains(string(data), "GTL_NEW=added") {
		t.Errorf("expected GTL_NEW=added after restart, got:\n%s", data)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_ConfigureAndRestartReplacesRuntimeEnvironment(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	envOut := filepath.Join(dir, "runtime.env")
	commandOut := filepath.Join(dir, "runtime.command")
	command := "printf '%s,%s\\n' {port} {port_2} > \"$GTL_COMMAND_OUT\"; env > \"$GTL_ENV_OUT\"; sleep 60"

	sv := newTestSupervisor(t, command, dir, sock)
	sv.Port = 4101
	sv.Env = map[string]string{
		"GTL_ENV_OUT":     envOut,
		"GTL_COMMAND_OUT": commandOut,
		"OLD":             "old-value",
		"REMOVE":          "remove-me",
	}
	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, envOut, 2*time.Second)
	waitForFile(t, commandOut, 2*time.Second)

	resp, err := ConfigureAndSend(sock, "restart", map[string]string{
		"GTL_ENV_OUT":     envOut,
		"GTL_COMMAND_OUT": commandOut,
		"NEW":             "new-value",
	}, 4201)
	if err != nil {
		t.Fatalf("configure/restart failed: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("configure/restart response = %q, want ok", resp)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(envOut)
		commandData, _ := os.ReadFile(commandOut)
		if strings.Contains(string(data), "NEW=new-value") &&
			!strings.Contains(string(data), "OLD=old-value") &&
			!strings.Contains(string(data), "REMOVE=remove-me") &&
			strings.TrimSpace(string(commandData)) == "4201,4202" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replacement was not observed by child; env=%q command=%q", data, commandData)
		}
		time.Sleep(25 * time.Millisecond)
	}

	gotCommand, err := Send(sock, "get-command")
	if err != nil {
		t.Fatalf("get-command failed: %v", err)
	}
	if !strings.Contains(gotCommand, "4201") || !strings.Contains(gotCommand, "4202") {
		t.Errorf("active command did not refresh allocated ports: %q", gotCommand)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_ConfigureRestartSerializesWithWaitReady(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	readyPort := listener.Addr().(*net.TCPAddr).Port

	// Pick a port with no listener so wait-ready remains active until the
	// configure operation publishes readyPort under the supervisor mutex.
	unused, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stalePort := unused.Addr().(*net.TCPAddr).Port
	_ = unused.Close()

	sv := newTestSupervisor(t, "sleep 60", dir, sock)
	sv.Port = stalePort
	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)

	waitResult := make(chan string, 1)
	go func() {
		resp, _ := SendWithTimeout(sock, "wait-ready:2", 3*time.Second)
		waitResult <- resp
	}()
	time.Sleep(50 * time.Millisecond)

	results := make(chan string, 2)
	for _, value := range []string{"one", "two"} {
		value := value
		go func() {
			resp, _ := ConfigureAndSend(sock, "restart", map[string]string{"VALUE": value}, readyPort)
			results <- resp
		}()
	}
	for range 2 {
		if resp := <-results; resp != "ok" {
			t.Fatalf("configure/restart response = %q, want ok", resp)
		}
	}
	if resp := <-waitResult; resp != "ok" {
		t.Fatalf("wait-ready did not observe atomically updated port: %q", resp)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_ShutdownRejectsQueuedConfigure(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	marker := filepath.Join(dir, "starts")
	// Keep shutdown in stopChildLocked long enough to queue a configure action
	// behind its opMu. The command is a real child process, not a mocked state.
	command := "trap 'sleep 1; exit 0' TERM; echo $$ >> " + marker + "; sleep 60"
	sv := newTestSupervisor(t, command, dir, sock)
	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	shutdownResult := make(chan string, 1)
	go func() {
		resp, _ := Send(sock, "shutdown")
		shutdownResult <- resp
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		sv.mu.Lock()
		terminal := sv.terminal
		sv.mu.Unlock()
		if terminal {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not enter terminal state")
		}
		time.Sleep(10 * time.Millisecond)
	}

	configured := make(chan error, 1)
	go func() {
		_, err := sv.configureAndAct(configureRequest{Action: "restart", Env: map[string]string{"AFTER": "shutdown"}})
		configured <- err
	}()
	if err := <-configured; !errors.Is(err, errSupervisorShuttingDown) {
		t.Fatalf("queued configure error = %v, want terminal shutdown error", err)
	}
	if resp := <-shutdownResult; resp != "ok" {
		t.Fatalf("shutdown response = %q, want ok", resp)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("supervisor returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not exit after terminal shutdown")
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if starts := splitNonEmpty(string(data)); len(starts) != 1 {
		t.Fatalf("queued configure spawned after shutdown: starts=%q", data)
	}
	if _, err := os.Stat(ChildPidPath(sock)); !os.IsNotExist(err) {
		t.Fatalf("terminal shutdown left child sidecar: %v", err)
	}
}

func TestConfigureAndSendValidatesSupervisorResponse(t *testing.T) {
	t.Run("legacy supervisor", func(t *testing.T) {
		sock, requests := protocolReplySupervisor(t, "unknown command: configure-action")
		resp, err := ConfigureAndSend(sock, "restart", map[string]string{"NEW": "value"}, 4321)
		if err == nil || !strings.Contains(err.Error(), "supervisor uses an older protocol; run gtl stop --kill then gtl start") {
			t.Fatalf("legacy configure error = %v, want actionable protocol upgrade error", err)
		}
		if resp != "" {
			t.Fatalf("legacy configure response = %q, want empty on error", resp)
		}
		if request := <-requests; !strings.HasPrefix(request, "configure-action:") {
			t.Fatalf("legacy supervisor request = %q, want configure-action", request)
		}
	})

	t.Run("server error", func(t *testing.T) {
		sock, _ := protocolReplySupervisor(t, "error: rejected")
		_, err := ConfigureAndSend(sock, "restart", map[string]string{}, 0)
		if err == nil || !strings.Contains(err.Error(), "supervisor returned runtime configuration error: rejected") {
			t.Fatalf("server error = %v, want normalized error", err)
		}
	})

	t.Run("unexpected response", func(t *testing.T) {
		sock, _ := protocolReplySupervisor(t, "maybe")
		_, err := ConfigureAndSend(sock, "restart", map[string]string{}, 0)
		if err == nil || !strings.Contains(err.Error(), "unexpected supervisor response") {
			t.Fatalf("unexpected response error = %v", err)
		}
	})

	t.Run("start already running", func(t *testing.T) {
		sock, _ := protocolReplySupervisor(t, "already running")
		resp, err := ConfigureAndSend(sock, "start", map[string]string{}, 0)
		if err != nil || resp != "already running" {
			t.Fatalf("start response = %q, %v; want already running, nil", resp, err)
		}
	})
}

func protocolReplySupervisor(t *testing.T, reply string) (string, <-chan string) {
	t.Helper()
	sock := tmpSocket(t)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	requests := make(chan string, 1)
	go func() {
		defer close(requests)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		request, _ := io.ReadAll(conn)
		requests <- string(request)
		_, _ = io.WriteString(conn, reply)
	}()
	return sock, requests
}

// TestSupervisor_StopWaitsForSurvivingDescendant uses a real inherited pipe
// fd. The shell leader exits on SIGTERM while its background descendant ignores
// it; stop must escalate to the entire group before replying and the inherited
// descriptor must reach EOF after the parent writer closes.
func TestSupervisor_StopWaitsForSurvivingDescendant(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	ready := filepath.Join(dir, "descendant-ready")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	command := "sh -c 'trap \"\" TERM; echo ready > " + ready + "; echo inherited-fd; while :; do sleep 1; done' & wait"
	sv := newTestSupervisor(t, command, dir, sock)
	sv.ChildStdout = writer
	sv.ChildStderr = writer
	sv.StopTimeout = 150 * time.Millisecond
	sv.KillTimeout = 2 * time.Second
	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, ready, 2*time.Second)

	start := time.Now()
	resp, err := Send(sock, "stop")
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("stop response = %q, want ok", resp)
	}
	if elapsed := time.Since(start); elapsed < sv.StopTimeout {
		t.Fatalf("stop returned before graceful process-group timeout: %s", elapsed)
	}
	if _, err := os.Stat(ChildPidPath(sock)); !os.IsNotExist(err) {
		t.Fatalf("child group sidecar remains after successful stop: %v", err)
	}

	_ = writer.Close()
	eof := make(chan error, 1)
	go func() { _, err := io.ReadAll(reader); eof <- err }()
	select {
	case err := <-eof:
		if err != nil {
			t.Fatalf("reading inherited fd: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("surviving descendant still held the inherited output fd after stop")
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_GetCommand(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)

	cmd := "sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "get-command")
	if err != nil {
		t.Fatalf("get-command failed: %v", err)
	}
	if resp != cmd {
		t.Errorf("get-command returned %q, want %q", resp, cmd)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

// TestSupervisor_UpdateEnvLargePayload verifies the framed protocol carries an
// update-env payload well past the old 4096-byte server read buffer without
// truncating a value mid-pair. A cut value used to slip through SplitN and
// corrupt the child's env.
func TestSupervisor_UpdateEnvLargePayload(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)

	sv := newTestSupervisor(t, "sleep 60", dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)

	// A single value larger than the old 4096-byte read window.
	bigVal := strings.Repeat("x", 8192)
	resp, err := Send(sock, "update-env:BIG="+bigVal+"\x00SECOND=tail")
	if err != nil {
		t.Fatalf("update-env failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	sv.mu.Lock()
	got := sv.Env["BIG"]
	tail := sv.Env["SECOND"]
	sv.mu.Unlock()
	if got != bigVal {
		t.Errorf("BIG env truncated: got %d bytes, want %d", len(got), len(bigVal))
	}
	// The trailing pair must survive too — proof nothing was cut mid-stream.
	if tail != "tail" {
		t.Errorf("expected SECOND=tail, got %q", tail)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

// TestSupervisor_GetCommandLargeReply verifies a get-command reply longer than
// the old 256-byte client buffer round-trips whole, so warnStaleCommand can't
// fire on a truncated command string.
func TestSupervisor_GetCommandLargeReply(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)

	// A start command comfortably over 256 bytes.
	cmd := "echo " + strings.Repeat("a", 512) + " && sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "get-command")
	if err != nil {
		t.Fatalf("get-command failed: %v", err)
	}
	if resp != cmd {
		t.Errorf("get-command truncated: got %d bytes, want %d", len(resp), len(cmd))
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

// TestSupervisor_StartDuringStopIsSerialized verifies that a racing start never
// reports "ok" without a child. Lifecycle operations are serialized, so by the
// time start runs the restart has completed and it accurately reports running.
func TestSupervisor_StartDuringStopIsSerialized(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	marker := filepath.Join(dir, "started")

	// Linger on SIGTERM so the stop window stays open for the racing start.
	cmd := "trap 'sleep 1; exit 0' TERM; echo $$ >> " + marker + "; sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	// Enter the stop window, then fire a start into it.
	go func() { _, _ = Send(sock, "restart") }()
	time.Sleep(300 * time.Millisecond)

	resp, err := Send(sock, "start")
	if err != nil {
		t.Fatalf("racing start send failed: %v", err)
	}
	if resp != "already running" {
		t.Errorf("expected serialized start to find the restarted child, got %q", resp)
	}

	// The supervisor must recover: restart finishes and the server is running.
	// Poll rather than sleeping a fixed 2s — the child lingers 1s on SIGTERM
	// before the fresh one starts, which under parallel-suite load overran a
	// fixed wait and reported a spurious "stopped".
	waitForStatus(t, sock, "running", 15*time.Second)

	_, _ = Send(sock, "shutdown")
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}
}

func TestSupervisor_UpdateEnvEmpty(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)

	sv := newTestSupervisor(t, "sleep 60", dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	resp, err := Send(sock, "update-env:")
	if err != nil {
		t.Fatalf("update-env with empty payload failed: %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected ok, got %s", resp)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func TestSupervisor_StatusWhenStopped(t *testing.T) {
	_, err := Send("/nonexistent/test.sock", "status")
	if err == nil {
		t.Error("expected error connecting to nonexistent socket")
	}
}

// TestSupervisor_ChildPidFileLifecycle verifies the child pgid sidecar is
// written while a child runs and removed once it stops, so a force-kill can
// reap the child process group.
func TestSupervisor_ChildPidFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	childPidPath := ChildPidPath(sock)

	sv := newTestSupervisor(t, "sleep 60", dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, childPidPath, 2*time.Second)

	data, err := os.ReadFile(childPidPath)
	if err != nil {
		t.Fatalf("reading child pid file: %v", err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pgid <= 1 {
		t.Fatalf("expected a valid pgid in %s, got %q", childPidPath, string(data))
	}
	// The persisted value must be a real process group leader — that's what
	// makes syscall.Kill(-pgid, ...) reap the whole group.
	if got, err := syscall.Getpgid(pgid); err != nil || got != pgid {
		t.Fatalf("persisted value %d is not a group leader (getpgid=%d err=%v)", pgid, got, err)
	}

	// Stopping the child must remove the sidecar.
	if _, err := Send(sock, "stop"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	waitForFileGone(t, childPidPath, 2*time.Second)

	_, _ = Send(sock, "shutdown")
	<-errCh
}

// TestSupervisor_StartDuringStopSpawnsNoExtraChild reproduces the stop/start
// race: while restart is inside stopChildLocked (s.mu released, waiting for the
// old child to exit), a competing 'start' arrives. Only one child — the one
// restart starts after the stop completes — must end up tracked and running.
func TestSupervisor_StartDuringStopSpawnsNoExtraChild(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)
	marker := filepath.Join(dir, "started")

	// On SIGTERM, linger ~1s before exiting so the stop window stays open long
	// enough for a racing 'start' to land inside it.
	cmd := "trap 'sleep 1; exit 0' TERM; echo $$ >> " + marker + "; sleep 60"
	sv := newTestSupervisor(t, cmd, dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)
	waitForFile(t, marker, 2*time.Second)

	// Kick off a restart; it enters the stop window immediately (s.mu released
	// before the SIGTERM linger). Fire a competing start mid-window.
	go func() { _, _ = Send(sock, "restart") }()
	time.Sleep(300 * time.Millisecond)
	if _, err := Send(sock, "start"); err != nil {
		t.Fatalf("racing start failed: %v", err)
	}

	// Let the SIGTERM linger elapse and restart start its fresh child.
	time.Sleep(2 * time.Second)

	resp, err := Send(sock, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if resp != "running" {
		t.Errorf("expected running after restart, got %s", resp)
	}

	// Exactly two starts total: the initial child and restart's fresh child.
	// A third line means the racing start spawned an untracked competitor.
	data, _ := os.ReadFile(marker)
	if lines := splitNonEmpty(string(data)); len(lines) != 2 {
		t.Errorf("expected 2 child starts (initial + restart), got %d: %q", len(lines), string(data))
	}

	_, _ = Send(sock, "shutdown")
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("supervisor didn't exit after shutdown")
	}
}

// newTestSupervisor builds a Supervisor wired for tests: quiet logging, child
// output captured instead of inherited, and a guaranteed reap of the child
// process group when the test ends.
//
// Containing the child's output is what keeps a leak from failing the whole
// package: with ChildStdout left at os.Stdout the child inherits the test
// binary's stdout pipe, and one child outliving its test makes `go test` sit
// waiting for EOF and report "Test I/O incomplete" against the package — not
// against the test that leaked.
func newTestSupervisor(t *testing.T, command, dir, sock string) *Supervisor {
	t.Helper()
	sv := New(command, dir, sock)
	sv.Log = func(f string, a ...any) {}
	out := &syncBuffer{}
	sv.ChildStdout = out
	sv.ChildStderr = out
	t.Cleanup(func() {
		reapChildGroup(t, sock)
		if t.Failed() {
			if s := out.String(); s != "" {
				t.Logf("child output:\n%s", s)
			}
		}
	})
	return sv
}

// syncBuffer is an io.Writer safe for a child that outlives the test. Writing
// to t.Log after a test completes panics, and a leaked child is exactly the
// case where that would happen, so output is buffered and only surfaced on
// failure.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// reapChildGroup SIGKILLs the supervised process group recorded in the sidecar
// pid file. Tests that fail or time out can return before the supervisor stops
// its child; without this the orphan survives the run.
func reapChildGroup(t *testing.T, sock string) {
	t.Helper()
	raw, err := os.ReadFile(ChildPidPath(sock))
	if err != nil {
		return
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pgid <= 0 {
		return
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
		t.Logf("reaped leaked child group %d", pgid)
	}
	_ = os.Remove(ChildPidPath(sock))
}

// tmpSocket returns a short /tmp socket path (macOS caps unix socket paths at
// ~104 bytes, which t.TempDir() paths exceed) and registers its cleanup.
func tmpSocket(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("/tmp", "gtl-test-*.sock")
	if err != nil {
		t.Fatalf("create temp sock: %v", err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() {
		reapChildGroup(t, sock)
		_ = os.Remove(sock)
		_ = os.Remove(sock + ".lock")
		_ = os.Remove(ChildPidPath(sock))
		_ = os.Remove(PidPath(sock))
	})
	return sock
}

func waitForFileGone(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s still present after %s", path, timeout)
}

// waitForStatus polls the supervisor's status until it reports want.
func waitForStatus(t *testing.T, sock, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = Send(sock, "status")
		if lastErr == nil && last == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status never became %q within %s (last %q, err %v)", want, timeout, last, lastErr)
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("socket %s not created within %s", path, timeout)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s not created within %s", path, timeout)
}

func TestSupervisor_SIGHUPShutdown(t *testing.T) {
	dir := t.TempDir()
	sock := tmpSocket(t)

	sv := newTestSupervisor(t, "sleep 60", dir, sock)

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()

	waitForSocket(t, sock, 2*time.Second)

	// SIGHUP should trigger graceful shutdown identical to SIGINT/SIGTERM.
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGHUP)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("supervisor returned error on SIGHUP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor didn't exit after SIGHUP")
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("expected socket to be cleaned up after SIGHUP")
	}
}

// TestSupervisor_WriteDeadlineUnblocksLock verifies that a hung client
// (connects, sends a command, never reads the response) doesn't hold the
// supervisor's mutex indefinitely. A subsequent "status" query must succeed
// once the write deadline fires on the stuck connection.
func TestSupervisor_WriteDeadlineUnblocksLock(t *testing.T) {
	dir := t.TempDir()
	// macOS caps unix socket paths at ~104 bytes; t.TempDir() paths exceed that.
	f, err := os.CreateTemp("/tmp", "gtl-test-*.sock")
	if err != nil {
		t.Fatalf("create temp sock: %v", err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	sv := newTestSupervisor(t, "sleep 60", dir, sock)
	sv.ConnWriteDeadline = 200 * time.Millisecond // fast deadline for the test

	errCh := make(chan error, 1)
	go func() { errCh <- sv.Run() }()
	waitForSocket(t, sock, 2*time.Second)

	// Connect and send "restart" but never read the response — simulates a
	// client that disappears mid-command (e.g. gtl stop timing out).
	hung, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = hung.Close() }()
	_, _ = hung.Write([]byte("restart"))
	// Deliberately not reading the response.

	// Wait for the write deadline to fire and the handleConn goroutine to exit.
	time.Sleep(400 * time.Millisecond)

	// The mutex must be free now — status should respond immediately.
	resp, err := SendWithTimeout(sock, "status", 2*time.Second)
	if err != nil {
		t.Fatalf("status after hung client: %v", err)
	}
	if resp != "running" && resp != "stopped" {
		t.Errorf("unexpected status response: %q", resp)
	}

	_, _ = Send(sock, "shutdown")
	<-errCh
}

func splitNonEmpty(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

// TestSupervisor_ChildOutputRouting pins the two halves of the contract that
// keeps a leaked child from failing the whole package: New must default the
// child's output to this process's own streams (so a real server still shows
// up in the user's terminal), and an override must actually receive it (so
// tests can keep the child off the test binary's stdout pipe).
func TestSupervisor_ChildOutputRouting(t *testing.T) {
	t.Run("New defaults to the process streams", func(t *testing.T) {
		sv := New("true", t.TempDir(), "/tmp/unused.sock")
		if sv.ChildStdout != os.Stdout {
			t.Errorf("ChildStdout = %v, want os.Stdout", sv.ChildStdout)
		}
		if sv.ChildStderr != os.Stderr {
			t.Errorf("ChildStderr = %v, want os.Stderr", sv.ChildStderr)
		}
	})

	t.Run("override receives child output", func(t *testing.T) {
		dir := t.TempDir()
		sock := tmpSocket(t)
		sv := newTestSupervisor(t, "echo hello-from-child && sleep 60", dir, sock)
		out := &syncBuffer{}
		sv.ChildStdout = out

		errCh := make(chan error, 1)
		go func() { errCh <- sv.Run() }()
		waitForSocket(t, sock, 3*time.Second)

		deadline := time.Now().Add(3 * time.Second)
		for !strings.Contains(out.String(), "hello-from-child") {
			if time.Now().After(deadline) {
				t.Fatalf("child output never reached the override, got %q", out.String())
			}
			time.Sleep(20 * time.Millisecond)
		}

		_, _ = Send(sock, "shutdown")
		<-errCh
	})
}
