// Package setup provides worktree provisioning orchestration.
// It coordinates resource allocation, database cloning, environment
// file generation, setup command execution, and editor configuration.
package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/editor"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/interpolation"
	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/provision"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/resolve"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
)

// SetupCommandError is returned by Run when the worktree and allocation
// are intact but a setup command (commands.setup) failed. The caller
// should keep the worktree and allocation — only the user's build
// commands failed, not the provisioning infrastructure.
type SetupCommandError struct {
	Alloc *allocator.Allocation
	Err   error
}

func (e *SetupCommandError) Error() string { return e.Err.Error() }
func (e *SetupCommandError) Unwrap() error { return e.Err }

// Options controls setup behavior. DryRun prints what would happen without
// making changes. RefreshOnly re-applies environment files without running
// setup commands or cloning databases. StrictDB restores the hard failure
// when the worktree database degrades (template missing, clone or fallback
// failed) instead of continuing with a warning — for callers that treat
// exit 0 as "environment fully ready".
type Options struct {
	DryRun      bool
	RefreshOnly bool
	StrictDB    bool
}

// HydrateTemplateFromSource hydrates a missing template database from a
// configured database.sources env, reusing the gtl db pull machinery. Wired
// by the cmd layer (it lives there to share `gtl provision`'s dump/restore
// path); nil means source hydration is unavailable in this context and the
// database ladder falls through to the empty-database fallback.
var HydrateTemplateFromSource func(pc *config.ProjectConfig, template, sourceEnv string) error

// DBDegradation records that worktree setup finished without a usable
// database clone: the database is either empty (fallback created) or absent
// (even the fallback failed). Setup continues past it by design — a worktree
// with a degraded database is more useful than no worktree — unless
// Options.StrictDB is set. Exposed so non-CLI surfaces (MCP) can report it.
type DBDegradation struct {
	Database string // allocated worktree database name
	State    string // "empty" | "absent"
	Reason   string
}

// Message renders the one-paragraph warning shared by the CLI block and the
// MCP result payload: state, cause, recovery, and the no-self-heal notice.
// The recovery line is deliberately generic — the reason names the specific
// remedy (gtl provision, a config key, manual creation) per ladder branch.
func (d *DBDegradation) Message() string {
	state := "was created empty (no schema, no data)"
	if d.State == "absent" {
		state = "could not be created"
	}
	return fmt.Sprintf(
		"Database %s %s: %s. This will not self-heal on later runs. Once the template database exists, run 'gtl db reset' in this worktree to re-clone from it.",
		d.Database, state, d.Reason)
}

// RegistryPath overrides the default registry location. Empty uses the
// standard path. Exposed so tests (and the MCP server) can inject a
// temporary registry without affecting global state at runtime.
var RegistryPath string

// Setup orchestrates worktree provisioning. It combines allocation, database
// cloning, environment file generation, and setup command execution.
type Setup struct {
	WorktreePath  string
	MainRepo      string
	UserConfig    *config.UserConfig
	ProjectConfig *config.ProjectConfig
	Registry      *registry.Registry
	Allocator     *allocator.Allocator
	Log           io.Writer
	Options       Options
	Resolver      interpolation.ResolveFunc

	// DBDegradation is set when the database step degraded instead of
	// failing (nil on a healthy run). Populated by Run; read by callers
	// that surface the warning outside the log stream (MCP).
	DBDegradation *DBDegradation
}

// New creates a Setup that loads .treeline.yml from the worktree path, not the
// main repo — branch-specific config is respected. The mainRepo path is still
// used for copy_files source and SQLite template paths.
//
// All callers operate on worktrees that already exist (either just created by
// git worktree add, or resuming an existing one). The initial project detection
// before worktree creation happens in the cmd layer (e.g. cmd/new.go loads
// config from mainRepo to get the project name, then passes the worktree path
// here after creation).
func New(worktreePath string, mainRepo string, uc *config.UserConfig) *Setup {
	absPath, _ := filepath.Abs(worktreePath)
	if mainRepo == "" {
		mainRepo = worktree.DetectMainRepo(absPath)
	}

	pc := config.LoadProjectConfig(absPath)
	reg := registry.New(RegistryPath)
	al := allocator.New(uc, pc, reg)

	return &Setup{
		WorktreePath:  absPath,
		MainRepo:      mainRepo,
		UserConfig:    uc,
		ProjectConfig: pc,
		Registry:      reg,
		Allocator:     al,
		Log:           os.Stdout,
	}
}

