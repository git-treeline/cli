package devresources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

const redisConfig = "project: host_migration_qa\nenv: {REDIS_URL: '{redis_url}', REDIS_PREFIX: '{redis_prefix}'}\n"

func TestManagedDevRedisOptInAndBinding(t *testing.T) {
	for name, body := range map[string]string{
		"paired":               redisConfig,
		"http":                 "project: host_migration_qa\n",
		"URL only":             "project: host_migration_qa\nenv: {REDIS_URL: '{redis_url}'}\n",
		"prefix only":          "project: host_migration_qa\nenv: {REDIS_PREFIX: '{redis_prefix}'}\n",
		"other project":        strings.Replace(redisConfig, RedisProject, "production", 1),
		"project Redis config": redisConfig + "redis: {url: 'redis://localhost:6379'}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			pc := config.LoadProjectConfigReadOnlyQuiet(root)
			if err := ValidateProject(pc); (err == nil) != (name == "paired" || name == "http") {
				t.Fatal("project admission", err)
			}
			if name != "paired" {
				return
			}
			p := &Policy{reservations: map[string]int{RedisProject + "/qa-one": 42010}}
			for _, variant := range []string{"valid", "missing", "sibling", "database", "main"} {
				a := map[string]any{"project": RedisProject, "branch": "qa-one", "worktree": "/private/qa-one", "port": 42010, "redis_prefix": RedisProject + ":qa-one"}
				switch variant {
				case "missing":
					delete(a, "redis_prefix")
				case "sibling":
					a["redis_prefix"] = RedisProject + ":qa-two"
				case "database":
					a["redis_db"] = 1
				case "main":
					a["main_worktree"] = true
				}
				err := ValidateRedisBinding(pc, a)
				if (err == nil) != (variant == "valid" || variant == "main") {
					t.Fatal(variant, err)
				}
				if variant != "missing" {
					if err := p.ValidateAllocation(a); (err == nil) != (variant == "valid" || variant == "main") {
						t.Fatal("policy", variant, err)
					}
				}
			}
		})
	}
}

func TestManagedDevRedisNormalNamespaceProtected(t *testing.T) {
	for _, prefix := range []string{"host_migration_qa:qa-one", "host_migration_qa", "ordinary:qa-one"} {
		t.Run(prefix, func(t *testing.T) {
			f := newFixture(t)
			f.registry["allocations"].([]any)[0].(map[string]any)["redis_prefix"] = prefix
			f.write(t)
			path := filepath.Join(f.normal, "registry.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Check()
			if (err == nil) != strings.HasPrefix(prefix, "ordinary:") {
				t.Fatal("namespace admitted", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("normal registry changed", err)
			}
		})
	}
}
