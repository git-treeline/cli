package cmd

import (
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestNewListEntry_Projection(t *testing.T) {
	a := registry.Allocation{
		"project":      "salt",
		"branch":       "feature-x",
		"worktree":     "/wt/feature-x",
		"ports":        []any{float64(3010), float64(3011)},
		"database":     "salt_dev_feature_x",
		"redis_prefix": "salt:feature-x",
	}
	e := newListEntry(a, false)

	if e.Project != "salt" || e.Branch != "feature-x" || e.Path != "/wt/feature-x" {
		t.Errorf("unexpected core fields: %+v", e)
	}
	if len(e.Ports) != 2 || e.Ports[0] != 3010 || e.Ports[1] != 3011 {
		t.Errorf("unexpected ports: %v", e.Ports)
	}
	if e.Database != "salt_dev_feature_x" {
		t.Errorf("unexpected database: %q", e.Database)
	}
	if e.Redis != "prefix:salt:feature-x" {
		t.Errorf("unexpected redis: %q", e.Redis)
	}
	if e.Status != "unknown" {
		t.Errorf("expected status 'unknown' when not probed, got %q", e.Status)
	}
}

func TestRedisLabel_DBStrategy(t *testing.T) {
	got := redisLabel(registry.Allocation{"redis_db": float64(5)})
	if got != "db:5" {
		t.Errorf("expected db:5, got %q", got)
	}
	if got := redisLabel(registry.Allocation{}); got != "" {
		t.Errorf("expected empty redis label, got %q", got)
	}
}
