package cmd

import (
	"context"
	"fmt"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/devresources"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/setup"
	"github.com/git-treeline/cli/internal/worktree"
)

// Tests replace acquisition to exercise command refusal boundaries without
// creating lock files in the host's lifecycle lock directory.
var acquireCommandLifecycleLocks = registry.AcquireLifecycleLocks

type creationSelection struct {
	source, target, existing string
	inPlace                  bool
}

type creationAdmission struct {
	creationSelection
	locks    *registry.LifecycleLocks
	discover func() (creationSelection, error)
}

func discoverCreation(cwd, branch string, reviewInPlace bool, destination func(string, *config.ProjectConfig, *config.UserConfig) string) (creationSelection, error) {
	main, existing, err := worktree.DiscoverWorktreeTargets(cwd, branch)
	if err != nil {
		return creationSelection{}, err
	}
	source, err := registry.CanonicalLifecycleTarget(main)
	if err != nil {
		return creationSelection{}, err
	}
	if reviewInPlace {
		root, err := worktree.RepoRootStrict(cwd)
		if err != nil {
			return creationSelection{}, err
		}
		current, err := registry.CanonicalLifecycleTarget(root)
		if err != nil {
			return creationSelection{}, err
		}
		if current != source {
			if err := validateManagedDevCreation(source, current, branch); err != nil {
				return creationSelection{}, err
			}
			return creationSelection{source: source, target: current, inPlace: true}, nil
		}
	}
	pc := config.LoadProjectConfigReadOnly(source)
	if err := pc.LoadError(); err != nil {
		return creationSelection{}, err
	}
	uc := config.LoadUserConfig("")
	if err := uc.LoadError(); err != nil {
		return creationSelection{}, err
	}
	target := existing
	if target == "" {
		target = destination(source, pc, uc)
	}
	target, err = registry.CanonicalLifecycleTarget(target)
	if err != nil {
		return creationSelection{}, err
	}
	if existing != "" {
		existing = target
	}
	if err := validateManagedDevCreation(source, target, branch); err != nil {
		return creationSelection{}, err
	}
	return creationSelection{source: source, target: target, existing: existing}, nil
}

func validateManagedDevCreation(source, target, branch string) error {
	if !devresources.Active() {
		return nil
	}
	policy, err := devresources.Check()
	if err != nil {
		return err
	}
	pc := config.LoadProjectConfigReadOnlyQuiet(source)
	if err := setup.ValidateManagedDevProject(target, source, pc); err != nil {
		return err
	}
	_, err = policy.Ports(pc.Project(), branch, target == source, pc.PortsNeeded())
	return err
}

func admitCreation(ctx context.Context, discover func() (creationSelection, error)) (*creationAdmission, error) {
	selected, err := discover()
	if err != nil {
		return nil, err
	}
	locks, err := acquireCommandLifecycleLocks(ctx, []string{selected.source, selected.target})
	if err != nil {
		return nil, err
	}
	admission := &creationAdmission{creationSelection: selected, locks: locks, discover: discover}
	if err := admission.recheck(); err != nil {
		_ = locks.Close()
		return nil, err
	}
	return admission, nil
}

func (a *creationAdmission) recheck() error {
	current, err := a.discover()
	if err != nil {
		return err
	}
	if current != a.creationSelection {
		return fmt.Errorf("worktree destination changed during admission; retry the command")
	}
	return registry.New("").CheckReleaseReservations([]string{a.source, a.target})
}

func (a *creationAdmission) retainForStart(start bool) error {
	if !start {
		return nil
	}
	return a.locks.Retain([]string{a.target})
}
