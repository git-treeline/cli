// Package setup provides worktree provisioning orchestration.
// It coordinates resource allocation, database cloning, environment
// file generation, setup command execution, and editor configuration.
package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/git-treeline/cli/internal/allocator"
	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/database"
	"github.com/git-treeline/cli/internal/editor"
	"github.com/git-treeline/cli/internal/format"
	"github.com/git-treeline/cli/internal/interpolation"
	"github.com/git-treeline/cli/internal/proxy"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/resolve"
	"github.com/git-treeline/cli/internal/service"
	"github.com/git-treeline/cli/internal/style"
	"github.com/git-treeline/cli/internal/worktree"
)

// Options controls setup behavior. DryRun prints what would happen without
// making changes. RefreshOnly re-applies environment files without running
// setup commands or cloning databases.
type Options struct {
	DryRun      bool
	RefreshOnly bool
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
	if pruned, err := s.Registry.Prune(); err == nil && pruned > 0 {
		s.log("Reclaimed %d stale allocation(s)", pruned)
	}

	worktreeName := filepath.Base(s.WorktreePath)
	isMain := s.WorktreePath == s.MainRepo
	branch := s.detectBranch()
	resolverPkg := resolve.New(s.Registry, s.WorktreePath, branch)
	s.Resolver = resolverPkg.Resolve
	hadExisting := s.Registry.Find(s.WorktreePath) != nil
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
	if !alloc.Reused {
		if err := s.Registry.Allocate(alloc.ToRegistryEntry()); err != nil {
			return nil, fmt.Errorf("registering allocation: %w", err)
		}
	}

	if err := s.runPostAllocation(alloc, redisURL); err != nil {
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
	if alloc.Database != "" {
		_, _ = fmt.Fprintln(s.Log, style.Dimf("  Database: %s", alloc.Database))
	}
	_, _ = fmt.Fprintln(s.Log, style.Dimf("  Redis:    %s", redisURL))
	_, _ = fmt.Fprintln(s.Log, style.Dimf("  Local:    http://localhost:%d", alloc.Port))
	if s.UserConfig.RouterMode() != config.RouterModeDisabled && service.IsRunning() {
		routerURL := proxy.BuildRouterURL(0, s.ProjectConfig.Project(), branch, s.UserConfig.RouterDomain(), s.UserConfig.RouterPort(), true, service.IsPortForwardConfigured())
		_, _ = fmt.Fprintln(s.Log, style.Dimf("  Router:   %s", routerURL))
	}
	_, _ = fmt.Fprintln(s.Log, style.Dimf("  Dir:      %s", s.WorktreePath))

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

	if alloc.Database != "" && !alloc.Reused {
		if err := s.cloneDatabase(alloc); err != nil {
			return err
		}
	}

	if err := s.runHooks("pre_setup"); err != nil {
		return err
	}

	if err := s.runSetupCommands(); err != nil {
		return err
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
	if alloc.Database != "" {
		s.detail("  Database: %s", alloc.Database)
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

// InjectRouterTokens adds router_url, router_domain, and tunnel_host to an
// allocation map so the corresponding env tokens can be resolved.
func InjectRouterTokens(alloc interpolation.Allocation, project, branch, routerDomain, tunnelDomain string) {
	if routerDomain == "" {
		alloc["router_url"] = ""
		alloc["router_domain"] = ""
		if tunnelDomain != "" {
			alloc["tunnel_host"] = tunnelDomain
		}
		return
	}
	routeKey := proxy.RouteKey(project, branch)
	alloc["router_url"] = fmt.Sprintf("https://%s.%s", routeKey, routerDomain)
	alloc["router_domain"] = routerDomain
	if tunnelDomain != "" {
		alloc["tunnel_host"] = tunnelDomain
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

	source := filepath.Join(s.MainRepo, s.ProjectConfig.EnvFileSource())
	if _, err := os.Stat(source); err != nil {
		source = filepath.Join(s.MainRepo, ".env")
	}
	if data, err := os.ReadFile(source); err == nil {
		_ = os.WriteFile(envPath, data, 0o644)
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

	return os.WriteFile(file, []byte(content), 0o644)
}

func (s *Setup) cloneDatabase(alloc *allocator.Allocation) error {
	adapterName := s.ProjectConfig.DatabaseAdapter()
	adapter, err := database.ForAdapter(adapterName)
	if err != nil {
		return err
	}

	template := s.ProjectConfig.DatabaseTemplate()
	if template == "" {
		return nil
	}

	target := alloc.Database

	// SQLite uses file paths relative to the worktree/main repo
	if adapterName == "sqlite" {
		target = filepath.Join(s.WorktreePath, alloc.Database)
		template = filepath.Join(s.MainRepo, template)
	}

	exists, err := adapter.Exists(target)
	if err != nil {
		return err
	}
	if exists {
		s.log("Database %s already exists, skipping", alloc.Database)
		return nil
	}

	s.log("Cloning database %s → %s", s.ProjectConfig.DatabaseTemplate(), alloc.Database)
	if err := adapter.Clone(template, target); err != nil {
		return err
	}

	return nil
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
