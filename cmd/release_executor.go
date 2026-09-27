package cmd

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/git-treeline/cli/internal/registry"
)

// releaseExecutor runs only through adapters that establish the entire remaining
// plan's prerequisites, including post-hook recovery and runtime quiescence.
// It does not advertise a capability or supply best-effort cleanup adapters.
type releaseExecutor struct {
	Registry  *registry.Registry
	HeldLocks *registry.LifecycleLocks
	Acquire   func(context.Context, []string) (*registry.LifecycleLocks, error)
	Preflight func(context.Context, registry.ReleaseOperation) registry.PhaseResult
	RunPhase  func(context.Context, registry.ReleaseOperation, registry.ReleasePhase) registry.PhaseResult
	// RecoverSupervisor only observes a durable exact-generation outcome. It must never send shutdown or replay hooks.
	RecoverSupervisor func(context.Context, registry.ReleaseOperation) bool
}

type releaseExecutionError struct{ Code string }

func (e *releaseExecutionError) Error() string { return e.Code }

func releaseRefusal(code string) error { return &releaseExecutionError{Code: code} }

func (e *releaseExecutor) Start(ctx context.Context, intent registry.ReleaseRequest) (*registry.ReleaseOperation, error) {
	return e.run(ctx, intent, false)
}

func (e *releaseExecutor) Resume(ctx context.Context, intent registry.ReleaseRequest) (*registry.ReleaseOperation, error) {
	return e.run(ctx, intent, true)
}

