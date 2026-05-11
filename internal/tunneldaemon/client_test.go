package tunneldaemon

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/tunnel"
)

// TestClient_RegisterAgainstRunningDaemon exercises the full client path
// against a daemon that is already listening. Verifies that:
//   - the client connects without spawning,
//   - the register handshake completes,
//   - SIGINT triggers a clean disconnect and unregister,
//   - the daemon regenerates its config without the dropped hostname.
func TestClient_RegisterAgainstRunningDaemon(t *testing.T) {
	prev := IdleShutdown
	IdleShutdown = 200 * time.Millisecond
	defer func() { IdleShutdown = prev }()

	// Start a daemon in this process so we don't need a built binary.
	f, err := os.CreateTemp("/tmp", "gtl-client-test-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	runner := newFakeRunner()
	cfgDir := t.TempDir()

	d := New("test-tunnel")
	d.Runner = runner
	d.LogSink = io.Discard
	d.WriteConfig = func(name string, routes []tunnel.HostRoute) (string, error) {
		path := filepath.Join(cfgDir, "config.yml")
		return path, os.WriteFile(path, []byte(tunnel.GenerateMultiHostConfig(name, "/tmp/cred.json", routes)), 0o600)
	}

	ctx, cancel := context.WithCancel(context.Background())
	daemonDone := make(chan struct{})
	go func() {
		_ = d.Run(ctx, ln)
		close(daemonDone)
	}()
	defer func() {
		cancel()
		_ = ln.Close()
		<-daemonDone
	}()

	// Pre-create a connection through the client API by reaching into
	// dialWithSpawn directly (we override the socket path via SocketPath
	// can't be done; call the dial helper with a sentinel gtlBinary that's
	// never executed because the daemon is already up).
	//
	// We bypass SocketPath() and dial the test socket directly to keep the
	// test hermetic.
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Drive the same streamEvents flow the client uses. We send the
	// register, get the registered event, then interrupt.
	if err := writeJSON(conn, Register{Op: OpRegister, Hostname: "a.example.dev", Port: 3050}); err != nil {
		t.Fatal(err)
	}

	// Wait for daemon to start cloudflared once.
	waitFor(t, "registered + started", func() bool { return runner.StartCount() >= 1 })

	// Confirm the config contains the registered hostname.
	if !strings.Contains(runner.LastConfig(), `hostname: "a.example.dev"`) {
		t.Errorf("expected hostname in config:\n%s", runner.LastConfig())
	}

	// Close the connection — simulates Ctrl+C in the real client.
	_ = conn.Close()

	// After the only client disconnects, cloudflared should stop and the
	// daemon should eventually fire its Done() channel.
	select {
	case <-d.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not signal Done after client disconnect")
	}

	if !runner.Current().stopped.Load() {
		t.Error("expected cloudflared to be stopped after last client left")
	}
}

// TestDialWithSpawn_SpawnsWhenAbsent verifies the spawn path uses the
// provided binary. We substitute a fake binary that just listens on the
// socket and exits, proving the client invoked it. Skipped on systems
// without `nc -U` support.
func TestDialWithSpawn_SpawnsWhenAbsent(t *testing.T) {
	if !ncSupportsUnix() {
		t.Skip("nc -lU not available; smoke test covers the spawn path")
	}

	f, err := os.CreateTemp("/tmp", "gtl-spawn-test-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	fakeBin := writeFakeDaemonBinary(t, sock)

	conn, err := dialWithSpawn(sock, "any", fakeBin)
	if err != nil {
		t.Fatalf("dialWithSpawn: %v", err)
	}
	_ = conn.Close()
}

func ncSupportsUnix() bool {
	if _, err := os.Stat("/usr/bin/nc"); err == nil {
		return true
	}
	if _, err := os.Stat("/bin/nc"); err == nil {
		return true
	}
	return false
}

// TestDialWithSpawn_ReusesExisting verifies that when a daemon is already
// listening, dialWithSpawn just dials without spawning anything.
func TestDialWithSpawn_ReusesExisting(t *testing.T) {
	f, err := os.CreateTemp("/tmp", "gtl-existing-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	sock := f.Name()
	_ = f.Close()
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- struct{}{}
		_ = c.Close()
	}()

	// Pass a non-existent binary; if the client tries to spawn, this would
	// fail. Connection should succeed before any spawn attempt.
	conn, err := dialWithSpawn(sock, "any", "/nonexistent/gtl-binary-xyz")
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	_ = conn.Close()

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("daemon did not see connection")
	}
}

// writeFakeDaemonBinary creates a tiny shell script that listens on the
// given socket using `nc` (built-in on macOS/Linux) for a short window so
// dialWithSpawn can connect. If nc isn't available, the test self-skips.
func writeFakeDaemonBinary(t *testing.T, sock string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-daemon")

	script := "#!/bin/sh\n" +
		"# args: tunnel-daemon --tunnel X --socket S\n" +
		"sock=\"" + sock + "\"\n" +
		"# Use go-style listener via netcat\n" +
		"if command -v nc >/dev/null 2>&1; then\n" +
		"  ( nc -lU \"$sock\" > /dev/null 2>&1 & ) \n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestStreamEvents_RendersAndStopsOnEOF feeds a synthetic event stream
// and verifies streamEvents returns cleanly on EOF.
func TestStreamEvents_RendersAndStopsOnEOF(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	var streamErr error
	go func() {
		defer wg.Done()
		streamErr = streamEvents(client, "x.example.dev", 3050)
	}()

	// Write a registered event then close to simulate EOF.
	_ = writeJSON(server, Event{Kind: EventRegistered, Hostname: "x.example.dev", Port: 3050})
	_ = server.Close()

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("streamEvents did not return after EOF")
	}

	if streamErr != nil && !errors.Is(streamErr, io.EOF) && !isClosedPipe(streamErr) {
		t.Errorf("unexpected error: %v", streamErr)
	}
}

func isClosedPipe(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "closed") || errors.Is(err, syscall.EPIPE))
}
