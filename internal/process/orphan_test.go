package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// startGroupLeader spawns a sleep in its own process group and returns its pid
// (== pgid). Wait runs in the background so that once the process is killed it
// is reaped promptly: a zombie still answers signal 0, so Alive would keep
// reporting it as running and mask whether the kill landed. In production the
// orphan's parent is init, which reaps it for us.
func startGroupLeader(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "45")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
		}
	})
	return pid
}

func waitGone(t *testing.T, pid int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestTrackerRecordsAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	tr := NewTracker(dir)
	if tr == nil {
		t.Fatal("NewTracker returned nil")
	}
	tr.Add(4242)

	sidecar := filepath.Join(dir, probeDirName, "")
	entries, err := os.ReadDir(sidecar)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 sidecar, got %v (err %v)", entries, err)
	}
	path := filepath.Join(sidecar, entries[0].Name())
	if _, ok := readOwnerStart(path); !ok {
		t.Error("sidecar carries no owner start time, so a recycled pid could not be detected")
	}
	if recs := readRecords(path); len(recs) != 1 || recs[0].pgid != 4242 {
		t.Errorf("records = %v, want one entry for 4242", recs)
	}

	// A clean exit leaves nothing for a later run to reap.
	tr.Close()
	entries, _ = os.ReadDir(sidecar)
	if len(entries) != 0 {
		t.Errorf("Close should remove the sidecar, found %d", len(entries))
	}

	// Add and Close on a nil tracker are no-ops, so callers need not branch.
	var nilTracker *Tracker
	nilTracker.Add(1)
	nilTracker.Close()
}

func TestReapStale(t *testing.T) {
	t.Run("reaps a group left by a dead owner", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		writeSidecar(t, dir, 999999, pid, time.Now(), time.Time{})

		if n := ReapStale(dir); n != 1 {
			t.Fatalf("ReapStale killed %d groups, want 1", n)
		}
		if !waitGone(t, pid, 2*time.Second) {
			t.Errorf("recorded group (pid %d) survived reaping", pid)
		}
		if entries, _ := os.ReadDir(filepath.Join(dir, probeDirName)); len(entries) != 0 {
			t.Errorf("sidecar should be removed after reaping, found %d", len(entries))
		}
	})

	t.Run("leaves a live owner's children alone", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		// os.Getppid() is alive, standing in for another running gtl.
		writeSidecar(t, dir, os.Getppid(), pid, time.Now(), ownerStartOf(t, os.Getppid()))

		if n := ReapStale(dir); n != 0 {
			t.Fatalf("ReapStale killed %d groups, want 0", n)
		}
		if !Alive(pid) {
			t.Error("killed a live owner's child")
		}
		if entries, _ := os.ReadDir(filepath.Join(dir, probeDirName)); len(entries) != 1 {
			t.Error("a live owner's sidecar should be left in place")
		}
	})

	t.Run("skips our own sidecar", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		writeSidecar(t, dir, os.Getpid(), pid, time.Now(), ownerStartOf(t, os.Getpid()))

		if n := ReapStale(dir); n != 0 {
			t.Fatalf("ReapStale killed %d groups, want 0", n)
		}
		if !Alive(pid) {
			t.Error("reaped a group belonging to the current run")
		}
	})

	t.Run("refuses a pid that started after it was recorded", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		// The hallmark of pid reuse: the live process is newer than the record.
		writeSidecar(t, dir, 999999, pid, time.Now().Add(-time.Hour), time.Time{})

		if n := ReapStale(dir); n != 0 {
			t.Fatalf("ReapStale killed %d groups, want 0", n)
		}
		if !Alive(pid) {
			t.Error("killed a pid that had clearly been recycled")
		}
	})

	t.Run("reaps when a live owner pid is too old to be real", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		// os.Getppid() is alive, standing in for a recycled owner pid; the
		// sidecar is backdated past staleAfter so the pid is disbelieved.
		writeSidecar(t, dir, os.Getppid(), pid, time.Now(), time.Time{})
		sidecar := filepath.Join(dir, probeDirName, strconv.Itoa(os.Getppid())+".pgids")
		old := time.Now().Add(-staleAfter - time.Minute)
		if err := os.Chtimes(sidecar, old, old); err != nil {
			t.Fatal(err)
		}

		if n := ReapStale(dir); n != 1 {
			t.Fatalf("ReapStale killed %d groups, want 1", n)
		}
		if !waitGone(t, pid, 2*time.Second) {
			t.Error("group behind a stale sidecar survived reaping")
		}
	})

	t.Run("reaps a dead predecessor whose pid we now carry", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		// The sidecar names our own pid, but was written by a run that started
		// long before us — the signature of a recycled pid. Trusting the pid
		// alone would skip this as "ours", leak the orphan permanently, and
		// then destroy the record when our own tracker closed.
		writeSidecar(t, dir, os.Getpid(), pid, time.Now(), time.Now().Add(-24*time.Hour))

		if n := ReapStale(dir); n != 1 {
			t.Fatalf("ReapStale killed %d groups, want 1", n)
		}
		if !waitGone(t, pid, 2*time.Second) {
			t.Error("predecessor's orphan survived; it would never be reaped again")
		}
	})

	t.Run("keeps our own sidecar when the start time matches", func(t *testing.T) {
		dir := t.TempDir()
		pid := startGroupLeader(t)
		writeSidecar(t, dir, os.Getpid(), pid, time.Now(), ownerStartOf(t, os.Getpid()))

		if n := ReapStale(dir); n != 0 {
			t.Fatalf("ReapStale killed %d groups, want 0", n)
		}
		if !Alive(pid) {
			t.Error("reaped a group belonging to the current run")
		}
	})

	t.Run("missing directory is not an error", func(t *testing.T) {
		if n := ReapStale(filepath.Join(t.TempDir(), "absent")); n != 0 {
			t.Errorf("ReapStale on a missing dir returned %d", n)
		}
	})
}

