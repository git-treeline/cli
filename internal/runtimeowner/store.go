// Package runtimeowner retains exact runtime ownership and shutdown outcomes.
// Process evidence is supplied by the caller; this package never probes or
// signals a process and absence never establishes that a runtime never started.
package runtimeowner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

const (
	MaxRecords     = 1024
	MaxRecordBytes = 64 * 1024
	maxStoreBytes  = MaxRecords*MaxRecordBytes + 64*1024
)

var (
	ErrUnknown   = errors.New("runtime ownership is unknown")
	ErrConflict  = errors.New("runtime identity or transition conflict")
	ErrUncertain = errors.New("runtime outcome is uncertain; replay refused")
	ErrCapacity  = errors.New("runtime receipt capacity exhausted")
)

type State string

const (
	Prepared  State = "prepared"
	Owned     State = "owned"
	Stopping  State = "stopping"
	Completed State = "completed"
	Uncertain State = "uncertain"
)

type Binding struct {
	Target         registry.TargetIdentity `json:"target"`
	Allocation     registry.AllocationRef  `json:"allocation"`
	Generation     string                  `json:"generation"`
	ConfigDigest   string                  `json:"config_digest"`
	PostStopDigest string                  `json:"post_stop_digest"`
}

// Owner must come from verified kernel evidence, never a PID file or census
// guess. BirthID is positive Linux start ticks or Darwin seconds:microseconds.
type Owner struct {
	BootID  string `json:"boot_id"`
	PID     int    `json:"pid"`
	BirthID string `json:"birth_id"`
}

