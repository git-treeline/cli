package registry

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/supervisor"
)

// CanonicalLifecycleTarget resolves directory aliases without treating a broken
// symlink or inaccessible ancestor as an absent create/release target.
func CanonicalLifecycleTarget(path string) (string, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("invalid lifecycle target")
	}
	absolute := path
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		absolute = cwd + string(filepath.Separator) + path
	}
	parts := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	resolved := string(filepath.Separator)
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			resolved = filepath.Dir(resolved)
			continue
		}
		if len(part) > 255 {
			return "", fmt.Errorf("invalid lifecycle target component")
		}
		candidate := filepath.Join(resolved, part)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			resolved = candidate
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolving lifecycle target: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			candidate, err = filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", fmt.Errorf("resolving lifecycle target alias: %w", err)
			}
			info, err = os.Stat(candidate)
			if err != nil {
				return "", fmt.Errorf("inspecting lifecycle target alias: %w", err)
			}
		}
		if !info.IsDir() {
			return "", fmt.Errorf("lifecycle target has a non-directory component: %s", candidate)
		}
		resolved = candidate
	}
	if len(resolved) > 4096 {
		return "", fmt.Errorf("lifecycle target is too long")
	}
	return resolved, nil
}

func canonicalLifecycleTargets(paths []string) ([]string, error) {
	keys, err := resolveLifecycleTargets(paths)
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

func resolveLifecycleTargets(paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > maxReleaseOperations {
		return nil, fmt.Errorf("invalid lifecycle target count")
	}
	keys := make([]string, 0, len(paths))
	for _, path := range paths {
		key, err := CanonicalLifecycleTarget(path)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// LifecycleLocks protects a canonical target set. It is separate from the
// registry transaction lock and must be acquired before entering a transaction.
type LifecycleLocks struct {
	paths []string
	files []*os.File
	once  sync.Once
	err   error
}

func (l *LifecycleLocks) Paths() []string { return slices.Clone(l.paths) }

// Close releases locks without unlinking their files; removing a lock file can
// split concurrent callers across different inodes and break exclusion.
func (l *LifecycleLocks) Close() error {
	l.once.Do(func() {
		for i := len(l.files) - 1; i >= 0; i-- {
			l.err = errors.Join(l.err, syscall.Flock(int(l.files[i].Fd()), syscall.LOCK_UN), l.files[i].Close())
		}
	})
	return l.err
}

// AcquireLifecycleLocks bounds contention to five seconds or the caller's
// earlier cancellation. No registry lock is held while acquiring this set.
func AcquireLifecycleLocks(ctx context.Context, paths []string) (*LifecycleLocks, error) {
	dir := fmt.Sprintf("/tmp/gtl-lifecycle-%d", os.Geteuid())
	return acquireLifecycleLocks(ctx, paths, dir)
}

func lifecycleLockPath(dir, key string) string {
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(dir, fmt.Sprintf("%x.lock", digest))
}

func acquireLifecycleLocks(ctx context.Context, paths []string, dir string) (_ *LifecycleLocks, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("lifecycle lock context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Keep the original spellings for the post-acquisition alias check.
	paths = slices.Clone(paths)
	originalKeys, err := resolveLifecycleTargets(paths)
	if err != nil {
		return nil, err
	}
	keys := slices.Clone(originalKeys)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if err := supervisor.EnsureSocketDir(filepath.Join(dir, "state.sock")); err != nil {
		return nil, err
	}
	locks := &LifecycleLocks{paths: keys}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, locks.Close())
		}
	}()
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := supervisor.OpenStateLock(lifecycleLockPath(dir, key))
		if err != nil {
			return nil, err
		}
		locks.files = append(locks.files, file)
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				break
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
				return nil, fmt.Errorf("locking lifecycle target: %w", err)
			}
			timer := time.NewTimer(25 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("waiting for lifecycle target lock: %w", ctx.Err())
			case <-timer.C:
			}
		}
	}
	for i, path := range paths {
		key, err := CanonicalLifecycleTarget(path)
		if err != nil {
			return nil, err
		}
		if key != originalKeys[i] {
			return nil, fmt.Errorf("lifecycle target changed while waiting for its lock")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return locks, nil
}

// CheckReleaseReservations is a strict, nonwriting preflight for regular
// lifecycle writers. Call it after acquiring the execution locks. It grants no
// permission to resume an existing release operation.
func (r *Registry) CheckReleaseReservations(paths []string) error {
	keys, err := canonicalLifecycleTargets(paths)
	if err != nil {
		return err
	}
	data, err := r.readReleaseStore()
	if err != nil {
		return err
	}
	for _, key := range keys {
		identity, err := directoryIdentity(key)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspecting lifecycle reservation target: %w", err)
		}
		for _, operation := range data.ReleaseOperations {
			if operation.Outcome() != "completed" && (operation.Target.Path == key || err == nil && operation.Target.Directory == identity) {
				return fmt.Errorf("target reserved by release %s", operation.OperationID)
			}
		}
	}
	return nil
}
