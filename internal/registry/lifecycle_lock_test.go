package registry

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func lifecycleFixture(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, err := os.MkdirTemp("/tmp", "gtl-lifecycle-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root, err := CanonicalLifecycleTarget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func acquireForTest(t *testing.T, dir string, paths ...string) *LifecycleLocks {
	t.Helper()
	locks, err := acquireLifecycleLocks(context.Background(), paths, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := locks.Close(); err != nil {
			t.Error(err)
		}
	})
	return locks
}

func TestLifecycleCanonicalTargets(t *testing.T) {
	root, _ := lifecycleFixture(t)
	directory := filepath.Join(root, "directory")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"", "missing", "missing/child"} {
		actual, err := CanonicalLifecycleTarget(filepath.Join(alias, tail))
		if err != nil || actual != filepath.Join(directory, tail) {
			t.Fatalf("alias %q: %q, %v", tail, actual, err)
		}
	}
	t.Chdir(root)
	if actual, err := CanonicalLifecycleTarget("alias/missing"); err != nil || actual != filepath.Join(directory, "missing") {
		t.Fatalf("relative alias: %q, %v", actual, err)
	}
	nested := filepath.Join(directory, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nested, filepath.Join(root, "deep-alias")); err != nil {
		t.Fatal(err)
	}
	if actual, err := CanonicalLifecycleTarget("deep-alias/../missing"); err != nil || actual != filepath.Join(directory, "missing") {
		t.Fatalf("parent traversal resolved before alias: %q, %v", actual, err)
	}
	if err := os.Symlink(filepath.Join(root, "absent"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "bad\x00path", string([]byte{0xff}), strings.Repeat("a", 4097), filepath.Join(root, "broken"), filepath.Join(root, "broken/child"), "broken/../directory", filepath.Join(root, "file"), filepath.Join(root, "file/child"), filepath.Join(root, "missing", strings.Repeat("a", 256))} {
		if _, err := CanonicalLifecycleTarget(target); err == nil {
			t.Fatalf("accepted malformed/non-directory target %q", target)
		}
	}
	if os.Geteuid() != 0 {
		private := filepath.Join(root, "unreadable")
		if err := os.Mkdir(private, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(private, 0700) })
		if _, err := CanonicalLifecycleTarget(filepath.Join(private, "child")); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("permission failure treated as missing: %v", err)
		}
	}
}

func TestLifecycleLockSetAndIndependentTargets(t *testing.T) {
	root, dir := lifecycleFixture(t)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	locks := acquireForTest(t, dir, b, filepath.Join(alias, "a"), a)
	if !slices.Equal(locks.Paths(), []string{a, b}) {
		t.Fatalf("not sorted/deduplicated: %v", locks.Paths())
	}
	copy := locks.Paths()
	copy[0] = "changed"
	if locks.Paths()[0] != a {
		t.Fatal("mutable lock keys escaped")
	}
	acquireForTest(t, dir, filepath.Join(root, "independent"))
	lockFile := lifecycleLockPath(dir, a)
	before, err := os.Stat(lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := locks.Close(); err != nil {
		t.Fatal(err)
	}
	if err := locks.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lockFile)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("close replaced or removed lock file")
	}
	acquireForTest(t, dir, a)
	for _, targets := range [][]string{nil, {""}, make([]string, maxReleaseOperations+1)} {
		if got, err := acquireLifecycleLocks(context.Background(), targets, dir); err == nil || got != nil {
			t.Fatalf("invalid lock set accepted: %v", err)
		}
	}
}

