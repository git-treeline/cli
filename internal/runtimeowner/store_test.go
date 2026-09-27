package runtimeowner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

func fixture(t *testing.T) (*Store, Binding, Owner) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gtl-runtime-receipts-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	b := Binding{
		Target:     registry.TargetIdentity{Path: "/fixture/worktree", Directory: registry.DirectoryIdentity{Dev: "1", Ino: "2"}, Common: &registry.CommonIdentity{Path: "/fixture/main/.git", DirectoryIdentity: registry.DirectoryIdentity{Dev: "1", Ino: "3"}}},
		Allocation: registry.AllocationRef{Kind: "allocated", Generation: uuid.NewString()},
		Generation: uuid.NewString(), ConfigDigest: strings.Repeat("a", 64), PostStopDigest: strings.Repeat("b", 64),
	}
	return New(filepath.Join(dir, "runtime-owners.json")), b, Owner{BootID: uuid.NewString(), PID: 101, BirthID: "1000:50"}
}

func mustPrepare(t *testing.T, s *Store, b Binding) Record {
	t.Helper()
	r, err := s.Prepare(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustOwn(t *testing.T, s *Store, b Binding, owner Owner) {
	t.Helper()
	mustPrepare(t, s, b)
	if _, err := s.RegisterOwner(context.Background(), b, owner); err != nil {
		t.Fatal(err)
	}
}

func mustStop(t *testing.T, s *Store, b Binding, owner Owner, operation string) {
	t.Helper()
	mustOwn(t, s, b, owner)
	if _, claimed, err := s.BeginStop(context.Background(), b, owner, operation); err != nil || !claimed {
		t.Fatalf("stop claimed=%v: %v", claimed, err)
	}
}

func TestRetainedCompletionAndLostReplies(t *testing.T) {
	s, b, owner := fixture(t)
	ctx := context.Background()
	first := mustPrepare(t, s, b)
	if first.State != Prepared || first.Owner != nil {
		t.Fatalf("prepared = %+v", first)
	}
	if duplicate := mustPrepare(t, New(s.Path), b); !reflect.DeepEqual(first, duplicate) {
		t.Fatal("prepare retry changed receipt")
	}
	if _, err := s.RegisterOwner(ctx, b, owner); err != nil {
		t.Fatal(err)
	}
	operation := uuid.NewString()
	if _, claimed, err := New(s.Path).BeginStop(ctx, b, owner, operation); err != nil || !claimed {
		t.Fatalf("claimed=%v: %v", claimed, err)
	}
	if r, claimed, err := New(s.Path).BeginStop(ctx, b, owner, operation); err != nil || claimed || r.State != Stopping {
		t.Fatalf("lost stop reply replay: %+v %v %v", r, claimed, err)
	}
	completed, err := New(s.Path).Complete(ctx, b, owner, operation)
	if err != nil || completed.State != Completed || completed.OperationID != operation || completed.Owner == nil || *completed.Owner != owner {
		t.Fatalf("completion %+v: %v", completed, err)
	}
	queried, err := New(s.Path).Query(ctx, b)
	if err != nil || !reflect.DeepEqual(queried, completed) {
		t.Fatalf("lost completion reply %+v: %v", queried, err)
	}
	if again, err := s.Complete(ctx, b, owner, operation); err != nil || !reflect.DeepEqual(again, completed) {
		t.Fatal("complete retry was not idempotent", err)
	}
	if _, claimed, err := s.BeginStop(ctx, b, owner, operation); err != nil || claimed {
		t.Fatal("completed stop replayed", err)
	}
	if _, _, err := s.BeginStop(ctx, b, owner, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-operation stop accepted", err)
	}
	if _, err := s.Complete(ctx, b, owner, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-operation completion accepted", err)
	}
	if got, err := s.Get(ctx, b.Generation); err != nil || !reflect.DeepEqual(got, completed) {
		t.Fatal("generation lookup failed", err)
	}
}

func TestMissingLegacyAndInterruptedRegistrationRemainUnknown(t *testing.T) {
	s, b, owner := fixture(t)
	ctx := context.Background()
	for _, legacy := range []string{"old.pid", "old.sock.post-stop"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(s.Path), legacy), []byte("101"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Query(ctx, b); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, b.Generation); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := s.RegisterOwner(ctx, b, owner); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	mustPrepare(t, s, b)
	if r, err := New(s.Path).Query(ctx, b); err != nil || r.State != Prepared || r.Owner != nil {
		t.Fatalf("interrupted handoff became owned: %+v %v", r, err)
	}
	if _, claimed, err := s.BeginStop(ctx, b, owner, uuid.NewString()); !errors.Is(err, ErrConflict) || claimed {
		t.Fatal("prepared allowed stop", err)
	}
	if _, err := s.Complete(ctx, b, owner, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("prepared allowed complete", err)
	}
}

func TestExactBindingAndOwnerRequired(t *testing.T) {
	s, b, owner := fixture(t)
	mustOwn(t, s, b, owner)
	for name, mutate := range map[string]func(*Binding){
		"config":              func(v *Binding) { v.ConfigDigest = strings.Repeat("c", 64) },
		"post-stop selection": func(v *Binding) { v.PostStopDigest = strings.Repeat("d", 64) },
		"allocation":          func(v *Binding) { v.Allocation.Generation = uuid.NewString() },
		"path":                func(v *Binding) { v.Target.Path = "/fixture/replacement" },
		"inode":               func(v *Binding) { v.Target.Directory.Ino = "9" },
		"common directory":    func(v *Binding) { common := *v.Target.Common; common.Ino = "9"; v.Target.Common = &common },
	} {
		t.Run(name, func(t *testing.T) {
			other := b
			mutate(&other)
			if _, err := s.Query(context.Background(), other); !errors.Is(err, ErrConflict) {
				t.Fatal("changed binding accepted", err)
			}
			if _, err := s.Prepare(context.Background(), other); !errors.Is(err, ErrConflict) {
				t.Fatal("changed prepare accepted", err)
			}
		})
	}
	for name, mutate := range map[string]func(*Owner){
		"boot":  func(v *Owner) { v.BootID = uuid.NewString() },
		"PID":   func(v *Owner) { v.PID++ },
		"birth": func(v *Owner) { v.BirthID = "1000:51" },
	} {
		t.Run(name, func(t *testing.T) {
			other := owner
			mutate(&other)
			if _, err := s.RegisterOwner(context.Background(), b, other); !errors.Is(err, ErrConflict) {
				t.Fatal("owner replaced", err)
			}
			if _, claimed, err := s.BeginStop(context.Background(), b, other, uuid.NewString()); !errors.Is(err, ErrConflict) || claimed {
				t.Fatal("wrong owner claimed stop", err)
			}
		})
	}
	other := b
	other.Generation = uuid.NewString()
	if _, err := s.Prepare(context.Background(), other); !errors.Is(err, ErrConflict) {
		t.Fatal("unresolved generation replaced", err)
	}
}

func TestUncertainNeverReplays(t *testing.T) {
	for _, state := range []State{Prepared, Owned, Stopping} {
		t.Run(string(state), func(t *testing.T) {
			s, b, owner := fixture(t)
			var expected *Owner
			operation := ""
			mustPrepare(t, s, b)
			if state != Prepared {
				mustOwn(t, s, b, owner)
				expected = &owner
			}
			if state == Stopping {
				operation = uuid.NewString()
				mustStop(t, s, b, owner, operation)
			}
			r, err := s.MarkUncertain(context.Background(), b, expected, operation, "stop_interrupted")
			if err != nil || r.State != Uncertain {
				t.Fatal(err)
			}
			if _, err := s.RegisterOwner(context.Background(), b, owner); !errors.Is(err, ErrUncertain) {
				t.Fatal("uncertain owner replay", err)
			}
			if _, claimed, err := s.BeginStop(context.Background(), b, owner, uuid.NewString()); !errors.Is(err, ErrUncertain) || claimed {
				t.Fatal("uncertain stop replay", err)
			}
			if _, err := s.Complete(context.Background(), b, owner, uuid.NewString()); !errors.Is(err, ErrUncertain) {
				t.Fatal("uncertain completed", err)
			}
			if got, err := s.MarkUncertain(context.Background(), b, expected, operation, "stop_interrupted"); err != nil || !reflect.DeepEqual(got, r) {
				t.Fatal("uncertain retry changed", err)
			}
		})
	}
}

func TestOneConcurrentStopClaimAndNoLostRecords(t *testing.T) {
	s, b, owner := fixture(t)
	mustOwn(t, s, b, owner)
	operation := uuid.NewString()
	var claims atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			_, claim, err := New(s.Path).BeginStop(context.Background(), b, owner, operation)
			if err != nil {
				t.Error(err)
			}
			if claim {
				claims.Add(1)
			}
			other := b
			other.Generation, other.Allocation.Generation = uuid.NewString(), uuid.NewString()
			other.Target.Path = fmt.Sprintf("/fixture/worktree-%d", i)
			other.Target.Directory.Ino = fmt.Sprint(i + 10)
			if _, err := New(s.Path).Prepare(context.Background(), other); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("stop claims = %d", claims.Load())
	}
	data, err := s.read()
	if err != nil || len(data.Records) != 13 {
		t.Fatalf("retained %d: %v", len(data.Records), err)
	}
}

func TestDurabilityFailureCannotAcknowledgeOrGrantReplay(t *testing.T) {
	s, b, owner := fixture(t)
	mustOwn(t, s, b, owner)
	operation := uuid.NewString()
	failure := errors.New("injected directory sync failure")
	s.syncDir = func(string) error { return failure }
	if _, claimed, err := s.BeginStop(context.Background(), b, owner, operation); !errors.Is(err, failure) || claimed {
		t.Fatalf("unflushed claim %v: %v", claimed, err)
	}
	recovered := New(s.Path)
	if r, claimed, err := recovered.BeginStop(context.Background(), b, owner, operation); err != nil || claimed || r.State != Stopping {
		t.Fatalf("uncertain claim replayed: %+v %v %v", r, claimed, err)
	}
	if _, err := s.Complete(context.Background(), b, owner, operation); !errors.Is(err, failure) {
		t.Fatal("unflushed complete acknowledged", err)
	}
	var synchronized bool
	recovered.syncDir = func(path string) error {
		data, err := recovered.read()
		if err != nil || data.Records[0].State != Completed {
			t.Fatal("sync ran before completed write", err)
		}
		synchronized = true
		return syncDirectory(path)
	}
	if r, err := recovered.Query(context.Background(), b); err != nil || r.State != Completed || !synchronized {
		t.Fatalf("lost completion recovery %+v %v", r, err)
	}
}

func TestPrivateFilesAndUnsafeFilesRejected(t *testing.T) {
	s, b, _ := fixture(t)
	mustPrepare(t, s, b)
	for _, path := range []string{s.Path, s.Path + ".lock"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("not private", path, err)
		}
	}
	if err := os.Chmod(s.Path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(context.Background(), b); err == nil {
		t.Fatal("insecure receipt accepted")
	}
	if err := os.Remove(s.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(s.Path+".lock", s.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(context.Background(), b); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(s.Path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(s.Path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(context.Background(), b); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestCorruptAndNewerReceiptsFailClosed(t *testing.T) {
	s, b, _ := fixture(t)
	mustPrepare(t, s, b)
	original, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"schema":           strings.Replace(string(original), `"schema":1`, `"schema":2`, 1),
		"unknown":          strings.Replace(string(original), `"schema":1`, `"schema":1,"environment":"secret"`, 1),
		"duplicate":        strings.Replace(string(original), `"schema":1`, `"schema":2,"schema":1`, 1),
		"nested duplicate": strings.Replace(string(original), `"state":"prepared"`, `"state":"completed","state":"prepared"`, 1),
		"missing state":    strings.Replace(string(original), `"state":"prepared",`, "", 1),
		"trailing":         string(original) + `{}`,
		"null":             `null`,
		"no array":         `{"schema":1}`,
		"bad time":         strings.Replace(string(original), `"created_at":"`, `"created_at":"bad`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(s.Path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Query(context.Background(), b); err == nil {
				t.Fatal("corrupt receipt accepted")
			}
			if _, err := s.Prepare(context.Background(), b); err == nil {
				t.Fatal("corrupt receipt overwritten")
			}
			after, err := os.ReadFile(s.Path)
			if err != nil || string(after) != raw {
				t.Fatal("corrupt store changed", err)
			}
		})
	}
}

func TestRecordAndRetentionBounds(t *testing.T) {
	s, b, owner := fixture(t)
	mustStop(t, s, b, owner, uuid.NewString())
	data, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	data.Records[0].State, data.Records[0].Code = Completed, "ok"
	for i := 1; i < MaxRecords; i++ {
		r := data.Records[0]
		r.Generation = uuid.NewString()
		r.OperationID = uuid.NewString()
		data.Records = append(data.Records, r)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	other := b
	other.Generation = uuid.NewString()
	if _, err := s.Prepare(context.Background(), other); !errors.Is(err, ErrCapacity) {
		t.Fatal("full store accepted new work", err)
	}
	if _, err := s.Query(context.Background(), b); err != nil {
		t.Fatal("full store cannot query", err)
	}
	data.Records = data.Records[:1]
	raw, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	padded := strings.Replace(string(raw), `"target":`, strings.Repeat(" ", MaxRecordBytes)+`"target":`, 1)
	if err := os.WriteFile(s.Path, []byte(padded), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(context.Background(), b); !errors.Is(err, ErrCapacity) {
		t.Fatal("oversized individual record accepted", err)
	}
}

func TestInvalidEvidenceAndBoundedCodes(t *testing.T) {
	s, b, owner := fixture(t)
	mustPrepare(t, s, b)
	for _, birth := range []string{"", "0", "01", "1:01", "1:1000000", "-1", "secret env", "1:2:3"} {
		bad := owner
		bad.BirthID = birth
		if _, err := s.RegisterOwner(context.Background(), b, bad); !errors.Is(err, ErrConflict) {
			t.Fatal("bad birth accepted", birth, err)
		}
	}
	for _, boot := range []string{"", "boot-string", strings.ToUpper(owner.BootID), uuid.Nil.String()} {
		bad := owner
		bad.BootID = boot
		if _, err := s.RegisterOwner(context.Background(), b, bad); !errors.Is(err, ErrConflict) {
			t.Fatal("bad boot accepted", boot, err)
		}
	}
	if _, err := s.MarkUncertain(context.Background(), b, nil, "", "hook stderr secret"); !errors.Is(err, ErrConflict) {
		t.Fatal("unbounded code accepted", err)
	}
	for _, birth := range []string{"1", "1000:0", "1000:999999"} {
		good := owner
		good.BirthID = birth
		if !validOwner(good) {
			t.Fatal("valid birth rejected", birth)
		}
	}
}

func TestLockWaitHonorsCancellation(t *testing.T) {
	s, b, _ := fixture(t)
	mustPrepare(t, s, b)
	lock, err := supervisor.OpenStateLock(s.Path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Query(ctx, b); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock wait ignored cancellation", err)
	}
}
