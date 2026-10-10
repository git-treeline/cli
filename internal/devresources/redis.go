package devresources

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/git-treeline/cli/internal/config"
)

const RedisProject = "host_migration_qa"
const RedisURL = "redis://localhost:6379"

var redisWorktreeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// RedisRequested requires both connection and namespace tokens; either alone
// cannot establish the prefixed fixture contract.
func RedisRequested(pc *config.ProjectConfig) (bool, error) {
	url, prefix := false, false
	for _, pattern := range pc.EnvTemplate() {
		url = url || strings.Contains(pattern, "{redis_url}")
		prefix = prefix || strings.Contains(pattern, "{redis_prefix}")
	}
	if url != prefix || (url && pc.Project() != RedisProject) {
		return false, fmt.Errorf("managed dev cannot derive {redis_url} without paired Redis tokens for the QA project: %w", refusal)
	}
	return url, nil
}

func RedisPrefix(pc *config.ProjectConfig, worktree string) (string, error) {
	requested, err := RedisRequested(pc)
	if err != nil || !requested {
		return "", err
	}
	if !filepath.IsAbs(worktree) || filepath.Clean(worktree) != worktree || !redisWorktreeName.MatchString(filepath.Base(worktree)) {
		return "", refusal
	}
	return RedisProject + ":" + filepath.Base(worktree), nil
}

func ValidateRedisBinding(pc *config.ProjectConfig, allocation map[string]any) error {
	worktree, _ := allocation["worktree"].(string)
	expected, err := RedisPrefix(pc, worktree)
	if err != nil {
		return err
	}
	actual := ""
	if value := allocation["redis_prefix"]; value != nil {
		var ok bool
		actual, ok = value.(string)
		if !ok {
			return refusal
		}
	}
	if actual != expected || allocation["redis_db"] != nil {
		return refusal
	}
	return nil
}

func validRedisPrefix(project, worktree, prefix string) bool {
	return project == RedisProject && filepath.IsAbs(worktree) && filepath.Clean(worktree) == worktree && redisWorktreeName.MatchString(filepath.Base(worktree)) && prefix == RedisProject+":"+filepath.Base(worktree)
}

func normalAllocationUsesRedisNamespace(allocation map[string]any) bool {
	prefix, _ := allocation["redis_prefix"].(string)
	return prefix == RedisProject || strings.HasPrefix(prefix, RedisProject+":")
}
