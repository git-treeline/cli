package provision

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/config"
)

// versionFiles are the repo-pinned runtime files mise auto-reads. Their presence
// triggers `mise install` (which installs whatever the repo pins) — runtime
// provisioning is driven by these files, not by the provision: config.
var versionFiles = []string{
	".mise.toml",
	"mise.toml",
	".tool-versions",
	".ruby-version",
	".node-version",
	".nvmrc",
	".python-version",
}

// Deps are the injectable effect/probe seams the executor runs actions through.
// The command layer wires these to real implementations; tests supply fakes.
// Every probe is consulted before its effect so each step is idempotent.
type Deps struct {
	GOOS string

	// FileExists reports whether a path exists (used for runtime detection).
	FileExists func(path string) bool
	// LookPath reports whether a binary is on PATH (nil error = found).
	LookPath func(bin string) (string, error)

	// PackageInstalled reports whether an apt package is installed (dpkg -s).
	PackageInstalled func(pkg string) (bool, error)
	// AptUpdate refreshes the apt package indexes (sudo apt-get update). The
	// executor calls it lazily, exactly once, immediately before the first real
	// install of a run — see withLazyAptUpdate.
	AptUpdate func() error
	// AptInstall installs the given apt packages (sudo apt-get install -y).
	AptInstall func(pkgs []string) error
	// ServiceEnable enables and starts a systemd unit (systemctl enable --now).
	ServiceEnable func(name string) error

	// RunInDir runs a shell command in dir, streaming its output.
	RunInDir func(dir, command string) error

	// DBExists reports whether a local database exists.
	DBExists func(name string) (bool, error)
	// CreateDB creates an empty local database.
	CreateDB func(name string) error
	// HydrateFromSource dumps a configured source env and restores it into the
	// named template database, creating it. Reuses the gtl db pull machinery.
	HydrateFromSource func(template, sourceEnv string) error

	// Log writes a top-level "==>" step line; Warn writes a warning line.
	Log  func(format string, args ...any)
	Warn func(format string, args ...any)
}

// RuntimeAction returns a runtime-provisioning Action when the repo pins any
// language runtime via a version file, or nil when there's nothing to do. It is
// the one non-config-driven step, so it lives here rather than in PlanConfig.
func RuntimeAction(repoDir string, fileExists func(string) bool) *Action {
	var found []string
	for _, f := range versionFiles {
		if fileExists(filepath.Join(repoDir, f)) {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &Action{Kind: ActionRuntime, VersionFiles: found}
}

// BuildPlan assembles the full ordered plan for a repo: config-driven actions
// (apt, services, database) followed by the runtime action when the repo pins a
// runtime. GOOS gates platform-specific actions.
func BuildPlan(cfg config.ProvisionConfig, repoDir string, goos string, fileExists func(string) bool) []Action {
	configActions := PlanConfig(cfg, goos)
	rt := RuntimeAction(repoDir, fileExists)
	if rt == nil {
		return configActions
	}
	// Runtime runs after packages/services and before the database step,
	// matching the documented order: apt -> services -> runtimes -> database.
	out := make([]Action, 0, len(configActions)+1)
	inserted := false
	for _, a := range configActions {
		if a.Kind == ActionDatabase && !inserted {
			out = append(out, *rt)
			inserted = true
		}
		out = append(out, a)
	}
	if !inserted {
		out = append(out, *rt)
	}
	return out
}

// Run executes the plan in order, checking before acting. It returns on the
// first real failure (platform skips and idempotent no-ops are not failures).
func Run(actions []Action, repoDir string, d Deps) error {
	d = withLazyAptUpdate(d)
	for _, a := range actions {
		if err := runAction(a, repoDir, d); err != nil {
			return err
		}
	}
	return nil
}

// withLazyAptUpdate wraps d.AptInstall so `apt-get update` runs exactly once
// per provision run, lazily — immediately before the first real package
// install (apt packages or service packages both route through AptInstall). A
// fresh host has stale/empty package indexes, so without this the first install
// fails with "Unable to locate package". A fully-idempotent re-run installs
// nothing, so it never triggers the update: no-op runs stay fast and sudo-free.
func withLazyAptUpdate(d Deps) Deps {
	if d.AptInstall == nil {
		return d
	}
	install := d.AptInstall
	update := d.AptUpdate
	updated := false
	d.AptInstall = func(pkgs []string) error {
		if !updated {
			if update != nil {
				if err := update(); err != nil {
					return err
				}
			}
			updated = true
		}
		return install(pkgs)
	}
	return d
}

func runAction(a Action, repoDir string, d Deps) error {
	switch a.Kind {
	case ActionApt:
		return runApt(a, d)
	case ActionService:
		return runServices(a, d)
	case ActionRuntime:
		return runRuntime(a, repoDir, d)
	case ActionDatabase:
		return runDatabase(a, repoDir, d)
	}
	return nil
}

func runApt(a Action, d Deps) error {
	d.Log("apt: %s", join(a.Packages))
	if a.PlatformSkip {
		d.Warn("%s", a.SkipNote)
		return nil
	}
	missing, err := missingPackages(a.Packages, d)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		d.Log("all apt packages already installed")
		return nil
	}
	d.Log("installing: %s", join(missing))
	return d.AptInstall(missing)
}

func runServices(a Action, d Deps) error {
	d.Log("services: %s", join(a.Packages))
	if a.PlatformSkip {
		d.Warn("%s", a.SkipNote)
		return nil
	}
	missing, err := missingPackages(a.Packages, d)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		d.Log("installing service packages: %s", join(missing))
		if err := d.AptInstall(missing); err != nil {
			return err
		}
	}
	for _, svc := range a.Packages {
		d.Log("enabling service: %s", svc)
		if err := d.ServiceEnable(svc); err != nil {
			return err
		}
	}
	return nil
}