func (s *Setup) Run() (*allocator.Allocation, error) {
	if err := s.ProjectConfig.Validate(); err != nil {
		return nil, err
	}
	if err := s.handleProjectRename(); err != nil {
		return nil, err
	}
	if pruned, err := s.Registry.Prune(); err == nil && pruned > 0 {
		s.log("Reclaimed %d stale allocation(s)", pruned)
	}

	worktreeName := filepath.Base(s.WorktreePath)
	isMain := s.WorktreePath == s.MainRepo
	branch := s.detectBranch()
	resolverPkg := resolve.New(s.Registry, s.WorktreePath, branch)
	s.Resolver = resolverPkg.Resolve
	hadExisting := s.Registry.Find(s.WorktreePath) != nil
	s.Allocator.DryRun = s.Options.DryRun
	alloc, err := s.Allocator.Allocate(s.WorktreePath, worktreeName, isMain, branch)
	if err != nil {
		return nil, err
	}

	alloc.Branch = branch
	redisURL := s.Allocator.BuildRedisURL(alloc)

	if s.Options.DryRun {
		return alloc, s.printDryRun(alloc, redisURL)
	}

	if alloc.Reused {
		if alloc.Branch != "" {
			_ = s.Registry.UpdateField(s.WorktreePath, "branch", alloc.Branch)
		}
		s.log("Reusing existing allocation for '%s'", worktreeName)
	} else if hadExisting && !alloc.Reused {
		if len(alloc.Ports) > 1 {
			s.log("Previous ports were in use by another process, re-allocated to %s for '%s'", format.JoinInts(alloc.Ports, ", "), worktreeName)
		} else {
			s.log("Previous port was in use by another process, re-allocated to %d for '%s'", alloc.Port, worktreeName)
		}
	} else if len(alloc.Ports) > 1 {
		s.log("Allocating ports %s for '%s'", format.JoinInts(alloc.Ports, ", "), worktreeName)
	} else {
		s.log("Allocating port %d for '%s'", alloc.Port, worktreeName)
	}
	// Both main and non-main allocations are now persisted atomically inside the
	// allocator's registry transaction, so there is nothing to write here.
	if err := s.runPostAllocation(alloc, redisURL); err != nil {
		var sce *SetupCommandError
		if errors.As(err, &sce) {
			// Setup commands failed but allocation + worktree are intact.
			// Populate the alloc reference for the caller and skip release.
			// A degraded database is very likely WHY the commands failed
			// (migrations against an empty DB), so the warning must not be
			// lost on this path.
			if s.DBDegradation != nil {
				s.printDBDegradation()
			}
			sce.Alloc = alloc
			return nil, sce
		}
		if !alloc.Reused {
			_, _ = s.Registry.Release(s.WorktreePath)
			s.log("Rolled back allocation due to error")
		}
		return nil, err
	}

	_, _ = fmt.Fprintln(s.Log)
	_, _ = fmt.Fprintln(s.Log, style.Successf("Done!")+" Worktree '"+worktreeName+"' ready:")
	if len(alloc.Ports) > 1 {
		_, _ = fmt.Fprintln(s.Log, style.Dimf("  Ports:    %s", format.JoinInts(alloc.Ports, ", ")))
	} else {
		_, _ = fmt.Fprintln(s.Log, style.Dimf("  Port:     %d", alloc.Port))
	}
	if db := alloc.PrimaryDatabase(); db != "" {
		_, _ = fmt.Fprintln(s.Log, style.Dimf("  Database: %s", db))
	}
	_, _ = fmt.Fprintln(s.Log, style.Dimf("  Redis:    %s", redisURL))
	_, _ = fmt.Fprintln(s.Log, style.Dimf("  Local:    http://localhost:%d", alloc.Port))
	if s.UserConfig.RouterMode() != config.RouterModeDisabled && service.IsRunning() {
		routerURL := proxy.BuildRouterURL(0, s.ProjectConfig.Project(), branch, s.UserConfig.RouterDomain(), s.UserConfig.RouterPort(), true, service.IsPortForwardConfigured())
		_, _ = fmt.Fprintln(s.Log, style.Dimf("  Router:   %s", routerURL))
	}
	_, _ = fmt.Fprintln(s.Log, style.Dimf("  Dir:      %s", s.WorktreePath))

	// After the summary, so "Database: x" in the block above is never the
	// last word on a database that is actually empty or absent.
	if s.DBDegradation != nil {
		s.printDBDegradation()
	}

	return alloc, nil
}

