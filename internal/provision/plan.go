// Package provision implements `gtl provision`: the repo-level, idempotent
// answer to "what must be true on a host before gtl setup can succeed for this
// repo." It reads the provision: section of .treeline.yml and brings a host up
// to that declared baseline — system packages, services, language runtimes, and
// the template database gtl clones worktree databases from.
//
// The package splits into a pure planner (config -> ordered actions, testable
// as data) and an executor that runs each action through injectable seams
// (Deps), checking before acting so every step is safe to re-run.
package provision

import (
	"fmt"
	"strings"

	"github.com/git-treeline/cli/internal/config"
)

// ActionKind identifies what an Action provisions.
type ActionKind string

const (
	ActionApt      ActionKind = "apt"
	ActionService  ActionKind = "service"
	ActionRuntime  ActionKind = "runtime"
	ActionDatabase ActionKind = "database"
)

// DBMode describes how a template database is brought into existence.
type DBMode string

const (
	// DBModeSource hydrates the template from a configured database.sources env
	// (real data), reusing the same dump/restore path as `gtl db pull`.
	DBModeSource DBMode = "source"
	// DBModeHydrate creates an empty template then runs a shell command in the
	// repo dir to fill it (e.g. bin/rails db:schema:load db:seed).
	DBModeHydrate DBMode = "hydrate"
	// DBModeEmpty creates an empty template with no data — worktree databases
	// cloned from it will be empty until a schema is loaded.
	DBModeEmpty DBMode = "empty"
)

// Action is one planned provisioning step. The pure planner fills the intent
// (kind, payload, platform applicability); the executor decides at run time
// whether the action is already satisfied and can be skipped.
type Action struct {
	Kind ActionKind

	// Packages carries apt package names (ActionApt) or service package names
	// (ActionService).
	Packages []string

	// Runtime fields.
	VersionFiles []string // version files that triggered runtime provisioning

	// Database fields.
	DBTemplate string
	DBMode     DBMode
	DBSource   string // env name under database.sources (DBModeSource)
	DBHydrate  string // shell command (DBModeHydrate)

	// PlatformSkip is true when the current OS can't perform this action (apt
	// and services are Linux-only). The executor prints the intent and moves on.
	PlatformSkip bool
	SkipNote     string
}

// Summary renders a one-line human description of the action's intent, used by
// --dry-run and as the leading log line for each step.
func (a Action) Summary() string {
	switch a.Kind {
	case ActionApt:
		return "apt packages: " + strings.Join(a.Packages, " ")
	case ActionService:
		return "services: " + strings.Join(a.Packages, " ")
	case ActionRuntime:
		return "language runtimes via mise (" + strings.Join(a.VersionFiles, ", ") + ")"
	case ActionDatabase:
		switch a.DBMode {
		case DBModeSource:
			return fmt.Sprintf("template database %q from source %q", a.DBTemplate, a.DBSource)
		case DBModeHydrate:
			return fmt.Sprintf("template database %q via: %s", a.DBTemplate, a.DBHydrate)
		default:
			return fmt.Sprintf("template database %q (empty)", a.DBTemplate)
		}
	}
	return string(a.Kind)
}

// PlanConfig derives the config-driven actions (apt, services, database) from a
// parsed provision section, in execution order, marking platform-gated actions
// for the given GOOS. It is pure: no filesystem, no probes. Runtime provisioning
// is not config-driven (it follows the repo's version files) and is appended by
// the executor via RuntimeAction.
func PlanConfig(cfg config.ProvisionConfig, goos string) []Action {
	var actions []Action
	linux := goos == "linux"

	if len(cfg.Apt) > 0 {
		a := Action{Kind: ActionApt, Packages: cfg.Apt}
		if !linux {
			a.PlatformSkip = true
			a.SkipNote = "apt is Linux-only; install these with your Mac package manager if needed"
		}
		actions = append(actions, a)
	}

	if len(cfg.Services) > 0 {
		a := Action{Kind: ActionService, Packages: cfg.Services}
		if !linux {
			a.PlatformSkip = true
			a.SkipNote = "systemd services are Linux-only; skipping on this host"
		}
		actions = append(actions, a)
	}

	if cfg.Database.Template != "" {
		a := Action{Kind: ActionDatabase, DBTemplate: cfg.Database.Template}
		switch {
		case cfg.Database.Source != "":
			a.DBMode = DBModeSource
			a.DBSource = cfg.Database.Source
		case cfg.Database.Hydrate != "":
			a.DBMode = DBModeHydrate
			a.DBHydrate = cfg.Database.Hydrate
		default:
			a.DBMode = DBModeEmpty
		}
		actions = append(actions, a)
	}

	return actions
}
