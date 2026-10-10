package cmd

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
)

type releaseDatabasesAdapter struct {
	Registry   *registry.Registry
	NewAdapter func(string, []string) (database.Adapter, error)
}

func (a releaseDatabasesAdapter) Run(ctx context.Context, op registry.ReleaseOperation) registry.PhaseResult {
	blocked := registry.PhaseResult{State: "blocked", Code: "failed"}
	if a.Registry == nil || ctx.Err() != nil || !op.Policy.DropDatabases || op.Allocation.Kind != "allocated" || op.Snapshot == nil || len(op.DatabaseConfigRevision) != 64 {
		return blocked
	}
	op = cloneReleaseOperation(op)
	started := false
	for _, phase := range op.Phases {
		if phase.Name == registry.PhaseDatabases {
			started = phase.State == "started" && phase.Attempt != ""
		}
	}
	if !started {
		return blocked
	}
	if err := validateManagedDevRelease(op); err != nil {
		return blocked
	}
	keep, err := a.survivors(op)
	if err != nil {
		return blocked
	}
	attempted, err := format.DropDatabasesChecked(ctx, op.Target.Path, *op.Snapshot, keep, format.DatabaseCleanupCheck{
		ConfigRevision: op.DatabaseConfigRevision, NewAdapter: a.NewAdapter,
		BeforeDrop: func() error {
			current, err := a.Registry.GetRelease(op.OperationID)
			if err != nil || current == nil || !reflect.DeepEqual(*current, op) {
				return errors.New("release_binding_changed")
			}
			info, err := os.Lstat(op.Target.Path)
			if err != nil || !info.IsDir() {
				return errors.New("release_target_changed")
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || strconv.FormatUint(uint64(stat.Dev), 10) != op.Target.Directory.Dev || strconv.FormatUint(uint64(stat.Ino), 10) != op.Target.Directory.Ino {
				return errors.New("release_target_changed")
			}
			currentKeep, err := a.survivors(op)
			if err != nil || !reflect.DeepEqual(currentKeep, keep) {
				return errors.New("release_survivors_changed")
			}
			return validateManagedDevRelease(op)
		},
	})
	if err != nil {
		if attempted {
			return registry.PhaseResult{State: "uncertain", Code: "unknown"}
		}
		if errors.Is(err, format.ErrDatabaseCleanupConfig) {
			return registry.PhaseResult{State: "blocked", Code: "config_changed"}
		}
		return blocked
	}
	return registry.PhaseResult{State: "completed", Code: "ok"}
}

func (a releaseDatabasesAdapter) survivors(op registry.ReleaseOperation) (map[string]bool, error) {
	allocations, err := a.Registry.ReleasePreflight()
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	found := false
	for _, allocation := range allocations {
		if registry.GetString(allocation, "worktree") == op.Target.Path {
			if registry.GetString(allocation, "allocation_generation") != op.Allocation.Generation {
				return nil, errors.New("release_allocation_changed")
			}
			found = true
			continue
		}
		if value, ok := allocation["database"]; ok {
			name, ok := value.(string)
			if !ok || !cleanupDatabaseName(name, true) {
				return nil, errors.New("invalid_survivor_databases")
			}
		}
		if value, ok := allocation["databases"]; ok {
			names, ok := value.([]any)
			if !ok || len(names) > 256 {
				return nil, errors.New("invalid_survivor_databases")
			}
			for _, value := range names {
				name, ok := value.(string)
				if !ok || !cleanupDatabaseName(name, false) {
					return nil, errors.New("invalid_survivor_databases")
				}
			}
		}
		for _, name := range registry.ExtractDatabases(allocation) {
			keep[name] = true
		}
	}
	if !found {
		return nil, errors.New("release_allocation_missing")
	}
	return keep, nil
}

func cleanupDatabaseName(name string, empty bool) bool {
	return (empty || name != "") && len(name) <= 4096 && utf8.ValidString(name) && !strings.ContainsRune(name, 0)
}
