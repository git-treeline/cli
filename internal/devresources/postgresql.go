package devresources

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/git-treeline/cli/internal/config"
)

const PostgreSQLProject = "host_migration_qa"
const PostgreSQLTemplate = "treeline_dev_host_migration_qa_template"

var regexpUnsafePostgreSQL = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
var regexpPostgreSQLUnderscores = regexp.MustCompile(`_+`)

func validatePostgreSQLProject(pc *config.ProjectConfig) error {
	if pc.Project() != PostgreSQLProject || pc.DatabaseTemplate() != PostgreSQLTemplate {
		return refusal
	}
	args := pc.DatabaseConnArgs()
	if len(args) != 6 || (args[1] != "127.0.0.1" && args[1] != "localhost" && args[1] != "::1") || !identifier.MatchString(args[5]) {
		return refusal
	}
	switch config.Dig(pc.Data, "database", "port").(type) {
	case int, string:
	default:
		return refusal
	}
	port, err := strconv.Atoi(args[3])
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != args[3] {
		return refusal
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "PG") && key != "PGPASSWORD" && key != "PGPASSFILE" {
			return refusal
		}
	}
	return nil
}

func validPostgreSQLAllocation(project, worktree, name string, main bool) bool {
	if project != PostgreSQLProject || !filepath.IsAbs(worktree) || filepath.Clean(worktree) != worktree {
		return false
	}
	expected := PostgreSQLTemplate
	if !main {
		suffix := strings.Trim(regexpPostgreSQLUnderscores.ReplaceAllString(regexpUnsafePostgreSQL.ReplaceAllString(filepath.Base(worktree), "_"), "_"), "_")
		if suffix == "" {
			return false
		}
		expected += "_" + suffix
	}
	return name == expected && len(name) <= 63 && identifier.MatchString(name)
}

func normalAllocationUsesPostgreSQLNamespace(allocation map[string]any) bool {
	names := []any{allocation["database"]}
	if values, ok := allocation["databases"].([]any); ok {
		names = append(names, values...)
	}
	for _, raw := range names {
		if name, ok := raw.(string); ok && (name == PostgreSQLTemplate || strings.HasPrefix(name, PostgreSQLTemplate+"_")) {
			return true
		}
	}
	return false
}