func (e *releaseExecutor) run(ctx context.Context, intent registry.ReleaseRequest, resume bool) (*registry.ReleaseOperation, error) {
	if e.Registry == nil || e.Preflight == nil || e.RunPhase == nil {
		return nil, releaseRefusal("executor_unconfigured")
	}
	// The caller and adapters cannot change the binding held by this invocation.
	if intent.RuntimeBinding != nil {
		binding := *intent.RuntimeBinding
		intent.RuntimeBinding = &binding
	}
	if intent.Target.Common != nil {
		common := *intent.Target.Common
		intent.Target.Common = &common
	}
	acquire := e.Acquire
	if acquire == nil {
		acquire = acquireCommandLifecycleLocks
	}
	locks := e.HeldLocks
	if locks != nil {
		if !slices.Contains(locks.Paths(), intent.Target.Path) {
			return nil, releaseRefusal("execution_lock_unavailable")
		}
	} else {
		var err error
		locks, err = acquire(ctx, []string{intent.Target.Path})
		if err != nil || locks == nil {
			return nil, releaseRefusal("execution_lock_unavailable")
		}
		defer locks.Close()
	}

	op, err := e.Registry.GetRelease(intent.OperationID)
	if err != nil {
		return nil, releaseRefusal("registry_unavailable")
	}
	if op != nil {
		if !reflect.DeepEqual(op.ReleaseRequest, intent) {
			return op, releaseRefusal("binding_conflict")
		}
	} else {
		if resume {
			return nil, releaseRefusal("not_found")
		}
		if err := e.Registry.CheckReleaseReservations([]string{intent.Target.Path}); err != nil {
			return nil, releaseRefusal("reservation_refused")
		}
		prepared, _, err := e.Registry.PrepareRelease(intent)
		if err != nil {
			return nil, releaseRefusal("prepare_refused")
		}
		op = &prepared
	}
	for {
		if op.Outcome() == "completed" {
			return op, nil
		}
		if op.Outcome() == "uncertain" {
			recovered := false
			if e.RecoverSupervisor != nil && ctx.Err() == nil {
				for _, phase := range op.Phases {
					if phase.Name == registry.PhaseSupervisor && phase.State == "started" && e.RecoverSupervisor(ctx, cloneReleaseOperation(*op)) {
						updated, err := e.Registry.RecordReleasePhase(registry.PhaseToken{OperationID: op.OperationID, Phase: phase.Name, Attempt: phase.Attempt}, registry.PhaseResult{State: "completed", Code: "ok"})
						if err != nil {
							return e.latest(intent.OperationID, "receipt_unavailable")
						}
						op, recovered = &updated, true
						break
					}
				}
			}
			if recovered {
				continue
			}
			return op, releaseRefusal("uncertain")
		}
		if ctx.Err() != nil {
			return op, releaseRefusal("cancelled")
		}
		var next registry.ReleasePhase
		for _, phase := range op.Phases {
			if phase.State != "completed" {
				next = phase.Name
				break
			}
		}
		gate := e.Preflight(ctx, cloneReleaseOperation(*op))
		gateCode := ""
		if gate.State != "completed" || gate.Code != "ok" {
			gateCode = "preflight_blocked"
			if gate.State != "blocked" || !validReleaseBlockedCode(gate.Code) {
				// No effect ran. An unavailable prerequisite is a blocked gate,
				// not evidence of an uncertain external dispatch.
				gate = registry.PhaseResult{State: "blocked", Code: "failed"}
				gateCode = "preflight_unavailable"
			}
		}
		if ctx.Err() != nil {
			return op, releaseRefusal("cancelled")
		}
		token, err := e.Registry.BeginReleasePhase(intent.OperationID, next, op.Revision)
		if err != nil {
			return e.latest(intent.OperationID, "phase_refused")
		}
		if gateCode != "" {
			if _, err := e.Registry.RecordReleasePhase(token, gate); err != nil {
				return e.latest(intent.OperationID, "receipt_unavailable")
			}
			return e.latest(intent.OperationID, gateCode)
		}
		started, err := e.Registry.GetRelease(intent.OperationID)
		if err != nil || started == nil {
			return e.latest(intent.OperationID, "receipt_unavailable")
		}
		if ctx.Err() != nil {
			// Begin is durable but no dispatch occurred; retain its conservative
			// uncertainty rather than inventing a phase completion.
			return started, releaseRefusal("cancelled")
		}
		if next == registry.PhaseAllocation {
			retired, err := e.Registry.RetireReleaseAllocation(token)
			if err != nil {
				return e.latest(intent.OperationID, "receipt_unavailable")
			}
			op = &retired
			continue
		}
		result := e.RunPhase(ctx, cloneReleaseOperation(*started), next)
		if !validReleaseEffectResult(result, next, intent.Policy) {
			result = registry.PhaseResult{State: "uncertain", Code: "unknown"}
		}
		if next == registry.PhaseSupervisor && result.State == "uncertain" {
			// Keep the original attempt eligible for read-only receipt recovery;
			// BeginReleasePhase still refuses another dispatch of this attempt.
			return started, releaseRefusal("uncertain")
		}
		updated, err := e.Registry.RecordReleasePhase(token, result)
		if err != nil {
			return e.latest(intent.OperationID, "receipt_unavailable")
		}
		op = &updated
		if result.State != "completed" {
			return op, releaseRefusal(result.State)
		}
	}
}

func (e *releaseExecutor) latest(id string, code string) (*registry.ReleaseOperation, error) {
	if current, err := e.Registry.GetRelease(id); err == nil && current != nil {
		return current, releaseRefusal(code)
	}
	return nil, releaseRefusal(code)
}

func cloneReleaseOperation(op registry.ReleaseOperation) registry.ReleaseOperation {
	data, _ := json.Marshal(op)
	var cloned registry.ReleaseOperation
	_ = json.Unmarshal(data, &cloned)
	return cloned
}

func validReleaseBlockedCode(code string) bool {
	return code == "failed" || code == "privilege_required" || code == "config_changed" || code == "identity_changed"
}

func validReleaseEffectResult(result registry.PhaseResult, phase registry.ReleasePhase, policy registry.ReleasePolicy) bool {
	switch result.State {
	case "completed":
		return result.Code == "ok" || result.Code == "ignored_hook_failure" && policy.IgnoreHookFailure && (phase == registry.PhasePreHook || phase == registry.PhasePostHook)
	case "blocked":
		return validReleaseBlockedCode(result.Code)
	case "uncertain":
		return result.Code == "unknown"
	}
	return false
}
