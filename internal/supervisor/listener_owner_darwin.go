//go:build darwin

package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func ownedTCPListener(port, pgid int) bool {
	if port <= 0 || pgid <= 1 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-Fp").Output()
	if err != nil {
		return false
	}
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 || line[0] != 'p' {
			continue
		}
		pid, err := strconv.Atoi(line[1:])
		if err != nil || pid <= 1 {
			return false
		}
		owner, err := syscall.Getpgid(pid)
		if err != nil || owner != pgid {
			return false
		}
		found = true
	}
	return found
}
