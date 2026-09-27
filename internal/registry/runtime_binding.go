package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RuntimeBinding identifies the exact runtime whose completion release needs.
// Unknown is deliberately different from a fresh, never-started enrollment.
type RuntimeBinding struct {
	Kind       string `json:"kind"`
	Generation string `json:"generation,omitempty"`
}

func validRuntimeBinding(binding RuntimeBinding) bool {
	return (binding.Kind == "never_started" || binding.Kind == "unknown") && binding.Generation == "" || binding.Kind == "supervisor" && canonicalUUID(binding.Generation)
}

// AllocationRuntimeBinding never infers freshness from legacy absence.
func AllocationRuntimeBinding(allocation Allocation) (RuntimeBinding, error) {
	value, present := allocation["runtime_binding"]
	if !present {
		return RuntimeBinding{Kind: "unknown"}, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return RuntimeBinding{}, err
	}
	var binding RuntimeBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil || !validRuntimeBinding(binding) {
		return RuntimeBinding{}, fmt.Errorf("invalid runtime binding")
	}
	canonical, _ := json.Marshal(binding)
	var originalValue, canonicalValue any
	_ = json.Unmarshal(raw, &originalValue)
	_ = json.Unmarshal(canonical, &canonicalValue)
	if !reflect.DeepEqual(originalValue, canonicalValue) {
		return RuntimeBinding{}, fmt.Errorf("noncanonical runtime binding")
	}
	return binding, nil
}

func withRuntimeTargetLock(locks *LifecycleLocks, target TargetIdentity, fn func() error) error {
	if locks == nil {
		return fmt.Errorf("runtime binding requires lifecycle lock")
	}
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if locks.closed || !slices.Contains(locks.paths, target.Path) {
		return fmt.Errorf("runtime target lifecycle lock is not held")
	}
	return fn()
}

// EnrollFreshAllocation creates an allocation in the same locked operation that
// certifies it has never started. The caller must own the fresh supported
// creation sequence; this API must not be used to adopt an existing worktree's
// legacy runtime. Existing allocation paths and supplied identities are refused.
func (r *Registry) EnrollFreshAllocation(locks *LifecycleLocks, target TargetIdentity, entry Allocation) (Allocation, error) {
	return r.EnrollFreshAllocationTx(locks, target, func(UsedResources) (Allocation, error) { return entry, nil })
}

// EnrollFreshAllocationTx selects resources and certifies fresh enrollment in
// one registry transaction, under the caller's creation lifecycle lock.
func (r *Registry) EnrollFreshAllocationTx(locks *LifecycleLocks, target TargetIdentity, compute func(UsedResources) (Allocation, error)) (Allocation, error) {
	if compute == nil {
		return nil, fmt.Errorf("fresh allocation computation is required")
	}
	var result Allocation
	err := withRuntimeTargetLock(locks, target, func() error {
		return r.registryTransaction(func(data *RegistryData) error {
			if data.Version != 4 {
				return fmt.Errorf("fresh runtime enrollment requires schema 4; no automatic legacy migration")
			}
			if err := verifyTarget(target, false); err != nil {
				return err
			}
			if err := reservationFor(data, target.Path); err != nil {
				return err
			}
			if allocationAt(*data, target.Path) != nil {
				return fmt.Errorf("fresh enrollment target is already allocated")
			}
			entry, err := compute(UsedResources{Ports: allocationsUsedPorts(data.Allocations), RedisDbs: allocationsUsedRedisDbs(data.Allocations)})
			if err != nil {
				return err
			}
			if _, exists := entry["allocation_generation"]; exists {
				return fmt.Errorf("fresh enrollment cannot supply generation")
			}
			if _, exists := entry["runtime_binding"]; exists {
				return fmt.Errorf("fresh enrollment cannot supply runtime binding")
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			var allocation Allocation
			if err := json.Unmarshal(raw, &allocation); err != nil {
				return err
			}
			if allocation == nil {
				return fmt.Errorf("fresh allocation is required")
			}
			if err := verifyTarget(target, false); err != nil {
				return err
			}
			if path := GetString(allocation, "worktree"); path != "" && resolvePath(path) != target.Path {
				return fmt.Errorf("fresh allocation target mismatch")
			}
			if allocation["allocated_at"] == nil {
				allocation["allocated_at"] = time.Now().UTC().Format(time.RFC3339)
			}
			allocation["worktree"] = target.Path
			allocation["allocation_generation"] = uuid.NewString()
			allocation["runtime_binding"] = RuntimeBinding{Kind: "never_started"}
			data.Allocations = append(data.Allocations, allocation)
			result = allocation
			return nil
		}, true)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RegisterRuntime persists the generation before launch effects. A supervisor
// predecessor may only be replaced after the caller verifies its exact durable
// completed outcome under the held lifecycle lock. Unknown ownership cannot be
// adopted. An identical generation is an observation, not permission to launch
// again after an interrupted registration.
func (r *Registry) RegisterRuntime(locks *LifecycleLocks, target TargetIdentity, allocationGeneration string, expected RuntimeBinding, generation string) (Allocation, error) {
	var result Allocation
	err := withRuntimeTargetLock(locks, target, func() error {
		return r.registryTransaction(func(data *RegistryData) error {
			if data.Version != 4 || !canonicalUUID(allocationGeneration) || !validRuntimeBinding(expected) || expected.Kind == "unknown" || !canonicalUUID(generation) {
				return fmt.Errorf("invalid runtime registration")
			}
			if err := verifyTarget(target, false); err != nil {
				return err
			}
			if err := reservationFor(data, target.Path); err != nil {
				return err
			}
			allocation := allocationAt(*data, target.Path)
			if allocation == nil || GetString(allocation, "allocation_generation") != allocationGeneration {
				return fmt.Errorf("runtime allocation changed")
			}
			binding, err := AllocationRuntimeBinding(allocation)
			next := RuntimeBinding{Kind: "supervisor", Generation: generation}
			if err != nil || binding != expected && binding != next {
				return fmt.Errorf("runtime predecessor changed")
			}
			allocation["runtime_binding"] = next
			result = allocation
			return nil
		}, true)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CaptureReleaseTarget captures the canonical directory and Git-common identity
// without trusting inherited Git redirection or adopting runtime ownership.
func CaptureReleaseTarget(path string) (TargetIdentity, error) {
	canonical, err := CanonicalLifecycleTarget(path)
	if err != nil {
		return TargetIdentity{}, err
	}
	directory, err := directoryIdentity(canonical)
	if err != nil {
		return TargetIdentity{}, err
	}
	target := TargetIdentity{Path: canonical, Directory: directory}
	raw, gitErr := identityGit(canonical, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitErr == nil {
		commonPath := strings.TrimSuffix(string(raw), "\n")
		if commonPath == string(raw) || !filepath.IsAbs(commonPath) {
			return TargetIdentity{}, fmt.Errorf("Git common identity unavailable")
		}
		commonPath, err = filepath.EvalSymlinks(commonPath)
		if err != nil {
			return TargetIdentity{}, err
		}
		commonDirectory, err := directoryIdentity(commonPath)
		if err != nil {
			return TargetIdentity{}, err
		}
		target.Common = &CommonIdentity{Path: commonPath, DirectoryIdentity: commonDirectory}
	}
	if err := VerifyReleaseTarget(target); err != nil {
		return TargetIdentity{}, err
	}
	return target, nil
}
