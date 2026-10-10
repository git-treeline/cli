package setup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/envparse"
	"github.com/git-treeline/cli/internal/platform"
)

// TemplateConfigError identifies missing opt-in configuration. Automatic sync
// may skip these errors; failures after migration preparation remain fatal.
type TemplateConfigError struct {
	Message string
	Hint    string
}

func (e *TemplateConfigError) Error() string { return e.Message }

// UpdateTemplateDatabase migrates the root template using a detached checkout
// of the remote merge target, without allocating resources or moving branches.
func UpdateTemplateDatabase(ctx context.Context, mainRepo string, pc *config.ProjectConfig, log io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := pc.Validate(); err != nil {
		return err
	}
	if pc.MigrateCommand() == "" {
		return fmt.Errorf("no commands.migrate configured in .treeline.yml")
	}
	vars, err := templateDatabaseVars(mainRepo, pc)
	if err != nil {
		return err
	}
	if log == nil {
		log = io.Discard
	}
	return withTemplateCheckout(ctx, mainRepo, pc.MergeTarget(), log, func(path string) error {
		env, err := prepareTemplateMigration(path, mainRepo, pc, vars, log)
		if err != nil {
			return fmt.Errorf("preparing template migration: %w", err)
		}
		_, _ = fmt.Fprintf(log, "==> Migrating template %s: %s\n", pc.DatabaseTemplate(), pc.MigrateCommand())
		if err := runTemplateMigration(ctx, path, pc.MigrateCommand(), env, log); err != nil {
			return fmt.Errorf("template migration failed: %w (commands.migrate must prepare any dependencies needed in a fresh checkout)", err)
		}

		return nil
	})
}

func templateDatabaseVars(mainRepo string, pc *config.ProjectConfig) (map[string]string, error) {
	template := pc.DatabaseTemplate()
	if template == "" {
		return nil, &TemplateConfigError{
			Message: "no database.template configured in .treeline.yml",
			Hint:    "Set the root template database in .treeline.yml:\n  database:\n    template: myapp_development",
		}
	}
	if pc.DatabaseAdapter() == "sqlite" && !filepath.IsAbs(template) {
		var err error
		template, err = filepath.Abs(filepath.Join(mainRepo, template))
		if err != nil {
			return nil, fmt.Errorf("resolving template database path: %w", err)
		}
	}
	replacer := strings.NewReplacer("{database}", template, "{database.name}", template, "{database_1}", template)
	vars := make(map[string]string)
	bound := false
	for key, pattern := range pc.EnvTemplate() {
		if key == "SHADOW_DATABASE_URL" && hasTemplateDatabaseToken(pattern) {
			return nil, fmt.Errorf("env.SHADOW_DATABASE_URL must name a separate migration database, not database.template")
		}
		value := replacer.Replace(pattern)
		if value != pattern {
			if strings.ContainsAny(value, "{}") {
				return nil, fmt.Errorf("env.%s combines the template database with unresolved tokens; template migrations have no worktree allocation", key)
			}
			bound = true
			vars[key] = value
		} else if !strings.ContainsAny(pattern, "{}") {
			vars[key] = pattern
		}
	}
	if !bound {
		return nil, &TemplateConfigError{
			Message: "template migration requires an env mapping using {database}, {database.name}, or {database_1} so commands.migrate targets database.template",
			Hint:    "Map the variable your migration command reads for its primary database, for example:\n  env:\n    POSTGRES_DATABASE: \"{database}\"",
		}
	}
	return vars, nil
}

func prepareTemplateMigration(path, mainRepo string, pc *config.ProjectConfig, vars map[string]string, log io.Writer) ([]string, error) {
	for _, name := range append(pc.CopyFiles(), pc.EnvFileTarget()) {
		if err := checkTemplateDestination(path, name); err != nil {
			return nil, err
		}
	}
	s := &Setup{WorktreePath: path, MainRepo: mainRepo, ProjectConfig: pc, Log: log}
	if err := s.copyFiles(); err != nil {
		return nil, err
	}

	// Seed from the primary clone even if the fetched branch tracks an env
	// file. Worktree-specific values from the invoking shell must not win.
	source := filepath.Join(mainRepo, pc.EnvFileSource())
	data, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		source = filepath.Join(mainRepo, ".env")
		data, err = os.ReadFile(source)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading template environment: %w", err)
	}
	content := string(data)
	for _, key := range sortedEnvKeys(vars) {
		content = replaceOrAppendEnvAssignment(content, key, vars[key])
	}
	envPath := filepath.Join(path, pc.EnvFileTarget())
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return nil, err
	}
	// Ephemeral env files don't need persistent managed-env ownership records.
	if err := platform.AtomicWriteFile(envPath, []byte(content), platform.PrivateFileMode); err != nil {
		return nil, err
	}
	if err := checkTemplateDatabaseURLs(path, pc, vars); err != nil {
		return nil, err
	}

	var env []string
	managed := pc.EnvTemplate()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		_, overridden := vars[key]
		_, allocated := managed[key]
		if overridden || allocated || templateCallerVariable(key) {
			continue
		}
		if strings.HasSuffix(key, "_DATABASE_URL") {
			return nil, fmt.Errorf("inherited %s has no explicit migration target; configure env.%s for template migrations", key, key)
		}
		env = append(env, entry)
	}
	// Let the application's dotenv loader interpret the seed's quoting,
	// interpolation, comments, and multiline values. Only our explicit
	// overrides belong in the subprocess environment.
	for _, key := range sortedEnvKeys(vars) {
		env = append(env, key+"="+vars[key])
	}
	return env, nil
}

