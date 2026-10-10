package supervisor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// These fixtures exercise the socket handler and shutdown loop without binding
// sockets, starting children, or delivering any operating-system signals.
type settledTestConn struct {
	input    *strings.Reader
	output   bytes.Buffer
	mu       sync.Mutex
	deadline time.Time
	writeErr error
}

func (c *settledTestConn) Read(p []byte) (int, error) { return c.input.Read(p) }
func (c *settledTestConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	if !c.deadline.IsZero() && time.Now().After(c.deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return c.output.Write(p)
}
func (c *settledTestConn) Close() error                    { return nil }
func (c *settledTestConn) LocalAddr() net.Addr             { return &net.UnixAddr{Name: "fixture", Net: "unix"} }
func (c *settledTestConn) RemoteAddr() net.Addr            { return c.LocalAddr() }
func (c *settledTestConn) SetDeadline(t time.Time) error   { return c.SetWriteDeadline(t) }
func (c *settledTestConn) SetReadDeadline(time.Time) error { return nil }
func (c *settledTestConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *settledTestConn) response() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func settledSupervisor() *Supervisor {
	s := New("must never execute", "unused", "unused.sock")
	s.Log = func(string, ...any) {}
	return s
}

func settledRequest(s *Supervisor, request string) (*settledTestConn, <-chan struct{}) {
	c := &settledTestConn{input: strings.NewReader(request)}
	done := make(chan struct{})
	go func() { s.handleConn(c); close(done) }()
	return c, done
}

func awaitSettledTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture did not finish")
	}
}

func TestSettledShutdownDisabledPreservesLegacy(t *testing.T) {
	s := settledSupervisor()
	conn, done := settledRequest(s, settledShutdownCommand)
	awaitSettledTest(t, done)
	if err := settledShutdownResult(conn.response(), nil); !errors.Is(err, ErrSettledShutdownUnsupported) {
		t.Fatal("unset callback advertised settled protocol", err)
	}
	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal {
		t.Fatal("unsupported request stopped supervisor")
	}
	conn, done = settledRequest(s, "shutdown")
	awaitSettledTest(t, done)
	if conn.response() != "ok" {
		t.Fatalf("legacy response changed: %s", conn.response())
	}
	select {
	case <-s.done:
	default:
		t.Fatal("legacy shutdown did not finish")
	}
	if !errors.Is(s.shutdown(), errSupervisorShuttingDown) {
		t.Fatal("legacy repeat behavior changed")
	}
}

func TestSettledShutdownHeldCallbackSerializesRequestsAndSignals(t *testing.T) {
	s := settledSupervisor()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.ShutdownComplete = func() error { calls.Add(1); close(entered); <-release; return nil }
	first, firstDone := settledRequest(s, settledShutdownCommand)
	awaitSettledTest(t, entered)
	second, secondDone := settledRequest(s, settledShutdownCommand)
	restart, restartDone := settledRequest(s, "restart")
	signals := make(chan os.Signal, 1)
	loopDone := make(chan error, 1)
	go func() { loopDone <- s.waitForShutdown(signals) }()
	signals <- syscall.SIGTERM
	select {
	case <-s.done:
		t.Fatal("lifetime ended before callback")
	default:
	}
	select {
	case <-firstDone:
		t.Fatal("premature settled response")
	default:
	}
	select {
	case <-secondDone:
		t.Fatal("concurrent premature response")
	default:
	}
	select {
	case <-loopDone:
		t.Fatal("signal bypassed held completion")
	default:
	}
	status, statusDone := settledRequest(s, "status")
	awaitSettledTest(t, statusDone)
	if status.response() != "stopped" {
		t.Fatal("status deadlocked during completion")
	}
	close(release)
	awaitSettledTest(t, firstDone)
	awaitSettledTest(t, secondDone)
	awaitSettledTest(t, restartDone)
	if first.response() != settledShutdownAcknowledgement || second.response() != settledShutdownAcknowledgement {
		t.Fatal("settled callers did not share success")
	}
	if !strings.HasPrefix(restart.response(), "error:") || calls.Load() != 1 {
		t.Fatal("restart or callback replayed")
	}
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal loop did not settle")
	}
}

