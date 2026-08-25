// Package process is the single home for discovering and terminating the
// processes that hold gtl's TCP ports. It replaces three near-identical copies
// of the same lsof/ps/kill logic that had drifted across cmd and internal/service.
package process

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Listener is a process listening on a TCP port.
type Listener struct {
	PID  int
	Name string
}

// ListenersOnPort returns every process listening on the given TCP port, in the
// order lsof reports them. Empty if lsof is unavailable, errors, or the platform
// isn't supported.
func ListenersOnPort(port int) []Listener {
	// lsof ships on macOS and Linux; anything else has no supported discovery path.
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil
	}
	out, err := exec.Command("lsof", "-i", fmt.Sprintf("TCP:%d", port),
		"-sTCP:LISTEN", "-n", "-P", "-F", "cn").Output()
	if err != nil {
		return nil
	}
	return parseLsofListeners(out)
}

// parseLsofListeners turns `lsof -F cn` output into one Listener per process.
// The output is a flat stream of field lines: a `p<pid>` line opens a process
// record and the following `c<name>` line (when present) names it. Order is
// preserved; a process with no command line yields an empty Name.
func parseLsofListeners(out []byte) []Listener {
	var listeners []Listener
	cur := -1
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(line[1:])
			if err != nil {
				continue
			}
			listeners = append(listeners, Listener{PID: pid})
			cur = len(listeners) - 1
		case 'c':
			if cur >= 0 {
				listeners[cur].Name = line[1:]
			}
		}
	}
	return listeners
}

// CommandName returns the short command name for a PID via ps, or "" on error.
func CommandName(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	// ps may return a full path; trim to basename for readability.
	name := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// Alive reports whether the process exists (signal 0 probe). A permission error
// still means the process is there, so only ESRCH counts as gone.
func Alive(pid int) bool {
	return syscall.Kill(pid, 0) != syscall.ESRCH
}

// StartTime returns when the process began, via ps. The second return is
// false when the process is gone or ps could not be read. Used to tell a
// process apart from a later one that happens to reuse its pid.
func StartTime(pid int) (time.Time, bool) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return time.Time{}, false
	}
	// macOS ps renders lstart as "Tue Aug 25 17:22:57 2026", padding the day
	// to two columns for single digits.
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", raw, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// killablePgid rejects pids whose negation has a special meaning to kill(2).
// kill(-1, sig) signals EVERY process with the caller's uid — the user's whole
// session — and kill(-0, sig) signals our own process group. Neither is ever
// what a caller reaping a recorded child intends, so a corrupt, truncated, or
// hand-edited pid record must not be able to reach them.
func killablePgid(pgid int) bool {
	return pgid > 1
}

// KillGroup SIGKILLs the process group led by pgid, falling back to the single
// pid when the group signal is rejected. Returns true if a signal was
// delivered to something.
func KillGroup(pgid int) bool {
	if !killablePgid(pgid) {
		return false
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
		return true
	}
	return syscall.Kill(pgid, syscall.SIGKILL) == nil
}

// KillGracefully sends SIGTERM, waits up to timeout for the process to exit,
// then escalates to SIGKILL. It signals the whole process group (negative pid)
// so children spawned via `sh -c` die too, falling back to the single pid when
// the group signal is rejected. Returns true once the process is gone.
func KillGracefully(pid int, timeout time.Duration) bool {
	if !killablePgid(pid) {
		return false
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	time.Sleep(100 * time.Millisecond)
	return !Alive(pid)
}

// WaitPortFree polls the port until a loopback dial fails (nothing accepting) or
// timeout elapses. Returns true when the port is free. Callers use this rather
// than watching a pid because a graceful stop frees the port slightly before the
// process fully exits.
func WaitPortFree(port int, timeout time.Duration) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
	}
	return false
}
