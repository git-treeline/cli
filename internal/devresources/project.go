package devresources

import (
	"path/filepath"
	"strings"

	"github.com/git-treeline/cli/internal/config"
)

// ValidateProject admits HTTP, SQLite, dedicated QA PostgreSQL and contained file-copy configuration without running hooks
// or interpreting application commands. Callers validate filesystem containment.
func ValidateProject(pc *config.ProjectConfig) error {
	if pc == nil || pc.Validate() != nil || !identifier.MatchString(pc.Project()) {
		return refusal
	}
	if count, ok := integer(pc.Data["port_count"]); !ok || count < 1 || count > increment {
		return refusal
	}
	for key, value := range pc.Data {
		switch key {
		case "project", "port_count", "env_file", "env", "commands":
		case "database":
			database, ok := value.(map[string]any)
			if !ok {
				return refusal
			}
			sqlite := database["adapter"] == "sqlite"
			postgres := pc.DatabaseAdapter() == "postgresql" && pc.DatabaseTemplate() != ""
			if postgres && validatePostgreSQLProject(pc) != nil {
				return refusal
			}
			if sqlite && !validSQLiteName(pc.DatabaseTemplate()) {
				return refusal
			}
			for name, entry := range database {
				switch name {
				case "adapter":
					if entry != "postgresql" && entry != "sqlite" {
						return refusal
					}
				case "name":
					if sqlite {
						pattern, ok := entry.(string)
						if !ok || !validSQLitePattern(pattern) {
							return refusal
						}
					} else if entry != "{template}_{worktree}" {
						return refusal
					}
				case "template":
					if !sqlite && !postgres && entry != nil && entry != "" {
						return refusal
					}
				case "host", "port", "user":
					if !postgres {
						return refusal
					}
				default:
					return refusal
				}
			}
		case "hooks", "editor", "redis", "router", "tunnel", "aliases":
			entries, ok := value.(map[string]any)
			if !ok || len(entries) != 0 {
				return refusal
			}
		case "copy_files":
			entries, ok := value.([]any)
			if !ok || len(entries) > 256 {
				return refusal
			}
			for _, entry := range entries {
				name, ok := entry.(string)
				if !ok || !validSQLiteName(name) {
					return refusal
				}
			}
		case "related":
			entries, ok := value.([]any)
			if !ok || len(entries) != 0 {
				return refusal
			}
		case "merge_target", "worktree_base":
			if value != "" {
				return refusal
			}
		default:
			return refusal
		}
	}
	if commands, present := pc.Data["commands"]; present {
		entries, ok := commands.(map[string]any)
		if !ok {
			return refusal
		}
		for key, value := range entries {
			if _, ok := value.(string); key != "start" || !ok {
				return refusal
			}
		}
	}
	if value, present := pc.Data["env_file"]; present {
		target, ok := value.(string)
		if !ok || target == "" || filepath.IsAbs(target) || filepath.Clean(target) != target || target == ".." || strings.HasPrefix(target, "../") || strings.ContainsAny(target, "\x00\r\n") {
			return refusal
		}
	}
	if value, present := pc.Data["env"]; present {
		entries, ok := value.(map[string]any)
		if !ok {
			return refusal
		}
		for key, value := range entries {
			_, ok := value.(string)
			if !ok || strings.ContainsAny(key, "\x00\r\n=") || key == "" {
				return refusal
			}
		}
	}
	_, err := RedisRequested(pc)
	return err
}
