package allocator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/config"
)

func TestManagedDevRedisAllocationOptIn(t *testing.T) {
	for _, mode := range []string{"opted in", "HTTP", "main", "main HTTP", "database strategy", "remote"} {
		t.Run(mode, func(t *testing.T) {
			key := "host_migration_qa/feature"
			if mode == "main" || mode == "main HTTP" {
				key = "host_migration_qa"
			}
			al, reg := managedDevAllocator(t, key)
			root := al.ProjectConfig.ProjectRoot
			body := "project: host_migration_qa\n"
			if mode != "HTTP" && mode != "main HTTP" {
				body += "env: {REDIS_URL: '{redis_url}', REDIS_PREFIX: '{redis_prefix}'}\n"
			}
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "database strategy" {
				al.UserConfig.Data["redis"] = map[string]any{"strategy": "database"}
			}
			if mode == "remote" {
				al.UserConfig.Data["redis"] = map[string]any{"url": "redis://remote.invalid:6379"}
			}
			before, err := os.ReadFile(reg.Path)
			if err != nil {
				t.Fatal(err)
			}
			a, err := al.Allocate(root, filepath.Base(root), mode == "main" || mode == "main HTTP", "feature")
			if mode != "opted in" && mode != "HTTP" && mode != "main" && mode != "main HTTP" {
				if err == nil {
					t.Fatal("unsafe Redis allocation admitted")
				}
				after, err := os.ReadFile(reg.Path)
				if err != nil || string(before) != string(after) {
					t.Fatal("refusal changed registry", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if mode == "opted in" || mode == "main" {
				want = "host_migration_qa:" + filepath.Base(root)
			}
			if a.RedisPrefix != want || a.RedisDB != 0 || al.BuildRedisURL(a) != "redis://localhost:6379" {
				t.Fatal("Redis allocation", a)
			}
			again, err := al.Allocate(root, filepath.Base(root), mode == "main" || mode == "main HTTP", "feature")
			if err != nil || !again.Reused || again.RedisPrefix != want {
				t.Fatal("reuse", again, err)
			}
			if mode == "HTTP" || mode == "main HTTP" {
				return
			}
			before, err = os.ReadFile(reg.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, config.ProjectConfigFile), []byte("project: host_migration_qa\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := al.Allocate(root, filepath.Base(root), mode == "main" || mode == "main HTTP", "feature"); err == nil {
				t.Fatal("removed opt-in reused")
			}
			after, err := os.ReadFile(reg.Path)
			if err != nil || string(before) != string(after) {
				t.Fatal("drift changed registry", err)
			}
		})
	}
}
