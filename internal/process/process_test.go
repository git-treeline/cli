package process

import (
	"net"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestParseLsofListeners(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Listener
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "single process",
			in:   "p123\ncnode\n",
			want: []Listener{{PID: 123, Name: "node"}},
		},
		{
			name: "multiple processes preserve order",
			in:   "p123\ncnode\np456\ncruby\n",
			want: []Listener{{PID: 123, Name: "node"}, {PID: 456, Name: "ruby"}},
		},
		{
			name: "missing command name",
			in:   "p123\np456\ncruby\n",
			want: []Listener{{PID: 123, Name: ""}, {PID: 456, Name: "ruby"}},
		},
		{
			name: "address lines are ignored",
			in:   "p123\ncnode\nn127.0.0.1:3000\n",
			want: []Listener{{PID: 123, Name: "node"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLsofListeners([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d listeners %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("listener[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestKillGracefully starts a child process we own (in its own process group)
// and asserts KillGracefully reaps it.
func TestKillGracefully(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap the zombie once it dies so the pid probe stays accurate.
	go func() { _ = cmd.Wait() }()

	if !Alive(pid) {
		t.Fatalf("child pid %d should be alive right after start", pid)
	}

	if !KillGracefully(pid, 3*time.Second) {
		t.Fatalf("KillGracefully reported the process still alive")
	}
	// Give Wait a moment to reap, then confirm it's gone.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && Alive(pid) {
		time.Sleep(50 * time.Millisecond)
	}
	if Alive(pid) {
		t.Errorf("child pid %d still alive after KillGracefully", pid)
	}
}

// TestWaitPortFree binds a listener and asserts WaitPortFree flips from false to
// true once the listener is closed.
func TestWaitPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// While the listener is open the port is not free.
	if WaitPortFree(port, 300*time.Millisecond) {
		t.Errorf("WaitPortFree returned true while listener on %d is open", port)
	}

	_ = ln.Close()

	if !WaitPortFree(port, 2*time.Second) {
		t.Errorf("WaitPortFree never saw port %d free after close", port)
	}
}

func TestAliveOnDeadPID(t *testing.T) {
	// Reap a short-lived child, then confirm Alive reports it gone.
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	if Alive(pid) {
		t.Errorf("pid %d should be reported dead after exit+wait", pid)
	}
}