func (s *Setup) runPostAllocation(alloc *allocator.Allocation, redisURL string) error {
	s.copyFiles()

	interpMap := alloc.ToInterpolationMap()
	envVars, err := s.buildEnvVars(interpMap, redisURL)
	if err != nil {
		return fmt.Errorf("resolving env vars: %w", err)
	}
	if err := s.writeEnvFile(envVars); err != nil {
		return fmt.Errorf("writing env file: %w", err)
	}

	if s.Options.RefreshOnly {
		s.configureEditor(alloc)
		return nil
	}

	if alloc.PrimaryDatabase() != "" && !alloc.Reused {
		if s.ProjectConfig.DatabaseSyncOnCreate() {
			if err := s.syncTemplateDatabase(); err != nil {
				return err
			}
		}
		if err := s.cloneDatabase(alloc); err != nil {
			return err
		}
	}

	if err := s.runHooks("pre_setup"); err != nil {
		return err
	}

	if err := s.runSetupCommands(); err != nil {
		return &SetupCommandError{Err: err}
	}

	s.configureEditor(alloc)

	if err := s.runHooks("post_setup"); err != nil {
		s.warn("post_setup hook failed: %s", err)
	}

	return nil
}

func (s *Setup) printDryRun(alloc *allocator.Allocation, redisURL string) error {
	worktreeName := filepath.Base(s.WorktreePath)

	if alloc.Reused {
		s.log("[dry-run] Would reuse existing allocation for '%s'", worktreeName)
	} else {
		s.log("[dry-run] Would allocate for '%s'", worktreeName)
	}

	if len(alloc.Ports) > 1 {
		s.detail("  Ports:    %s", format.JoinInts(alloc.Ports, ", "))
	} else {
		s.detail("  Port:     %d", alloc.Port)
	}
	if db := alloc.PrimaryDatabase(); db != "" {
		s.detail("  Database: %s", db)
	}
	s.detail("  Redis:    %s", redisURL)
	s.detail("  Dir:      %s", s.WorktreePath)

	interpMap := alloc.ToInterpolationMap()
	envVars, _ := s.buildEnvVars(interpMap, redisURL)
	s.detail("  Env vars:")
	for k, v := range envVars {
		s.detail("    %s=%s", k, v)
	}

	return nil
}

func (s *Setup) copyFiles() {
	for _, file := range s.ProjectConfig.CopyFiles() {
		src := filepath.Join(s.MainRepo, file)
		dest := filepath.Join(s.WorktreePath, file)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		_ = os.WriteFile(dest, data, 0o644)
		s.log("Copied %s", file)
	}
}

func (s *Setup) buildEnvVars(alloc interpolation.Allocation, redisURL string) (map[string]string, error) {
	branch, _ := alloc["branch"].(string)
	routerDomain := s.UserConfig.RouterDomain()
	if s.UserConfig.RouterMode() == config.RouterModeDisabled {
		routerDomain = ""
	}
	InjectRouterTokens(alloc, s.ProjectConfig.Project(), branch, routerDomain, s.UserConfig.TunnelDomain(""))
	if s.Resolver != nil {
		return BuildEnvVarsWithResolver(s.ProjectConfig, alloc, redisURL, s.Resolver)
	}
	return BuildEnvVars(s.ProjectConfig, alloc, redisURL), nil
}

// InjectRouterTokens adds router_url, router_host, tunnel_host, and
// tunnel_url to an allocation map so the corresponding env tokens can be
// resolved. router_* tokens are blank when the local router is disabled;
// tunnel_* tokens are blank when no tunnel domain is configured.
// router_domain is also populated as a backwards-compatible alias for
// router_host.
func InjectRouterTokens(alloc interpolation.Allocation, project, branch, routerDomain, tunnelDomain string) {
	routeKey := proxy.RouteKey(project, branch)

	if routerDomain == "" {
		alloc["router_url"] = ""
		alloc["router_host"] = ""
		alloc["router_domain"] = ""
	} else {
		alloc["router_url"] = fmt.Sprintf("https://%s.%s", routeKey, routerDomain)
		alloc["router_host"] = routerDomain
		alloc["router_domain"] = routerDomain
	}

	if tunnelDomain != "" {
		alloc["tunnel_host"] = tunnelDomain
		alloc["tunnel_url"] = fmt.Sprintf("https://%s.%s", routeKey, tunnelDomain)
	}
}

