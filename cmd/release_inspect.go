package cmd

import (
	"encoding/json"
	"reflect"

	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/worktree"
	"github.com/spf13/cobra"
)

// Inspect captures intent without preparing an operation or granting permission
// to execute it. Start revalidates the captured identities before any effects.
func newReleaseInspectCommand(registryPath string) *cobra.Command {
	var operationID string
	var policy registry.ReleasePolicy
	command := &cobra.Command{
		Use:           "inspect <path> --operation-id <uuid>",
		Short:         "Capture an exact release request as JSON without running cleanup",
		SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE:  guardAppManagedDev,
		PersistentPostRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || policy.ForceDirty && !policy.RemoveWorktree {
				return releaseRefusal("invalid_request")
			}
			if !publicReleaseUUID(operationID) {
				return releaseRefusal("invalid_operation_id")
			}
			path, err := registry.CanonicalLifecycleTarget(args[0])
			if err != nil {
				return releaseRefusal("identity_changed")
			}
			locks, err := registry.AcquireLifecycleLocks(cmd.Context(), []string{path})
			if err != nil {
				return releaseRefusal("execution_lock_unavailable")
			}
			// Cleanup does not change the completed operation or its receipt.
			defer func() { _ = locks.Close() }()
			reg := registry.New(registryPath)
			checkAvailable := func() error {
				existing, err := reg.GetRelease(operationID)
				if err != nil {
					return releaseRefusal("registry_unavailable")
				}
				if existing != nil {
					return releaseRefusal("operation_exists_use_resume")
				}
				if err := reg.CheckReleaseReservations([]string{path}); err != nil {
					return releaseRefusal("reservation_refused")
				}
				return nil
			}
			if err := checkAvailable(); err != nil {
				return err
			}
			target, err := registry.CaptureReleaseTarget(path)
			if err != nil {
				return releaseRefusal("identity_changed")
			}
			allocation, err := reg.FindForRelease(path)
			if err != nil {
				return releaseRefusal("registry_unavailable")
			}
			intent := registry.ReleaseRequest{OperationID: operationID, Target: target, Policy: policy}
			if allocation == nil {
				if !policy.RemoveWorktree || policy.DropDatabases || policy.IgnoreHookFailure || target.Common == nil {
					return releaseRefusal("invalid_request")
				}
				intent.Allocation = registry.AllocationRef{Kind: "unallocated"}
				intent.HooksDigest = (releaseHookPlan{}).digest()
			} else {
				generation := registry.GetString(allocation, "allocation_generation")
				binding, err := registry.AllocationRuntimeBinding(allocation)
				if err != nil || binding.Kind == "unknown" || !publicReleaseUUID(generation) {
					return releaseRefusal("runtime_binding_unavailable")
				}
				intent.Allocation = registry.AllocationRef{Kind: "allocated", Generation: generation}
				intent.RuntimeBinding = &binding
				plan, err := loadReleaseHookPlan(path)
				if err != nil {
					return releaseRefusal("config_unavailable")
				}
				intent.HooksDigest = plan.digest()
				if policy.DropDatabases {
					intent.DatabaseConfigRevision, err = format.DatabaseCleanupConfigRevision(path)
					if err != nil {
						return releaseRefusal("config_unavailable")
					}
				}
			}
			if policy.RemoveWorktree {
				if _, err := worktree.ValidateRemoval(path); err != nil {
					return releaseRefusal("removal_refused")
				}
			}
			if registry.VerifyReleaseTarget(target) != nil {
				return releaseRefusal("identity_changed")
			}
			current, err := reg.FindForRelease(path)
			if err != nil {
				return releaseRefusal("registry_unavailable")
			}
			if !reflect.DeepEqual(current, allocation) {
				return releaseRefusal("binding_conflict")
			}
			if allocation != nil {
				plan, err := loadReleaseHookPlan(path)
				if err != nil || plan.digest() != intent.HooksDigest {
					return releaseRefusal("config_changed")
				}
				if policy.DropDatabases {
					revision, err := format.DatabaseCleanupConfigRevision(path)
					if err != nil || revision != intent.DatabaseConfigRevision {
						return releaseRefusal("config_changed")
					}
				}
			}
			if err := checkAvailable(); err != nil {
				return err
			}
			if cmd.Context().Err() != nil {
				return releaseRefusal("cancelled")
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(intent); err != nil {
				return releaseRefusal("output_unavailable")
			}
			return nil
		},
	}
	command.Flags().StringVar(&operationID, "operation-id", "", "Unused canonical operation UUID; use release resume for a retained operation")
	command.Flags().BoolVar(&policy.DropDatabases, "drop-db", false, "Include checked database cleanup")
	command.Flags().BoolVar(&policy.RemoveWorktree, "remove-worktree", false, "Include removal of a linked Git worktree")
	command.Flags().BoolVar(&policy.ForceDirty, "force-dirty", false, "Allow removal of a dirty linked worktree")
	command.Flags().BoolVar(&policy.IgnoreHookFailure, "ignore-hook-failure", false, "Allow settled release hook failures")
	command.SetFlagErrorFunc(func(*cobra.Command, error) error { return releaseRefusal("invalid_request") })
	return command
}