func templateCallerVariable(key string) bool {
	switch key {
	case "DATABASE_URL", "PGDATABASE", "POSTGRES_DATABASE", "MYSQL_DATABASE", "RAILS_ENV", "RACK_ENV", "NODE_ENV", "BUNDLE_GEMFILE", "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE":
		return true
	}
	return false
}

var templateEnvAssignment = regexp.MustCompile(`(?m)^[\t ]*(?:export[\t ]+)?([A-Za-z_][A-Za-z0-9_]*)[\t ]*=[\t ]*([^\r\n]*)`)
var templateEnvMode = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func hasTemplateDatabaseToken(pattern string) bool {
	return strings.Contains(pattern, "{database}") || strings.Contains(pattern, "{database.name}") || strings.Contains(pattern, "{database_1}")
}

// Dotenv consumers can load several files, regardless of env_file. Inspect
// their routing assignments without interpreting or exporting dotenv values.
func checkTemplateDatabaseURLs(root string, pc *config.ProjectConfig, vars map[string]string) error {
	files := []string{filepath.Clean(pc.EnvFileTarget())}
	for _, name := range []string{".env", ".env.local"} {
		if name != files[0] {
			files = append(files, name)
		}
	}
	baseCount := len(files)
	envTemplate := pc.EnvTemplate()
	addMode := func(key, value string) error {
		value = envparse.StripQuotes(strings.TrimSpace(strings.SplitN(value, "#", 2)[0]))
		if !templateEnvMode.MatchString(value) {
			return fmt.Errorf("cannot determine dotenv environment from %s; configure env.%s as a literal environment name for template migrations", key, key)
		}
		files = append(files, ".env."+value, ".env."+value+".local")
		return nil
	}
	explicitMode := false
	for _, key := range []string{"RAILS_ENV", "RACK_ENV", "NODE_ENV"} {
		if value, ok := vars[key]; ok {
			if err := addMode(key, value); err != nil {
				return err
			}
			explicitMode = true
		}
	}
	seen := make(map[string]bool)
	for i := 0; i < len(files); i++ {
		name := filepath.Clean(files[i])
		if seen[name] {
			continue
		}
		seen[name] = true
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("checking migration dotenv file %s: %w", name, err)
		}
		for _, match := range templateEnvAssignment.FindAllStringSubmatch(string(data), -1) {
			key := match[1]
			switch key {
			case "RAILS_ENV", "RACK_ENV", "NODE_ENV":
				if _, overridden := vars[key]; !overridden {
					if err := addMode(key, match[2]); err != nil {
						return err
					}
					explicitMode = true
				}
			case "DATABASE_URL":
				if !hasTemplateDatabaseToken(envTemplate[key]) {
					return fmt.Errorf("DATABASE_URL in %s is not bound to database.template; configure env.DATABASE_URL using {database}", name)
				}
			default:
				if strings.HasSuffix(key, "_DATABASE_URL") {
					if _, explicit := vars[key]; !explicit {
						return fmt.Errorf("%s in %s has no explicit migration target; configure env.%s for template migrations", key, name, key)
					}
				}
			}
		}
		// Without an explicit environment, common development dotenv files
		// are still candidates. Wait until the base files have been inspected.
		if i == baseCount-1 && !explicitMode {
			files = append(files, ".env.development", ".env.development.local")
		}
	}
	return nil
}

// Only the disposable checkout may receive copied files and env writes.
func checkTemplateDestination(root, name string) error {
	clean := filepath.Clean(name)
	if filepath.IsAbs(name) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("template preparation path must be inside the temporary checkout: %s", name)
	}
	path := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".git" {
			return fmt.Errorf("template preparation must not overwrite Git metadata: %s", name)
		}
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("template preparation must not write through a symlink: %s", name)
		}
	}
	return nil
}