// BuildEnvVars resolves the env template from a project config against an
// allocation. Exported so gtl start can inject vars into the child process
// without going through a full Setup.
func BuildEnvVars(pc *config.ProjectConfig, alloc interpolation.Allocation, redisURL string) map[string]string {
	tmpl := pc.EnvTemplate()
	result := make(map[string]string, len(tmpl))
	for key, pattern := range tmpl {
		result[key] = interpolation.Interpolate(pattern, alloc, redisURL, pc.Project())
	}
	return result
}

// BuildEnvVarsWithResolver resolves env templates including {resolve:...}
// cross-worktree tokens. Returns an error if any resolve target is missing.
func BuildEnvVarsWithResolver(pc *config.ProjectConfig, alloc interpolation.Allocation, redisURL string, resolver interpolation.ResolveFunc) (map[string]string, error) {
	tmpl := pc.EnvTemplate()
	result := make(map[string]string, len(tmpl))
	for key, pattern := range tmpl {
		val, err := interpolation.InterpolateWithResolver(pattern, alloc, redisURL, pc.Project(), resolver)
		if err != nil {
			return nil, err
		}
		result[key] = val
	}
	return result, nil
}

// RegenerateEnvFile re-resolves env vars (including {resolve:...} tokens) and
// rewrites the env file for an existing allocation. Used by gtl link/unlink to
// immediately apply link changes without running full setup.
func RegenerateEnvFile(worktreePath string, uc *config.UserConfig) error {
	absPath, _ := filepath.Abs(worktreePath)
	// Load from worktree (not mainRepo) so branch-specific config is respected
	pc := config.LoadProjectConfig(absPath)
	reg := registry.New(RegistryPath)

	allocMap := reg.Find(absPath)
	if allocMap == nil {
		return nil
	}

	interpAlloc := interpolation.Allocation(allocMap)
	branch, _ := allocMap["branch"].(string)

	InjectRouterTokens(interpAlloc, pc.Project(), branch, uc.RouterDomain(), uc.TunnelDomain(""))

	redisURL := interpolation.BuildRedisURL(uc.RedisURL(), interpAlloc)

	resolverPkg := resolve.New(reg, absPath, branch)

	envVars, err := BuildEnvVarsWithResolver(pc, interpAlloc, redisURL, resolverPkg.Resolve)
	if err != nil {
		return fmt.Errorf("resolving env vars: %w", err)
	}

	target := pc.EnvFileTarget()
	envPath := filepath.Join(absPath, target)

	for key, value := range envVars {
		if err := updateOrAppend(envPath, key, value); err != nil {
			return err
		}
	}

	return nil
}

func (s *Setup) writeEnvFile(vars map[string]string) error {
	target := s.ProjectConfig.EnvFileTarget()
	envPath := filepath.Join(s.WorktreePath, target)

	// Seed from the main repo's env file only on first provisioning. On a
	// re-run (setup/refresh) the worktree env already exists and may hold
	// manual edits — copying the seed over it would destroy them before the
	// updateOrAppend pass below re-applies gtl's vars. Update-in-place instead.
	if _, err := os.Stat(envPath); err != nil {
		source := filepath.Join(s.MainRepo, s.ProjectConfig.EnvFileSource())
		if _, err := os.Stat(source); err != nil {
			source = filepath.Join(s.MainRepo, ".env")
		}
		if data, err := os.ReadFile(source); err == nil {
			_ = platform.AtomicWriteFile(envPath, data, 0o644)
		}
	}

	for key, value := range vars {
		if err := updateOrAppend(envPath, key, value); err != nil {
			return err
		}
	}

	s.log("%s written", target)
	return nil
}