func TestLifecycleLockChild(t *testing.T) {
	if os.Getenv("GTL_LIFECYCLE_TEST_CHILD") != "1" {
		return
	}
	locks, err := acquireLifecycleLocks(context.Background(), []string{os.Getenv("GTL_LIFECYCLE_TEST_TARGET")}, os.Getenv("GTL_LIFECYCLE_TEST_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("LOCKED")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	if err := locks.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleTwoProcessContention(t *testing.T) {
	root, dir := lifecycleFixture(t)
	target := filepath.Join(root, "target")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifecycleLockChild$")
	child.Env = append(os.Environ(), "GTL_LIFECYCLE_TEST_CHILD=1", "GTL_LIFECYCLE_TEST_TARGET="+target, "GTL_LIFECYCLE_TEST_DIR="+dir)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = input.Close()
		if !waited {
			cancel()
			_ = child.Wait()
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "LOCKED\n" {
		t.Fatalf("child failed: %q, %v: %s", line, err, stderr.String())
	}
	contended, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if locks, err := acquireLifecycleLocks(contended, []string{target}, dir); !errors.Is(err, context.DeadlineExceeded) || locks != nil {
		t.Fatalf("second process entered: %v", err)
	}
	acquireForTest(t, dir, filepath.Join(root, "other"))
	if _, err := fmt.Fprintln(input); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	waited = true
	if err != nil {
		t.Fatalf("child exit: %v: %s", err, stderr.String())
	}
	acquireForTest(t, dir, target)
}

func TestLifecycleCancellationAndPartialFailureRelease(t *testing.T) {
	root, dir := lifecycleFixture(t)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	blocker := acquireForTest(t, dir, b)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if locks, err := acquireLifecycleLocks(ctx, []string{b, a}, dir); !errors.Is(err, context.DeadlineExceeded) || locks != nil {
		t.Fatalf("partial cancellation: %v", err)
	}
	first := acquireForTest(t, dir, a)
	_ = first.Close()
	_ = blocker.Close()
	c, d := filepath.Join(root, "c"), filepath.Join(root, "d")
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, lifecycleLockPath(dir, d)); err != nil {
		t.Fatal(err)
	}
	if locks, err := acquireLifecycleLocks(context.Background(), []string{c, d}, dir); err == nil || locks != nil {
		t.Fatal("symlink lock accepted")
	}
	acquireForTest(t, dir, c)
	if raw, _ := os.ReadFile(sentinel); string(raw) != "untouched" {
		t.Fatal("followed lock symlink")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if locks, err := AcquireLifecycleLocks(canceled, []string{a}); !errors.Is(err, context.Canceled) || locks != nil {
		t.Fatalf("pre-canceled acquisition: %v", err)
	}
	if locks, err := AcquireLifecycleLocks(nil, []string{a}); err == nil || locks != nil {
		t.Fatal("nil context accepted")
	}
}

func TestLifecycleAliasChangeWhileWaiting(t *testing.T) {
	root, dir := lifecycleFixture(t)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, p := range []string{a, b} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	blocker := acquireForTest(t, dir, b)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		locks, err := acquireLifecycleLocks(ctx, []string{alias, b}, dir)
		if locks != nil {
			_ = locks.Close()
		}
		result <- err
	}()
	// Observe acquisition of the first key rather than guessing scheduling with
	// a sleep. The second key remains held until the alias has been replaced.
	for {
		file, err := os.OpenFile(lifecycleLockPath(dir, a), os.O_RDWR, 0)
		if err == nil {
			err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			}
			_ = file.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				break
			}
		}
		select {
		case err := <-result:
			t.Fatalf("acquisition ended before alias change: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	// Changing to another key in the acquired set must still be rejected.
	if err := os.Symlink(b, alias); err != nil {
		t.Fatal(err)
	}
	_ = blocker.Close()
	if err := <-result; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed alias accepted: %v", err)
	}
	acquireForTest(t, dir, a, b)
}

func TestLifecycleReservationReadOnlyAndIdentity(t *testing.T) {
	reg, target, _ := releaseRegistryFixture(t)
	before, _ := os.ReadFile(reg.Path)
	if err := reg.CheckReleaseReservations([]string{target.Path}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(reg.Path); !bytes.Equal(before, after) {
		t.Fatal("reservation check rewrote legacy registry")
	}
	reg, request := enrolledRequest(t)
	if _, _, err := reg.PrepareRelease(request); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(request.Target.Path, alias); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(reg.Path)
	for _, p := range []string{request.Target.Path, alias} {
		if err := reg.CheckReleaseReservations([]string{p}); err == nil || !strings.Contains(err.Error(), request.OperationID) {
			t.Fatalf("reserved target admitted: %v", err)
		}
	}
	if err := reg.CheckReleaseReservations([]string{t.TempDir()}); err != nil {
		t.Fatalf("unrelated target blocked: %v", err)
	}
	renamed := filepath.Join(t.TempDir(), "renamed")
	if err := os.Rename(request.Target.Path, renamed); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{request.Target.Path, renamed} {
		if err := reg.CheckReleaseReservations([]string{p}); err == nil || !strings.Contains(err.Error(), request.OperationID) {
			t.Fatalf("missing path/renamed identity admitted: %v", err)
		}
	}
	if err := os.Mkdir(request.Target.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := reg.CheckReleaseReservations([]string{request.Target.Path}); err == nil {
		t.Fatal("replaced target admitted")
	}
	if after, _ := os.ReadFile(reg.Path); !bytes.Equal(before, after) {
		t.Fatal("refusal rewrote release evidence")
	}
	for _, raw := range []string{`{}`, `null`, `{"version":99,"allocations":[]}`, `{broken`} {
		if err := os.WriteFile(reg.Path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := reg.CheckReleaseReservations([]string{renamed}); err == nil {
			t.Fatalf("invalid registry admitted: %s", raw)
		}
		if after, _ := os.ReadFile(reg.Path); string(after) != raw {
			t.Fatal("invalid registry rewritten")
		}
	}
}

func TestLifecycleReservationOutcomesAndRegistryLockIndependence(t *testing.T) {
	reg, request := enrolledRequest(t)
	_, dir := lifecycleFixture(t)
	registryLock, err := os.OpenFile(reg.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer registryLock.Close()
	if err := syscall.Flock(int(registryLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	locks := acquireForTest(t, dir, request.Target.Path)
	if err := reg.CheckReleaseReservations(locks.Paths()); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(registryLock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.PrepareRelease(request); err != nil {
		t.Fatal(err)
	}
	for _, phase := range requiredPhases(request) {
		token := begin(t, reg, request.OperationID, phase)
		if err := reg.CheckReleaseReservations(locks.Paths()); err == nil {
			t.Fatal("started or pending release admitted")
		}
		if phase == PhaseAllocation {
			_, err = reg.RetireReleaseAllocation(token)
		} else {
			_, err = reg.RecordReleasePhase(token, PhaseResult{"completed", "ok"})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.CheckReleaseReservations(locks.Paths()); err != nil {
		t.Fatalf("completed operation still reserves: %v", err)
	}
	reg, request = enrolledRequest(t)
	if _, _, err := reg.PrepareRelease(request); err != nil {
		t.Fatal(err)
	}
	token := begin(t, reg, request.OperationID, PhasePreHook)
	if _, err := reg.RecordReleasePhase(token, PhaseResult{"uncertain", "unknown"}); err != nil {
		t.Fatal(err)
	}
	if err := New(reg.Path).CheckReleaseReservations([]string{request.Target.Path}); err == nil {
		t.Fatal("uncertain release admitted after reopen")
	}
}

func TestLifecycleRetainDestinationThroughForeground(t *testing.T) {
	root, dir := lifecycleFixture(t)
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	locks := acquireForTest(t, dir, source, target)
	for _, keys := range [][]string{nil, {filepath.Join(root, "unheld")}, {target, filepath.Join(root, "unheld")}} {
		if err := locks.Retain(keys); err == nil {
			t.Fatal("invalid retained set accepted")
		}
		if !slices.Equal(locks.Paths(), []string{source, target}) {
			t.Fatal("invalid retain changed held set")
		}
	}
	if err := locks.Retain([]string{target}); err != nil {
		t.Fatal(err)
	}
	if err := locks.Retain([]string{target, target}); err != nil {
		t.Fatal(err)
	}
	foreground := func() {
		// A sibling operation can acquire the source after setup, but release of
		// this destination stays excluded throughout the foreground callback.
		other := acquireForTest(t, dir, source)
		_ = other.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		if got, err := acquireLifecycleLocks(ctx, []string{target}, dir); !errors.Is(err, context.DeadlineExceeded) || got != nil {
			t.Fatalf("foreground destination lost exclusion: %v", err)
		}
	}
	foreground()
	if err := locks.Close(); err != nil {
		t.Fatal(err)
	}
	acquireForTest(t, dir, target)
	if err := locks.Retain([]string{target}); err == nil {
		t.Fatal("retain after close accepted")
	}
	primary := acquireForTest(t, dir, source, source)
	if err := primary.Retain([]string{source}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if got, err := acquireLifecycleLocks(ctx, []string{source}, dir); !errors.Is(err, context.DeadlineExceeded) || got != nil {
		t.Fatalf("source==target released its only lock: %v", err)
	}
}
