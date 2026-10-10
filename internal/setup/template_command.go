package setup

import (
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"
)

func runTemplateMigration(ctx context.Context, path, command string, env []string, log io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = path
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if ctx.Err() == nil {
			return err
		}
		stopTemplateMigration(cmd.Process.Pid)
	case <-ctx.Done():
		stopTemplateMigration(cmd.Process.Pid)
		<-done
	}
	return ctx.Err()
}

func stopTemplateMigration(pgid int) {
	if syscall.Kill(-pgid, syscall.SIGTERM) == syscall.ESRCH {
		return
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		// The shell can exit before its children, so inspect the whole group.
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return
		}
		select {
		case <-timer.C:
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return
		case <-ticker.C:
		}
	}
}