func updateOrAppend(file, key, value string) error {
	if _, err := os.Stat(file); err != nil {
		_ = os.WriteFile(file, []byte{}, 0o644)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	content := string(data)
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	line := fmt.Sprintf(`%s="%s"`, key, escaped)
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*$`)

	if re.MatchString(content) {
		content = re.ReplaceAllString(content, line)
	} else {
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += line + "\n"
	}

	return platform.AtomicWriteFile(file, []byte(content), 0o644)
}

func (s *Setup) syncTemplateDatabase() error {
	migrateCmd := s.ProjectConfig.MigrateCommand()
	if migrateCmd == "" {
		s.warn("database.sync_on_create is set but commands.migrate is not configured in .treeline.yml — skipping template sync")
		return nil
	}

	mergeTarget := s.ProjectConfig.MergeTarget()
	if mergeTarget == "" {
		mergeTarget = "main"
	}

	s.log("Syncing template database (pulling %s + migrating)", mergeTarget)

	pull := exec.Command("git", "pull", "origin", mergeTarget)
	pull.Dir = s.MainRepo
	pull.Stdout = s.Log
	pull.Stderr = s.Log
	if err := pull.Run(); err != nil {
		return fmt.Errorf("git pull in root repo: %w", err)
	}

	migrate := exec.Command("sh", "-c", migrateCmd)
	migrate.Dir = s.MainRepo
	migrate.Stdout = s.Log
	migrate.Stderr = s.Log
	if err := migrate.Run(); err != nil {
		return fmt.Errorf("migrate command failed: %w", err)
	}

	return nil
}

func (s *Setup) handleProjectRename() error {
	// Never treat an unreadable or unparseable config as a rename: Project()
	// falls back to the directory name in that case, which would look like a
	// rename and drop the recorded database. Refuse rather than risk data loss.
	if err := s.ProjectConfig.LoadError(); err != nil {
		return err
	}
	entry := s.Registry.Find(s.WorktreePath)
	if entry == nil {
		return nil
	}
	entryProject := registry.GetString(entry, "project")
	if entryProject == s.ProjectConfig.Project() {
		return nil
	}
	oldDB := registry.GetString(entry, "database")
	template := s.ProjectConfig.DatabaseTemplate()
	isMain := s.WorktreePath == s.MainRepo
	switch {
	case oldDB != "" && (oldDB == template || isMain):
		// A template database is never dropped on rename. For a main repo the
		// registry's "database" IS the template (allocateMain stores
		// DatabaseTemplate() verbatim), and the project name doesn't feed into
		// the template's name — so a rename records the same DB again and the
		// drop would only destroy the clone source cloneDatabase needs next.
		// The name compare catches a worktree whose pattern resolved to the
		// template; the isMain check catches a main repo whose template config
		// was renamed in the same edit (stored name no longer matches). Keep it
		// and just re-provision — cloneDatabase skips when the DB already exists.
		s.log("Project renamed %s → %s, keeping template database %s (use `gtl rename` for a proper project rename)", entryProject, s.ProjectConfig.Project(), oldDB)
		s.dropStaleExtraDatabases(entry)
	case oldDB != "":
		s.log("Project renamed %s → %s, dropping database %s", entryProject, s.ProjectConfig.Project(), oldDB)
		adapterName := registry.GetString(entry, "database_adapter")
		if adapter, err := database.ForAdapter(adapterName, s.ProjectConfig.DatabaseConnArgs()); err == nil {
			dbPath := oldDB
			if adapterName == "sqlite" {
				dbPath = filepath.Join(s.WorktreePath, oldDB)
			}
			if exists, err := adapter.Exists(dbPath); err == nil && exists {
				if err := adapter.Drop(dbPath); err != nil {
					s.warn("failed to drop %s: %v", oldDB, err)
				}
			}
		}
		s.dropStaleExtraDatabases(entry)
	default:
		s.log("Project renamed %s → %s, re-provisioning", entryProject, s.ProjectConfig.Project())
	}
	if _, err := s.Registry.Release(s.WorktreePath); err != nil {
		return fmt.Errorf("releasing stale registry entry: %w", err)
	}
	return nil
}

// dropStaleExtraDatabases drops the auxiliary databases of a stale registry
// entry that is about to be discarded. Extras are framework-built and
// disposable; once the entry is released nothing tracks them anymore, so
// leaving them behind would orphan them permanently.
func (s *Setup) dropStaleExtraDatabases(entry registry.Allocation) {
	names := registry.ExtractDatabases(entry)
	if len(names) < 2 {
		return
	}
	adapterName := registry.GetString(entry, "database_adapter")
	adapter, err := database.ForAdapter(adapterName, s.ProjectConfig.DatabaseConnArgs())
	if err != nil {
		s.warn("cannot drop auxiliary databases %s: %v", strings.Join(names[1:], ", "), err)
		return
	}
	for _, name := range names[1:] {
		if s.ProjectConfig.IsTemplateDatabase(name) {
			s.log("Keeping template database %s (clone source)", name)
			continue
		}
		dbPath := name
		if adapterName == "sqlite" {
			dbPath = filepath.Join(s.WorktreePath, name)
		}
		s.log("Dropping auxiliary database %s", name)
		if err := adapter.Drop(dbPath); err != nil {
			s.warn("failed to drop %s: %v", name, err)
		}
	}
}

func (s *Setup) cloneDatabase(alloc *allocator.Allocation) error {
	adapterName := s.ProjectConfig.DatabaseAdapter()
	adapter, err := database.ForAdapter(adapterName, s.ProjectConfig.DatabaseConnArgs())
	if err != nil {
		return err
	}
	return s.cloneDatabaseWith(adapter, adapterName, alloc)
}

// cloneDatabaseWith is the database ladder: clone from the template when it
// exists; when it doesn't, try to provision it (same idempotent step as
// `gtl provision`); failing that, create an empty database with the allocated
// name; failing even that, record the degradation and let setup continue.
// Database trouble never fails worktree creation unless Options.StrictDB is
// set — the same boundary SetupCommandError draws for user commands.
// Split from cloneDatabase so tests can inject a fake adapter.
func (s *Setup) cloneDatabaseWith(adapter database.Adapter, adapterName string, alloc *allocator.Allocation) error {
	template := s.ProjectConfig.DatabaseTemplate()
	if template == "" {
		return nil
	}

	primary := alloc.PrimaryDatabase()
	target := primary

	// SQLite uses file paths relative to the worktree/main repo
	if adapterName == "sqlite" {
		target = filepath.Join(s.WorktreePath, primary)
		template = filepath.Join(s.MainRepo, template)
	}

	exists, err := adapter.Exists(target)
	if err != nil {
		return s.degradeDatabase(primary, "absent", fmt.Sprintf("cannot check for database %s: %v", primary, err))
	}
	if exists {
		s.log("Database %s already exists, skipping", primary)
		return nil
	}

	templateExists, err := adapter.Exists(template)
	if err != nil {
		return s.degradeDatabase(primary, "absent", fmt.Sprintf("cannot check for template database %s: %v", s.ProjectConfig.DatabaseTemplate(), err))
	}
	if !templateExists {
		mode, err := s.provisionTemplate(adapter, adapterName, template)
		if err != nil {
			return s.fallbackEmptyDatabase(adapter, primary, target, err.Error())
		}
		if mode == provision.DBModeEmpty {
			// The template now exists but holds no schema or data, so the
			// clone below produces an equally empty worktree database —
			// record that so no surface reports a healthy clone.
			if err := s.degradeDatabase(primary, "empty", fmt.Sprintf("template database %s was created empty (set provision.database.source or provision.database.hydrate to fill it)", s.ProjectConfig.DatabaseTemplate())); err != nil {
				return err
			}
		}
	}

	s.log("Cloning database %s → %s", s.ProjectConfig.DatabaseTemplate(), primary)
	if err := adapter.Clone(template, target); err != nil {
		return s.fallbackEmptyDatabase(adapter, primary, target, err.Error())
	}
	return nil
}

// provisionTemplate brings a missing template database into existence via the
// provision database step, returning the mode it used. A nil error means the
// template exists now; an error explains why it still doesn't (the ladder
// falls through to the empty-database fallback with that reason).
func (s *Setup) provisionTemplate(adapter database.Adapter, adapterName, template string) (provision.DBMode, error) {
	templateName := s.ProjectConfig.DatabaseTemplate()
	if adapterName != "postgresql" {
		// Provision's database step drives pg tooling only (see gtl provision's
		// adapter guard); for SQLite a missing template file has no auto path.
		return "", fmt.Errorf("template database %s does not exist", templateName)
	}

	cfg := s.ProjectConfig.Provision()
	if !cfg.Present {
		// No provision: section — the repo never opted into gtl creating the
		// template. Creating it empty here would poison every later run: the
		// template would exist, clone cleanly, and no run would warn again.
		return "", fmt.Errorf("template database %s does not exist and no provision: section is configured (create it manually or add provision.database to .treeline.yml)", templateName)
	}
	if cfg.Database.Template != templateName {
		// provision: creates a different database than the one we clone from;
		// running it wouldn't help.
		return "", fmt.Errorf("template database %s does not exist", templateName)
	}
	if cfg.Database.Source != "" {
		if !cfg.Database.Auto {
			return "", fmt.Errorf("template database %s does not exist and hydrates from remote source %q, which is not run automatically (run 'gtl provision', or set provision.database.auto: true)", templateName, cfg.Database.Source)
		}
		if HydrateTemplateFromSource == nil {
			return "", fmt.Errorf("template database %s does not exist and source hydration is unavailable here (run 'gtl provision')", templateName)
		}
	}

	action := databaseAction(cfg)
	if action == nil {
		return "", fmt.Errorf("template database %s does not exist", templateName)
	}
	if s.Options.StrictDB && action.DBMode == provision.DBModeEmpty {
		// Provisioning could only produce an empty template, which strict mode
		// rejects anyway — fail before mutating the host.
		return "", fmt.Errorf("template database %s does not exist and provisioning would only create it empty", templateName)
	}

	// Same per-template serialization as Clone: a concurrent `gtl new` waits
	// here, then the re-probe below sees the template and skips.
	unlock, err := database.LockTemplate(templateName)
	if err != nil {
		return "", fmt.Errorf("template database %s does not exist (locking it failed: %v)", templateName, err)
	}
	defer unlock()

	// Re-probe under the lock: a concurrent run (or a manual createdb+migrate)
	// may have produced the template since the caller's check. Reporting the
	// planned mode for a template we didn't create would warn "created empty"
	// about a perfectly good database.
	if exists, err := adapter.Exists(templateName); err == nil && exists {
		return "", nil
	}

	s.log("Template database %s does not exist — provisioning", templateName)
	deps := provision.Deps{
		GOOS:     runtime.GOOS,
		DBExists: adapter.Exists,
		CreateDB: adapter.Create,
		RunInDir: func(dir, command string) error {
			cmd := exec.Command("sh", "-c", command)
			cmd.Dir = dir
			cmd.Stdout = s.Log
			cmd.Stderr = s.Log
			return cmd.Run()
		},
		HydrateFromSource: func(t, env string) error {
			return HydrateTemplateFromSource(s.ProjectConfig, t, env)
		},
		Log:  s.log,
		Warn: s.warn,
	}
	if err := provision.Run([]provision.Action{*action}, s.MainRepo, deps); err != nil {
		// The step may have created the template before failing (hydrate runs
		// after createdb; a source restore can die halfway). We know it did
		// not exist when we took the lock, so drop the partial remains —
		// leaving them would make every later run clone a broken template
		// without a warning.
		if exists, probeErr := adapter.Exists(templateName); probeErr == nil && exists {
			if dropErr := adapter.Drop(templateName); dropErr != nil {
				s.warn("could not remove partially provisioned template %s: %v", templateName, dropErr)
			} else {
				s.log("Removed partially provisioned template %s", templateName)
			}
		}
		return "", fmt.Errorf("provisioning template database %s failed: %v", templateName, err)
	}
	return action.DBMode, nil
}

// databaseAction extracts the database step from the provision plan, or nil
// when the config doesn't produce one.
func databaseAction(cfg config.ProvisionConfig) *provision.Action {
	for _, a := range provision.PlanConfig(cfg, runtime.GOOS) {
		if a.Kind == provision.ActionDatabase {
			return &a
		}
	}
	return nil
}

// fallbackEmptyDatabase creates an empty database with the allocated name so
// the worktree's env vars point at something real, then records the
// degradation. reason says why the template path didn't produce a clone.
func (s *Setup) fallbackEmptyDatabase(adapter database.Adapter, primary, target, reason string) error {
	if s.Options.StrictDB {
		return fmt.Errorf("database not cloned (--strict): %s", reason)
	}
	if err := adapter.Create(target); err != nil {
		return s.degradeDatabase(primary, "absent", fmt.Sprintf("%s; creating an empty database also failed: %v", reason, err))
	}
	s.log("Created empty database %s", primary)
	return s.degradeDatabase(primary, "empty", reason)
}

// degradeDatabase records a degraded database outcome and lets setup continue,
// or fails outright under StrictDB. An earlier recorded cause (e.g. "template
// created empty" before a failed clone) is kept, not overwritten — the first
// cause is usually the actionable one.
func (s *Setup) degradeDatabase(primary, state, reason string) error {
	if s.Options.StrictDB {
		return fmt.Errorf("database not cloned (--strict): %s", reason)
	}
	if prev := s.DBDegradation; prev != nil && prev.Database == primary {
		reason = prev.Reason + "; " + reason
	}
	s.DBDegradation = &DBDegradation{Database: primary, State: state, Reason: reason}
	return nil
}

// printDBDegradation renders the degradation warning as the last block of
// setup output, where it won't scroll away under setup-command noise.
func (s *Setup) printDBDegradation() {
	d := s.DBDegradation
	s.log("")
	if d.State == "absent" {
		s.warn("Database %s was NOT created — %s", d.Database, d.Reason)
	} else {
		s.warn("Database %s was created EMPTY (no schema, no data) — %s", d.Database, d.Reason)
	}
	s.warn("This will not self-heal on later runs.")
	s.warn("Once the template database exists, run 'gtl db reset' in this worktree to re-clone from it.")
}

func (s *Setup) runHooks(name string) error {
	hooks := s.ProjectConfig.Hooks()
	if hooks == nil {
		return nil
	}
	cmds, ok := hooks[name]
	if !ok || len(cmds) == 0 {
		return nil
	}
	return RunHookCommands(name, cmds, s.WorktreePath, func(f string, a ...any) {
		s.log(f, a...)
	})
}

// RunHookCommands executes a list of hook commands in the given directory.
// The log function receives formatted status messages. Returns on first failure.
func RunHookCommands(hookName string, cmds []string, dir string, log func(string, ...any)) error {
	for _, cmdStr := range cmds {
		if log != nil {
			log("Hook [%s]: %s", hookName, cmdStr)
		}
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hook %s failed: %s: %w", hookName, cmdStr, err)
		}
	}
	return nil
}

func (s *Setup) runSetupCommands() error {
	for _, cmdStr := range s.ProjectConfig.SetupCommands() {
		s.log("Running: %s", cmdStr)
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = s.WorktreePath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setup command failed: %s: %w", cmdStr, err)
		}
	}
	return nil
}

func (s *Setup) configureEditor(alloc *allocator.Allocation) {
	results := ConfigureEditor(s.WorktreePath, s.ProjectConfig, s.UserConfig, alloc.Port, alloc.Branch)
	for _, r := range results {
		if r.Err != nil {
			_, _ = fmt.Fprintln(s.Log, style.Warnf("%s: %v", r.Label, r.Err))
		} else if r.Path != "" {
			s.log("%s written to %s", r.Label, filepath.Base(r.Path))
		}
	}
}

// EditorResult captures the outcome of writing to one editor target.
type EditorResult struct {
	Label string
	Path  string
	Err   error
}

// ConfigureEditor resolves editor settings from project/user config and writes
// to all detected editor targets. Extracted so both gtl setup and gtl editor refresh
// can share the same logic.
func ConfigureEditor(worktreePath string, pc *config.ProjectConfig, uc *config.UserConfig, port int, branch string) []EditorResult {
	editorCfg := pc.Editor()
	if editorCfg == nil {
		return nil
	}

	project := pc.Project()
	routerDomain := uc.RouterDomain()
	routerURL := ""
	if uc.RouterMode() != config.RouterModeDisabled {
		routeKey := proxy.RouteKey(project, branch)
		routerURL = fmt.Sprintf("https://%s.%s", routeKey, routerDomain)
	}

	replacer := strings.NewReplacer(
		"{project}", project,
		"{port}", fmt.Sprintf("%d", port),
		"{branch}", branch,
		"{url}", fmt.Sprintf("http://localhost:%d", port),
		"{router_url}", routerURL,
	)

	title := ""
	if t := editorCfg["title"]; t != "" {
		title = replacer.Replace(t)
	}

	color := ""
	if c := editorCfg["color"]; c != "" {
		if c == "auto" {
			color = editor.ColorForBranch(branch)
		} else {
			color = c
		}
	}
	if uc := uc.EditorColor(project, branch); uc != "" {
		color = uc
	}

	theme := editorCfg["theme"]
	if ut := uc.EditorTheme(project, branch); ut != "" {
		theme = ut
	}

	if title == "" && color == "" && theme == "" {
		return nil
	}

	var results []EditorResult

	vsSettings := editor.VSCodeSettings{
		Title: title,
		Color: color,
		Theme: theme,
	}
	target, err := editor.WriteVSCode(worktreePath, vsSettings)
	results = append(results, EditorResult{Label: "Editor settings", Path: target, Err: err})

	if color != "" && editor.DetectJetBrains(worktreePath) {
		target, err := editor.WriteJetBrains(worktreePath, color)
		results = append(results, EditorResult{Label: "JetBrains project color", Path: target, Err: err})
	}

	return results
}

func (s *Setup) detectBranch() string {
	return worktree.CurrentBranch(s.WorktreePath)
}

func (s *Setup) log(format string, args ...any) {
	if format == "" {
		_, _ = fmt.Fprintln(s.Log)
		return
	}
	_, _ = fmt.Fprintln(s.Log, style.Actionf(format, args...))
}

// detail writes a subordinate line without the ==> prefix.
func (s *Setup) detail(format string, args ...any) {
	_, _ = fmt.Fprintf(s.Log, format+"\n", args...)
}

// warn writes a warning line using the Warning: prefix.
func (s *Setup) warn(format string, args ...any) {
	_, _ = fmt.Fprintln(s.Log, style.Warnf(format, args...))
}
