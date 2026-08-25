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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	return &Tracker{path: path, f: f}
}

// Add records a process group id. Called right after a child starts, so the
// record exists before the child can be orphaned.
func (t *Tracker) Add(pgid int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	_, _ = fmt.Fprintf(t.f, "%d %d\n", pgid, time.Now().Unix())
	_ = t.f.Sync()
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
		if owner == os.Getpid() {
			continue
		}
		// A run that is still going still owns its children. The age check
		// covers the case where the dead owner's pid has since been recycled
		// onto an unrelated live process: no single run holds a sidecar for
		// anywhere near staleAfter, so an old one is stale whatever its pid
		// now points at.
		if Alive(owner) && !olderThan(filepath.Join(probeDir, e.Name()), staleAfter) {
			continue
		}
		path := filepath.Join(probeDir, e.Name())
		for _, r := range readRecords(path) {
			if killGroupIfOurs(r.pgid, r.recordedAt) {
				killed++
			}
		}
		_ = os.Remove(path)
	}
	return killed
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
		if err != nil || pgid <= 0 {
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
