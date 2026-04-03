// Package setup provides worktree provisioning orchestration.
// It coordinates resource allocation, database cloning, environment
// file generation, setup command execution, and editor configuration.
package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/git-treeline/git-treeline/internal/allocator"
	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/database"
	"github.com/git-treeline/git-treeline/internal/format"
	"github.com/git-treeline/git-treeline/internal/interpolation"
	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/worktree"
)

// Options controls setup behavior. DryRun prints what would happen without
// making changes. RefreshOnly re-applies environment files without running
// setup commands or cloning databases.
type Options struct {
	DryRun      bool
	RefreshOnly bool
}

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
}

func New(worktreePath string, mainRepo string, uc *config.UserConfig) *Setup {
	absPath, _ := filepath.Abs(worktreePath)
	if mainRepo == "" {
		mainRepo = worktree.DetectMainRepo(absPath)
	}

	pc := config.LoadProjectConfig(mainRepo)
	reg := registry.New("")
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
	worktreeName := filepath.Base(s.WorktreePath)
	isMain := s.WorktreePath == s.MainRepo
	alloc, err := s.Allocator.Allocate(s.WorktreePath, worktreeName, isMain)
	if err != nil {
		return nil, err
	}

	alloc.Branch = s.detectBranch()
	redisURL := s.Allocator.BuildRedisURL(alloc)

	if s.Options.DryRun {
		return alloc, s.printDryRun(alloc, redisURL)
	}

	if alloc.Reused {
		if alloc.Branch != "" {
			_ = s.Registry.UpdateField(s.WorktreePath, "branch", alloc.Branch)
		}
		s.log("Reusing existing allocation for '%s'", worktreeName)
	} else if len(alloc.Ports) > 1 {
		s.log("Allocating ports %s for '%s'", format.JoinInts(alloc.Ports, ", "), worktreeName)
	} else {
		s.log("Allocating port %d for '%s'", alloc.Port, worktreeName)
	}
	if alloc.Database != "" {
		s.log("Database: %s", alloc.Database)
	}
	s.log("Redis: %s", redisURL)

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

	s.log("")
	s.log("Done! Worktree '%s' ready:", worktreeName)
	if len(alloc.Ports) > 1 {
		s.log("  Ports:    %s", format.JoinInts(alloc.Ports, ", "))
	} else {
		s.log("  Port:     %d", alloc.Port)
	}
	if alloc.Database != "" {
		s.log("  Database: %s", alloc.Database)
	}
	s.log("  Redis:    %s", redisURL)
	s.log("  URL:      http://localhost:%d", alloc.Port)
	s.log("  Dir:      %s", s.WorktreePath)

	return alloc, nil
}

func (s *Setup) runPostAllocation(alloc *allocator.Allocation, redisURL string) error {
	s.copyFiles()

	interpMap := alloc.ToInterpolationMap()
	envVars := s.buildEnvVars(interpMap, redisURL)
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

	if err := s.runSetupCommands(); err != nil {
		return err
	}

	s.configureEditor(alloc)
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
		s.log("  Ports:    %s", format.JoinInts(alloc.Ports, ", "))
	} else {
		s.log("  Port:     %d", alloc.Port)
	}
	if alloc.Database != "" {
		s.log("  Database: %s", alloc.Database)
	}
	s.log("  Redis:    %s", redisURL)
	s.log("  Dir:      %s", s.WorktreePath)

	interpMap := alloc.ToInterpolationMap()
	envVars := s.buildEnvVars(interpMap, redisURL)
	s.log("  Env vars:")
	for k, v := range envVars {
		s.log("    %s=%s", k, v)
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

func (s *Setup) buildEnvVars(alloc interpolation.Allocation, redisURL string) map[string]string {
	return BuildEnvVars(s.ProjectConfig, alloc, redisURL)
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
	line := fmt.Sprintf(`%s="%s"`, key, value)
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

	s.log("Cloning database %s -> %s", s.ProjectConfig.DatabaseTemplate(), alloc.Database)
	if err := adapter.Clone(template, target); err != nil {
		return err
	}

	s.log("Database cloned")
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
	editor := s.ProjectConfig.Editor()
	if editor == nil {
		return
	}
	titleTemplate, ok := editor["vscode_title"]
	if !ok || titleTemplate == "" {
		return
	}

	title := strings.NewReplacer(
		"{project}", s.ProjectConfig.Project(),
		"{port}", fmt.Sprintf("%d", alloc.Port),
		"{branch}", alloc.Branch,
	).Replace(titleTemplate)

	vscodeDir := filepath.Join(s.WorktreePath, ".vscode")
	_ = os.MkdirAll(vscodeDir, 0o755)
	settings := map[string]string{"window.title": title}
	data, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.WriteFile(filepath.Join(vscodeDir, "settings.json"), append(data, '\n'), 0o644)
	s.log(".vscode/settings.json written")
}

func (s *Setup) detectBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = s.WorktreePath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Setup) log(format string, args ...any) {
	if format == "" {
		_, _ = fmt.Fprintln(s.Log)
		return
	}
	_, _ = fmt.Fprintf(s.Log, "==> "+format+"\n", args...)
}
