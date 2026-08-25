package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Orphan reaping closes the one gap a process cannot close for itself: on
// macOS there is no way to bind a child's lifetime to its parent (no
// PR_SET_PDEATHSIG), so a SIGKILL leaves no chance to clean up, and a child in
// its own process group also escapes a group kill aimed at the parent. Rather
// than trying to survive our own death, each run records the process groups it
// spawned in a sidecar file and the next run reaps whatever a dead predecessor
// left behind. Orphans are therefore bounded to a single generation instead of
// accumulating across polls.

const probeDirName = "probes"

// ownerField prefixes the sidecar line recording the owning run's start time.
const ownerField = "owner"

// staleAfter is how old a sidecar must be before its owner pid is disbelieved.
// Comfortably longer than any single run holds one open.
const staleAfter = 5 * time.Minute

// Tracker records the process groups spawned by the current run so that a
// later run can reap them if this one dies before cleaning up. It is safe for
// concurrent use.
type Tracker struct {
	path string

	mu     sync.Mutex
	f      *os.File
	closed bool
}

// NewTracker creates a sidecar file under dir keyed by this process's pid.
// A nil Tracker is usable and does nothing, so callers need not special-case
// a failure to create one.
func NewTracker(dir string) *Tracker {
	probeDir := filepath.Join(dir, probeDirName)
	if err := os.MkdirAll(probeDir, 0o700); err != nil {
		return nil
	}
	path := filepath.Join(probeDir, strconv.Itoa(os.Getpid())+".pgids")
	// Truncate: a file already at this name belongs to a dead predecessor
	// whose pid we now carry. ReapStale runs first and has already dealt with
	// its records, so starting clean is what we want.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil
	}
	// Stamp our own start time so a later run can tell this sidecar apart from
	// one left by a dead predecessor that happened to share our pid.
	if start, ok := StartTime(os.Getpid()); ok {
		_, _ = fmt.Fprintf(f, "%s %d\n", ownerField, start.Unix())
	}
	return &Tracker{path: path, f: f}
}

// Add records a process group id. Callers invoke it immediately after starting
// a child, which narrows — but cannot close — the window in which a kill would
// orphan a child that was never recorded.
func (t *Tracker) Add(pgid int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	// No Sync: os.File.Write reaches the kernel, so the record survives our
	// process being killed, which is the only case that matters. Only a
	// machine crash could lose it, and then the recorded pids are meaningless
	// anyway. fsync here costs milliseconds per probe on a polled command.
	_, _ = fmt.Fprintf(t.f, "%d %d\n", pgid, time.Now().Unix())
}

// Close removes the sidecar. Reaching it means this run cleaned up after
// itself, so there is nothing for a later run to reap.
func (t *Tracker) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	_ = t.f.Close()
	_ = os.Remove(t.path)
}

// ReapStale kills process groups recorded by runs that are no longer alive,
// and removes their sidecars. Sidecars belonging to a live process are left
// alone: that run is still using them. Returns the number of groups killed.
func ReapStale(dir string) int {
	probeDir := filepath.Join(dir, probeDirName)
	entries, err := os.ReadDir(probeDir)
	if err != nil {
		return 0
	}
	killed := 0
	for _, e := range entries {
		owner, ok := ownerPidFromName(e.Name())
		if !ok {
			continue
		}
		path := filepath.Join(probeDir, e.Name())
		// A run that is still going still owns its children — including this
		// one, whose own sidecar is skipped by the same check.
		if ownerIsLive(owner, path) {
			continue
		}
		for _, r := range readRecords(path) {
			if killGroupIfOurs(r.pgid, r.recordedAt) {
				killed++
			}
		}
		_ = os.Remove(path)
	}
	return killed
}

// ownerIsLive reports whether the run that wrote this sidecar is still going.
//
// Identity is (pid, start time), never the pid alone. A dead owner's pid can
// be recycled — including onto us — and treating that as "still running" would
// skip a predecessor's sidecar as though it were our own, leaking its orphans
// permanently and then destroying the record on Close.
//
// When the start time can't be established at either end, fall back to the
// sidecar's age: no single run holds one open for anywhere near staleAfter.
func ownerIsLive(owner int, path string) bool {
	if !Alive(owner) {
		return false
	}
	recorded, haveRecorded := readOwnerStart(path)
	start, haveStart := StartTime(owner)
	if !haveRecorded || !haveStart {
		return !olderThan(path, staleAfter)
	}
	diff := start.Sub(recorded)
	if diff < 0 {
		diff = -diff
	}
	return diff <= ownerStartTolerance
}

// ownerStartTolerance absorbs the second-granularity of the two clocks the
// owner's start time is read through.
const ownerStartTolerance = 2 * time.Second

// readOwnerStart returns the start time the owning run stamped into its
// sidecar.
func readOwnerStart(path string) (time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != ownerField {
			continue
		}
		sec, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(sec, 0), true
	}
	return time.Time{}, false
}

// olderThan reports whether the file was last written more than d ago.
func olderThan(path string, d time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > d
}

// ownerPidFromName parses "<pid>.pgids".
func ownerPidFromName(name string) (int, bool) {
	base, ok := strings.CutSuffix(name, ".pgids")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(base)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// record is one tracked process group and the moment it was recorded.
type record struct {
	pgid       int
	recordedAt time.Time
}

func readRecords(path string) []record {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var records []record
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pgid, err := strconv.Atoi(fields[0])
		// Reject 0 and 1 as well as junk: see killablePgid — their negations
		// mean "our own group" and "every process this user owns".
		if err != nil || pgid <= 1 {
			continue
		}
		sec, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		records = append(records, record{pgid: pgid, recordedAt: time.Unix(sec, 0)})
	}
	return records
}

// pidReuseSlack absorbs the second-granularity of ps lstart and the gap
// between spawning a child and recording it.
const pidReuseSlack = 5 * time.Second

// killGroupIfOurs SIGKILLs the process group led by pgid, but only once the
// pid is confirmed to be the process we recorded. Between recording and
// reaping the number may have been recycled onto something unrelated, and
// killing a whole group on a stale pid would be far worse than leaking one —
// so anything that started after we wrote the record, or that cannot be
// verified at all, is left alone.
func killGroupIfOurs(pgid int, recordedAt time.Time) bool {
	if !Alive(pgid) {
		return false
	}
	start, ok := StartTime(pgid)
	if !ok {
		return false
	}
	if start.After(recordedAt.Add(pidReuseSlack)) {
		return false
	}
	return KillGroup(pgid)
}

type trackerKey struct{}

// WithTracker attaches a Tracker to ctx so that process-spawning helpers
// deeper in the call stack can record what they start without every layer
// having to thread it through explicitly.
func WithTracker(ctx context.Context, t *Tracker) context.Context {
	return context.WithValue(ctx, trackerKey{}, t)
}

// TrackerFrom returns the Tracker attached to ctx, or nil.
func TrackerFrom(ctx context.Context) *Tracker {
	t, _ := ctx.Value(trackerKey{}).(*Tracker)
	return t
}