type Record struct {
	Binding
	Owner       *Owner `json:"owner"`
	OperationID string `json:"operation_id,omitempty"`
	State       State  `json:"state"`
	Code        string `json:"code,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type envelope struct {
	Schema  int      `json:"schema"`
	Records []Record `json:"records"`
}

// Store uses a private supervisor state directory (a direct child of /tmp).
// There is deliberately no eviction or adoption of older runtime state.
type Store struct {
	Path    string
	syncDir func(string) error
}

func New(path string) *Store { return &Store{Path: path} }

func (s *Store) Prepare(ctx context.Context, binding Binding) (Record, error) {
	return s.transaction(ctx, binding, true, func(records *[]Record, index int) (Record, bool, error) {
		if index >= 0 {
			return (*records)[index], false, nil
		}
		if len(*records) == MaxRecords {
			return Record{}, false, ErrCapacity
		}
		for _, other := range *records {
			if other.State != Completed && (other.Target.Path == binding.Target.Path || other.Target.Directory == binding.Target.Directory || other.Allocation == binding.Allocation) {
				return Record{}, false, ErrConflict
			}
		}
		now := timestamp()
		r := Record{Binding: binding, State: Prepared, CreatedAt: now, UpdatedAt: now}
		*records = append(*records, r)
		return r, true, nil
	})
}

func (s *Store) Query(ctx context.Context, binding Binding) (Record, error) {
	return s.transaction(ctx, binding, false, func(records *[]Record, index int) (Record, bool, error) {
		if index < 0 {
			return Record{}, false, ErrUnknown
		}
		return (*records)[index], false, nil
	})
}

// Get retrieves retained facts by generation without consulting current
// configuration. Callers must compare the target and allocation before using
// the returned binding to request an exact transition.
func (s *Store) Get(ctx context.Context, generation string) (Record, error) {
	return s.transact(ctx, nil, generation, false, func(records *[]Record, index int) (Record, bool, error) {
		if index < 0 {
			return Record{}, false, ErrUnknown
		}
		return (*records)[index], false, nil
	})
}

func (s *Store) RegisterOwner(ctx context.Context, binding Binding, owner Owner) (Record, error) {
	if !validOwner(owner) {
		return Record{}, ErrConflict
	}
	return s.change(ctx, binding, func(r *Record) (bool, error) {
		if r.State == Uncertain {
			return false, ErrUncertain
		}
		if r.Owner != nil {
			if *r.Owner == owner {
				return false, nil
			}
			return false, ErrConflict
		}
		if r.State != Prepared {
			return false, ErrConflict
		}
		r.Owner, r.State = &owner, Owned
		return true, nil
	})
}

// BeginStop grants execution only when claimed is true. A lost acknowledgement
// leaves stopping queryable but cannot grant a second execution of hooks.
func (s *Store) BeginStop(ctx context.Context, binding Binding, owner Owner, operationID string) (record Record, claimed bool, err error) {
	if !validOwner(owner) || !validUUID(operationID) {
		return Record{}, false, ErrConflict
	}
	record, err = s.change(ctx, binding, func(r *Record) (bool, error) {
		if r.State == Uncertain {
			return false, ErrUncertain
		}
		if r.Owner == nil || *r.Owner != owner {
			return false, ErrConflict
		}
		if r.State == Stopping || r.State == Completed {
			if r.OperationID != operationID {
				return false, ErrConflict
			}
			return false, nil
		}
		if r.State != Owned {
			return false, ErrConflict
		}
		r.OperationID, r.State, claimed = operationID, Stopping, true
		return true, nil
	})
	return record, claimed && err == nil, err
}

// Complete is called only after the verified owner's child and captured
// post-stop plan have settled. Success is synchronized before it is returned.
func (s *Store) Complete(ctx context.Context, binding Binding, owner Owner, operationID string) (Record, error) {
	if !validOwner(owner) || !validUUID(operationID) {
		return Record{}, ErrConflict
	}
	return s.change(ctx, binding, func(r *Record) (bool, error) {
		if r.State == Uncertain {
			return false, ErrUncertain
		}
		if r.Owner == nil || *r.Owner != owner || r.OperationID != operationID {
			return false, ErrConflict
		}
		if r.State == Completed {
			return false, nil
		}
		if r.State != Stopping {
			return false, ErrConflict
		}
		r.State, r.Code = Completed, "ok"
		return true, nil
	})
}

func (s *Store) MarkUncertain(ctx context.Context, binding Binding, owner *Owner, operationID, code string) (Record, error) {
	if !uncertainCode(code) {
		return Record{}, ErrConflict
	}
	return s.change(ctx, binding, func(r *Record) (bool, error) {
		if !reflect.DeepEqual(r.Owner, owner) || r.OperationID != operationID {
			return false, ErrConflict
		}
		if r.State == Uncertain {
			if r.Code == code {
				return false, nil
			}
			return false, ErrConflict
		}
		if r.State == Completed {
			return false, ErrConflict
		}
		r.State, r.Code = Uncertain, code
		return true, nil
	})
}

func (s *Store) change(ctx context.Context, binding Binding, fn func(*Record) (bool, error)) (Record, error) {
	return s.transaction(ctx, binding, false, func(records *[]Record, index int) (Record, bool, error) {
		if index < 0 {
			return Record{}, false, ErrUnknown
		}
		r := &(*records)[index]
		changed, err := fn(r)
		if changed {
			r.UpdatedAt = timestamp()
		}
		return *r, changed, err
	})
}

func (s *Store) transaction(ctx context.Context, binding Binding, create bool, fn func(*[]Record, int) (Record, bool, error)) (Record, error) {
	return s.transact(ctx, &binding, binding.Generation, create, fn)
}

func (s *Store) transact(ctx context.Context, binding *Binding, generation string, create bool, fn func(*[]Record, int) (Record, bool, error)) (record Record, resultErr error) {
	if ctx == nil || !validUUID(generation) || binding != nil && !validBinding(*binding) || !filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != s.Path {
		return Record{}, ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	socket := filepath.Join(filepath.Dir(s.Path), "state.sock")
	if create {
		if err := supervisor.EnsureSocketDir(socket); err != nil {
			return Record{}, err
		}
		if err := syncDirectory(filepath.Dir(filepath.Dir(s.Path))); err != nil {
			return Record{}, err
		}
	} else if err := supervisor.ValidateSocketDir(socket); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, ErrUnknown
		}
		return Record{}, err
	}
	if !create {
		if _, err := os.Lstat(s.Path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Record{}, ErrUnknown
			}
			return Record{}, err
		}
	}
	lock, err := supervisor.OpenStateLock(s.Path + ".lock")
	if err != nil {
		return Record{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	for {
		if err := ctx.Err(); err != nil {
			return Record{}, err
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return Record{}, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Record{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer func() { resultErr = errors.Join(resultErr, syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)) }()
	data, err := s.read()
	if err != nil {
		return Record{}, err
	}
	index := -1
	for i, r := range data.Records {
		if r.Generation == generation {
			if binding != nil && !reflect.DeepEqual(r.Binding, *binding) {
				return Record{}, ErrConflict
			}
			index = i
		}
	}
	record, changed, err := fn(&data.Records, index)
	if err != nil {
		return Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if changed {
		if err := validateEnvelope(data); err != nil {
			return Record{}, err
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return Record{}, err
		}
		if err := supervisor.WriteStateFile(s.Path, raw); err != nil {
			return Record{}, err
		}
	}
	// AtomicWriteFile synchronizes the new inode. Synchronizing the containing
	// directory makes the rename durable, including on an exact retry/query
	// after a previous caller lost its reply or observed a directory-sync error.
	syncDir := s.syncDir
	if syncDir == nil {
		syncDir = syncDirectory
	}
	if err := syncDir(filepath.Dir(s.Path)); err != nil {
		return Record{}, err
	}
	return record, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (s *Store) read() (envelope, error) {
	file, err := os.OpenFile(s.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return envelope{Schema: 1, Records: []Record{}}, nil
	}
	if err != nil {
		return envelope{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return envelope{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ok || int(stat.Uid) != os.Geteuid() {
		return envelope{}, fmt.Errorf("unsafe runtime receipt file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxStoreBytes+1))
	if err != nil {
		return envelope{}, err
	}
	if len(raw) > maxStoreBytes {
		return envelope{}, ErrCapacity
	}
	if err := uniqueJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return envelope{}, err
	}
	var bounds struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &bounds); err != nil {
		return envelope{}, fmt.Errorf("invalid runtime receipts")
	}
	if len(bounds.Records) > MaxRecords {
		return envelope{}, ErrCapacity
	}
	for _, record := range bounds.Records {
		if len(record) > MaxRecordBytes {
			return envelope{}, ErrCapacity
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var data envelope
	if err := decoder.Decode(&data); err != nil {
		return envelope{}, fmt.Errorf("invalid runtime receipts")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return envelope{}, fmt.Errorf("trailing runtime receipt data")
	}
	if err := validateEnvelope(data); err != nil {
		return envelope{}, err
	}
	return data, nil
}

func uniqueJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid runtime receipt JSON")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("invalid runtime receipt JSON")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate runtime receipt field")
			}
			seen[name] = true
		}
		if err := uniqueJSON(decoder); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("invalid runtime receipt JSON")
	}
	return nil
}

func validateEnvelope(data envelope) error {
	if data.Schema != 1 || data.Records == nil {
		return fmt.Errorf("unsupported or invalid runtime receipt schema")
	}
	if len(data.Records) > MaxRecords {
		return ErrCapacity
	}
	seen := map[string]bool{}
	operations := map[string]bool{}
	activePaths := map[string]bool{}
	activeDirectories := map[registry.DirectoryIdentity]bool{}
	activeAllocations := map[registry.AllocationRef]bool{}
	for _, r := range data.Records {
		raw, err := json.Marshal(r)
		if err != nil || len(raw) > MaxRecordBytes {
			return ErrCapacity
		}
		if !validBinding(r.Binding) || seen[r.Generation] {
			return fmt.Errorf("invalid runtime receipt identity")
		}
		seen[r.Generation] = true
		created, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		updated, updatedErr := time.Parse(time.RFC3339Nano, r.UpdatedAt)
		if err != nil || updatedErr != nil || created.Year() < 1970 || updated.Year() > 9999 || updated.Before(created) || created.UTC().Format(time.RFC3339Nano) != r.CreatedAt || updated.UTC().Format(time.RFC3339Nano) != r.UpdatedAt {
			return fmt.Errorf("invalid runtime receipt time")
		}
		if r.Owner != nil && !validOwner(*r.Owner) {
			return fmt.Errorf("invalid runtime owner")
		}
		if r.OperationID != "" && (!validUUID(r.OperationID) || r.Owner == nil) {
			return fmt.Errorf("invalid runtime stop operation")
		}
		if r.OperationID != "" {
			if operations[r.OperationID] {
				return fmt.Errorf("duplicate runtime stop operation")
			}
			operations[r.OperationID] = true
		}
		valid := false
		switch r.State {
		case Prepared:
			valid = r.Owner == nil && r.OperationID == "" && r.Code == ""
		case Owned:
			valid = r.Owner != nil && r.OperationID == "" && r.Code == ""
		case Stopping:
			valid = r.Owner != nil && r.OperationID != "" && r.Code == ""
		case Completed:
			valid = r.Owner != nil && r.OperationID != "" && r.Code == "ok"
		case Uncertain:
			valid = uncertainCode(r.Code)
		}
		if !valid {
			return fmt.Errorf("invalid runtime receipt state")
		}
		if r.State != Completed {
			if activePaths[r.Target.Path] || activeDirectories[r.Target.Directory] || activeAllocations[r.Allocation] {
				return fmt.Errorf("ambiguous active runtime owners")
			}
			activePaths[r.Target.Path], activeDirectories[r.Target.Directory], activeAllocations[r.Allocation] = true, true, true
		}
	}
	return nil
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validBinding(b Binding) bool {
	validPath := func(path string) bool {
		return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
	}
	validDirectory := func(d registry.DirectoryIdentity) bool { return decimal(d.Dev, false) && decimal(d.Ino, false) }
	return validUUID(b.Generation) && b.Allocation.Kind == "allocated" && validUUID(b.Allocation.Generation) && validDigest(b.ConfigDigest) && validDigest(b.PostStopDigest) && validPath(b.Target.Path) && validDirectory(b.Target.Directory) && (b.Target.Common == nil || validPath(b.Target.Common.Path) && validDirectory(b.Target.Common.DirectoryIdentity))
}

func validDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func decimal(value string, positive bool) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == value && (!positive || n > 0)
}

func validOwner(owner Owner) bool {
	if !validUUID(owner.BootID) || owner.PID <= 0 || owner.PID > 2147483647 {
		return false
	}
	parts := strings.Split(owner.BirthID, ":")
	if len(parts) == 1 {
		return decimal(parts[0], true)
	}
	if len(parts) != 2 || !decimal(parts[0], true) || !decimal(parts[1], false) {
		return false
	}
	micros, _ := strconv.ParseUint(parts[1], 10, 64)
	return micros < 1000000
}

func uncertainCode(code string) bool {
	switch code {
	case "owner_unverified", "owner_changed", "stop_interrupted", "child_failed", "post_stop_failed", "durability_failed":
		return true
	default:
		return false
	}
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
