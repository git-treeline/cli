package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func TestManagedDevRedisSetupEnvironmentAndDrift(t *testing.T) {
	for _, mode := range []string{"valid", "prefix", "URL", "removed opt-in"} {
		t.Run(mode, func(t *testing.T) {
			s, a := managedDevSQLiteSetup(t)
			private := filepath.Join(os.Getenv("GTL_HOME"), "config.json")
			data, err := os.ReadFile(private)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(private, []byte(strings.ReplaceAll(string(data), "fixture/", "host_migration_qa/")), 0600); err != nil {
				t.Fatal(err)
			}
			body := "project: host_migration_qa\nport_count: 1\nenv: {REDIS_URL: '{redis_url}', REDIS_PREFIX: '{redis_prefix}'}\n"
			p := filepath.Join(s.WorktreePath, config.ProjectConfigFile)
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			s.ProjectConfig = config.LoadProjectConfigReadOnlyQuiet(s.WorktreePath)
			a.Project, a.DatabaseAdapter, a.Databases, a.RedisPrefix = "host_migration_qa", "", nil, "host_migration_qa:feature"
			url := "redis://localhost:6379"
			if mode == "prefix" {
				a.RedisPrefix = "host_migration_qa:sibling"
			}
			if mode == "URL" {
				url = "redis://remote.invalid:6379"
			}
			if mode == "removed opt-in" {
				if err := os.WriteFile(p, []byte("project: host_migration_qa\nport_count: 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = s.runPostAllocation(a, url)
			envPath := filepath.Join(s.WorktreePath, s.ProjectConfig.EnvFileTarget())
			if mode != "valid" {
				if err == nil {
					t.Fatal("drift admitted")
				}
				if _, err := os.Stat(envPath); !os.IsNotExist(err) {
					t.Fatal("env written on refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(envPath)
			if err != nil || !strings.Contains(string(data), "redis://localhost:6379") || !strings.Contains(string(data), "host_migration_qa:feature") || strings.Contains(string(data), "{redis_") {
				t.Fatal("Redis env not materialized", err)
			}
		})
	}
}