func TestSettledShutdownFailureRetainsOwnership(t *testing.T) {
	for _, failure := range []string{"child stop", "callback", "timeout", "panic"} {
		t.Run(failure, func(t *testing.T) {
			s := settledSupervisor()
			calls, stops := 0, 0
			known := errors.New("fixture cleanup failed")
			s.ShutdownComplete = func() error {
				calls++
				if failure == "panic" {
					panic("fixture panic")
				}
				if failure == "timeout" {
					return context.DeadlineExceeded
				}
				return known
			}
			err := s.shutdownWithStop(func() error {
				stops++
				if failure == "child stop" {
					return known
				}
				return nil
			})
			if err == nil {
				t.Fatal("failed shutdown reported success")
			}
			if again := s.shutdownWithStop(func() error { stops++; return nil }); again != err {
				t.Fatal("terminal outcome changed")
			}
			if stops != 1 || failure == "child stop" && calls != 0 || failure != "child stop" && calls != 1 {
				t.Fatalf("cleanup repeated: stops %d calls %d", stops, calls)
			}
			select {
			case <-s.done:
				t.Fatal("failed strict shutdown released lifetime")
			default:
			}
			conn, done := settledRequest(s, settledShutdownCommand)
			awaitSettledTest(t, done)
			if settledShutdownResult(conn.response(), nil) == nil {
				t.Fatal("failed callback acknowledged settled")
			}
			if !errors.Is(s.restart(), errSupervisorShuttingDown) {
				t.Fatal("failed shutdown permitted restart")
			}
			signals := make(chan os.Signal)
			loopDone := make(chan error, 1)
			go func() { loopDone <- s.waitForShutdown(signals) }()
			signals <- syscall.SIGHUP
			select {
			case <-loopDone:
				t.Fatal("signal released failed shutdown ownership")
			case <-time.After(10 * time.Millisecond):
			}
			// Only fixture teardown ends this deliberately retained owner.
			s.shutdownOnce.Do(func() { close(s.done) })
			select {
			case got := <-loopDone:
				if got != err {
					t.Fatal("failure evidence lost")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("fixture loop leaked")
			}
		})
	}
}

func TestSettledShutdownCallbackStartsAfterChildStop(t *testing.T) {
	s := settledSupervisor()
	stopping, release := make(chan struct{}), make(chan struct{})
	var callback atomic.Bool
	s.ShutdownComplete = func() error { callback.Store(true); return nil }
	finished := make(chan struct{})
	go func() {
		_ = s.shutdownWithStop(func() error { close(stopping); <-release; return nil })
		close(finished)
	}()
	awaitSettledTest(t, stopping)
	if callback.Load() {
		t.Fatal("callback preceded child group exit")
	}
	select {
	case <-s.done:
		t.Fatal("lifetime ended during child stop")
	default:
	}
	close(release)
	awaitSettledTest(t, finished)
	if !callback.Load() {
		t.Fatal("callback omitted after child stop")
	}
}

func TestSettledShutdownLostReplyDoesNotCancelOrReplay(t *testing.T) {
	for _, failure := range []error{io.ErrClosedPipe, os.ErrDeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			s := settledSupervisor()
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			calls := 0
			s.ShutdownComplete = func() error { calls++; close(entered); <-release; return nil }
			conn := &settledTestConn{input: strings.NewReader(settledShutdownCommand), writeErr: failure}
			go func() { s.handleConn(conn); close(finished) }()
			awaitSettledTest(t, entered)
			select {
			case <-s.done:
				t.Fatal("lost client cancelled cleanup")
			default:
			}
			close(release)
			awaitSettledTest(t, finished)
			if conn.response() != "" || settledShutdownResult(conn.response(), failure) == nil {
				t.Fatal("lost reply inferred success")
			}
			retry, done := settledRequest(s, settledShutdownCommand)
			awaitSettledTest(t, done)
			if retry.response() != settledShutdownAcknowledgement || calls != 1 {
				t.Fatal("retained owner replayed callback")
			}
		})
	}
}

func TestSettledShutdownStrictResponse(t *testing.T) {
	for _, response := range []string{"ok", "", "settled-v1\n", "error: shutdown not settled", "unknown command: shutdown-settled-v1", "unsupported: shutdown-settled-v1"} {
		if settledShutdownResult(response, nil) == nil {
			t.Fatalf("unproven response accepted: %q", response)
		}
	}
	if settledShutdownResult(settledShutdownAcknowledgement, nil) != nil {
		t.Fatal("exact settled response refused")
	}
	if settledShutdownResult(settledShutdownAcknowledgement, io.EOF) == nil {
		t.Fatal("partial transport error accepted")
	}
}