func runRuntime(a Action, repoDir string, d Deps) error {
	d.Log("runtimes: pinned by %s", join(a.VersionFiles))
	if _, err := d.LookPath("mise"); err != nil {
		d.Warn("mise not found on PATH — skipping runtime install (repo pins %s)", join(a.VersionFiles))
		return nil
	}
	d.Log("running: mise install")
	if err := d.RunInDir(repoDir, "mise install"); err != nil {
		return fmt.Errorf("mise install failed: %w", err)
	}
	return nil
}

func runDatabase(a Action, repoDir string, d Deps) error {
	d.Log("database: template %q", a.DBTemplate)
	exists, err := d.DBExists(a.DBTemplate)
	if err != nil {
		return err
	}
	if exists {
		d.Log("template %q already exists — skipping", a.DBTemplate)
		return nil
	}

	switch a.DBMode {
	case DBModeSource:
		d.Log("hydrating %q from source %q", a.DBTemplate, a.DBSource)
		return d.HydrateFromSource(a.DBTemplate, a.DBSource)
	case DBModeHydrate:
		d.Log("creating empty template %q", a.DBTemplate)
		if err := d.CreateDB(a.DBTemplate); err != nil {
			return err
		}
		d.Log("running: %s", a.DBHydrate)
		if err := d.RunInDir(repoDir, a.DBHydrate); err != nil {
			return fmt.Errorf("hydrate command failed: %s: %w", a.DBHydrate, err)
		}
		return nil
	default:
		d.Log("creating empty template %q", a.DBTemplate)
		if err := d.CreateDB(a.DBTemplate); err != nil {
			return err
		}
		d.Warn("template %q created empty — worktree databases will be empty until a schema is loaded (set provision.database.source or provision.database.hydrate)", a.DBTemplate)
		return nil
	}
}

// missingPackages returns the subset of pkgs not already installed.
func missingPackages(pkgs []string, d Deps) ([]string, error) {
	var missing []string
	for _, p := range pkgs {
		ok, err := d.PackageInstalled(p)
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, p)
		}
	}
	return missing, nil
}

// PrintPlan writes the ordered plan without acting (--dry-run).
func PrintPlan(w io.Writer, actions []Action) {
	if len(actions) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing to provision.")
		return
	}
	_, _ = fmt.Fprintln(w, "Provision plan (dry run):")
	for _, a := range actions {
		line := "  - " + a.Summary()
		if a.PlatformSkip {
			line += "  [skip: " + a.SkipNote + "]"
		}
		_, _ = fmt.Fprintln(w, line)
	}
	_, _ = fmt.Fprintln(w, "  (no changes made)")
}

func join(s []string) string { return strings.Join(s, " ") }
