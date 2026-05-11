package tunneldaemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RegisterAndWait connects to the daemon for tunnelName (spawning it if not
// running), registers (hostname → localhost:port), then blocks until the
// process is interrupted. cloudflared output relevant to this hostname is
// printed to stdout/stderr. The returned error is non-nil only if the
// daemon refused the registration or the connection died unexpectedly.
//
// gtlBinary is the path used to spawn the daemon when one isn't already
// running (typically os.Args[0]); if empty, RegisterAndWait will resolve
// it via os.Executable().
func RegisterAndWait(tunnelName, hostname string, port int, gtlBinary string) error {
	socketPath := SocketPath(tunnelName)
	conn, err := dialWithSpawn(socketPath, tunnelName, gtlBinary)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := writeJSON(conn, Register{Op: OpRegister, Hostname: hostname, Port: port}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	return streamEvents(conn, hostname, port)
}

// dialWithSpawn dials the daemon socket and, if no daemon answers, forks
// a detached daemon process and retries with backoff.
func dialWithSpawn(socketPath, tunnelName, gtlBinary string) (net.Conn, error) {
	if c, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond); err == nil {
		return c, nil
	}

	bin := gtlBinary
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("locate gtl binary: %w", err)
		}
		bin = exe
	}

	if err := spawnDaemon(bin, tunnelName, socketPath); err != nil {
		return nil, fmt.Errorf("spawn daemon: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond); err == nil {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("daemon did not start in time")
}

func spawnDaemon(gtlBinary, tunnelName, socketPath string) error {
	logPath := filepath.Join(os.TempDir(), fmt.Sprintf("gtl-tunnel-%s.log", sanitizeFilename(tunnelName)))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		logFile = nil
	}

	cmd := exec.Command(gtlBinary, "tunnel-daemon",
		"--tunnel", tunnelName,
		"--socket", socketPath,
	)
	cmd.Stdin = nil
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "GTL_TUNNEL_DAEMON=1")
	if err := cmd.Start(); err != nil {
		return err
	}
	// We don't Wait — the daemon is meant to outlive this call.
	go func() { _ = cmd.Process.Release() }()
	return nil
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// streamEvents reads events from the daemon until the user interrupts or
// the connection closes. On SIGINT/SIGTERM, the connection is closed so
// the daemon unregisters this hostname.
func streamEvents(conn net.Conn, hostname string, port int) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	done := make(chan error, 1)
	go func() {
		dec := json.NewDecoder(bufio.NewReader(conn))
		for {
			var ev Event
			if err := dec.Decode(&ev); err != nil {
				done <- err
				return
			}
			renderEvent(ev, hostname, port)
			switch ev.Kind {
			case EventError:
				done <- fmt.Errorf("%s", ev.Error)
				return
			case EventTunnelDown:
				msg := ev.Error
				if msg == "" {
					msg = "cloudflared exited"
				}
				done <- fmt.Errorf("tunnel down: %s", msg)
				return
			}
		}
	}()

	select {
	case <-sigs:
		_ = conn.Close()
		// Drain so we exit cleanly without printing partial events.
		<-done
		return nil
	case err := <-done:
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func renderEvent(ev Event, hostname string, port int) {
	switch ev.Kind {
	case EventRegistered:
		_, _ = fmt.Fprintf(os.Stdout, "Tunnel: https://%s → http://localhost:%d\n", ev.Hostname, ev.Port)
		_, _ = fmt.Fprintln(os.Stdout, "Press Ctrl+C to stop")
		_, _ = fmt.Fprintln(os.Stdout)
	case EventTunnelUp:
		// Initial cloudflared "Registered tunnel connection" line; surface to
		// the user so they see the tunnel really came online.
		_, _ = fmt.Fprintln(os.Stdout, ev.Line)
	case EventLog:
		if ev.Stream == StreamStderr {
			fmt.Fprintln(os.Stderr, ev.Line)
		} else {
			_, _ = fmt.Fprintln(os.Stdout, ev.Line)
		}
	case EventError:
		fmt.Fprintln(os.Stderr, ev.Error)
	case EventTunnelDown:
		msg := ev.Error
		if msg == "" {
			msg = "cloudflared exited"
		}
		fmt.Fprintf(os.Stderr, "Tunnel down: %s\n", msg)
	}
}

func sanitizeFilename(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(s)
}
