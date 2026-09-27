package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
)

type releaseHookPlan struct {
	pre, post []string
}

func loadReleaseHookPlan(root string) (releaseHookPlan, error) {
	pc := config.LoadProjectConfigReadOnlyQuiet(root)
	bad := errors.New("invalid_release_hooks")
	if pc.LoadError() != nil {
		return releaseHookPlan{}, bad
	}
	hooks, ok := pc.Data["hooks"].(map[string]any)
	if !ok {
		return releaseHookPlan{}, bad
	}
	parse := func(name string) ([]string, error) {
		commands := []string{}
		value, exists := hooks[name]
		if !exists {
			return commands, nil
		}
		items, ok := value.([]any)
		if !ok {
			return nil, bad
		}
		for _, item := range items {
			command, ok := item.(string)
			if !ok || !utf8.ValidString(command) || strings.ContainsRune(command, 0) {
				return nil, bad
			}
			commands = append(commands, command)
		}
		return commands, nil
	}
	pre, err := parse("pre_release")
	if err != nil {
		return releaseHookPlan{}, err
	}
	post, err := parse("post_release")
	return releaseHookPlan{pre: pre, post: post}, err
}

func (p releaseHookPlan) digest() string {
	// Normalize absent lists to JSON arrays so preparation and resume agree.
	data, _ := json.Marshal(struct {
		Pre  []string `json:"pre_release"`
		Post []string `json:"post_release"`
	}{append([]string{}, p.pre...), append([]string{}, p.post...)})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type checkedHookOutcome string

const (
	hookNotStarted    checkedHookOutcome = "not_started"
	hookSettledOK     checkedHookOutcome = "settled_ok"
	hookSettledFailed checkedHookOutcome = "settled_failed"
	hookUncertain     checkedHookOutcome = "uncertain"
)

// A settled result certifies completion of the command and its child work,
// not merely the shell's exit. There is deliberately no default shell runner.
type checkedHookRunner func(context.Context, string, string) checkedHookOutcome

type releaseHooksAdapter struct {
	Registry *registry.Registry
	RunHook  checkedHookRunner
}

func (a releaseHooksAdapter) Run(ctx context.Context, op registry.ReleaseOperation, phase registry.ReleasePhase) registry.PhaseResult {
	blocked := registry.PhaseResult{State: "blocked", Code: "failed"}
	unknown := registry.PhaseResult{State: "uncertain", Code: "unknown"}
	if a.Registry == nil || op.Allocation.Kind != "allocated" || op.Snapshot == nil || (phase != registry.PhasePreHook && phase != registry.PhasePostHook) {
		return blocked
	}
	op = cloneReleaseOperation(op)
	started := false
	for _, record := range op.Phases {
		if record.Name == phase {
			started = record.State == "started" && record.Attempt != ""
		}
	}
	if !started {
		return blocked
	}
	check := func() (releaseHookPlan, registry.PhaseResult) {
		if ctx.Err() != nil {
			return releaseHookPlan{}, blocked
		}
		current, err := a.Registry.GetRelease(op.OperationID)
		if err != nil || current == nil || !reflect.DeepEqual(*current, op) {
			return releaseHookPlan{}, blocked
		}
		if registry.VerifyReleaseTarget(op.Target) != nil {
			return releaseHookPlan{}, registry.PhaseResult{State: "blocked", Code: "identity_changed"}
		}
		plan, err := loadReleaseHookPlan(op.Target.Path)
		if err != nil || plan.digest() != op.HooksDigest {
			return releaseHookPlan{}, registry.PhaseResult{State: "blocked", Code: "config_changed"}
		}
		return plan, registry.PhaseResult{}
	}
	plan, refusal := check()
	if refusal.State != "" {
		return refusal
	}
	commands := plan.pre
	if phase == registry.PhasePostHook {
		commands = plan.post
	}
	if len(commands) > 0 && a.RunHook == nil {
		return blocked
	}
	for i, command := range commands {
		if _, refusal := check(); refusal.State != "" {
			if i > 0 {
				return unknown
			}
			return refusal
		}
		switch callCheckedHook(a.RunHook, ctx, op.Target.Path, command) {
		case hookSettledOK:
		case hookSettledFailed:
			if op.Policy.IgnoreHookFailure {
				return registry.PhaseResult{State: "completed", Code: "ignored_hook_failure"}
			}
			return unknown
		case hookNotStarted:
			if i == 0 {
				return blocked
			}
			return unknown
		default:
			return unknown
		}
	}
	return registry.PhaseResult{State: "completed", Code: "ok"}
}

func callCheckedHook(run checkedHookRunner, ctx context.Context, dir, command string) (outcome checkedHookOutcome) {
	outcome = hookUncertain
	defer func() {
		if recover() != nil {
			outcome = hookUncertain
		}
	}()
	return run(ctx, dir, command)
}