func TestReapStaleIgnoresMalformedRecords(t *testing.T) {
	dir := t.TempDir()
	probeDir := filepath.Join(dir, probeDirName)
	if err := os.MkdirAll(probeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Junk filenames and junk contents must not panic or kill anything.
	for name, body := range map[string]string{
		"notapid.pgids": "1234 5678\n",
		"999998.pgids":  "garbage\n\n-5 12345\n0 1\nalso bad\n",
		"999997.txt":    "1 2\n",
	} {
		if err := os.WriteFile(filepath.Join(probeDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n := ReapStale(dir); n != 0 {
		t.Errorf("ReapStale killed %d groups from malformed records", n)
	}
}

func TestTrackerFromContext(t *testing.T) {
	if got := TrackerFrom(context.Background()); got != nil {
		t.Errorf("TrackerFrom on a bare context = %v, want nil", got)
	}
	dir := t.TempDir()
	tr := NewTracker(dir)
	defer tr.Close()
	if got := TrackerFrom(WithTracker(context.Background(), tr)); got != tr {
		t.Error("TrackerFrom did not return the attached tracker")
	}
}

func TestStartTime(t *testing.T) {
	start, ok := StartTime(os.Getpid())
	if !ok {
		t.Fatal("StartTime failed for the current process")
	}
	if time.Since(start) < 0 || time.Since(start) > 24*time.Hour {
		t.Errorf("implausible start time %v for this test process", start)
	}
	if _, ok := StartTime(999999); ok {
		t.Error("StartTime reported success for a pid that does not exist")
	}
}

// writeSidecar fabricates a tracker sidecar as if owner had recorded pgid.
// ownerStart is the start time stamped for the owning run; the zero value
// omits the line, standing in for a run that could not read its own.
func writeSidecar(t *testing.T, dir string, owner, pgid int, at time.Time, ownerStart time.Time) {
	t.Helper()
	probeDir := filepath.Join(dir, probeDirName)
	if err := os.MkdirAll(probeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(probeDir, strconv.Itoa(owner)+".pgids")
	body := ""
	if !ownerStart.IsZero() {
		body += ownerField + " " + strconv.FormatInt(ownerStart.Unix(), 10) + "\n"
	}
	body += strconv.Itoa(pgid) + " " + strconv.FormatInt(at.Unix(), 10) + "\n"
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ownerStartOf returns a pid's real start time, for sidecars that should look
// like they belong to a genuinely running owner.
func ownerStartOf(t *testing.T, pid int) time.Time {
	t.Helper()
	start, ok := StartTime(pid)
	if !ok {
		t.Fatalf("could not read start time for pid %d", pid)
	}
	return start
}

// TestKillablePgid guards the blast radius of KillGroup/KillGracefully.
// kill(-1, sig) signals every process with this uid and kill(-0, sig) signals
// our own process group, so a pid record must never be able to reach either.
//
// This deliberately tests the predicate rather than calling KillGroup(1): a
// regression that removed the guard would otherwise make running the test
// suite kill the developer's entire session.
func TestKillablePgid(t *testing.T) {
	for _, pgid := range []int{-1, 0, 1} {
		if killablePgid(pgid) {
			t.Errorf("killablePgid(%d) = true; kill(-%d) would signal far more than one group", pgid, pgid)
		}
	}
	if !killablePgid(2) {
		t.Error("killablePgid(2) = false, want true")
	}
}

// TestReapStaleRefusesDangerousPgids ensures a corrupt sidecar naming pgid 0
// or 1 reaps nothing, rather than signalling our own group or the whole
// session.
func TestReapStaleRefusesDangerousPgids(t *testing.T) {
	dir := t.TempDir()
	probeDir := filepath.Join(dir, probeDirName)
	if err := os.MkdirAll(probeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "0 " + strconv.FormatInt(time.Now().Unix(), 10) + "\n" +
		"1 " + strconv.FormatInt(time.Now().Unix(), 10) + "\n" +
		"-1 " + strconv.FormatInt(time.Now().Unix(), 10) + "\n"
	if err := os.WriteFile(filepath.Join(probeDir, "999999.pgids"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := ReapStale(dir); n != 0 {
		t.Fatalf("ReapStale acted on %d dangerous pgid(s), want 0", n)
	}
}
