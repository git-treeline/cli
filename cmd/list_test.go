package cmd

import (
	"encoding/json"
	"strings"
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

// The --json contract must serialize to valid, parseable JSON carrying the
// documented fields (project, branch, ports, db, redis, path, status).
func TestListEntry_JSONShape(t *testing.T) {
	entries := []listEntry{
		newListEntry(registry.Allocation{
			"project":  "salt",
			"branch":   "feature-x",
			"worktree": "/wt/feature-x",
			"ports":    []any{float64(3010), float64(3011)},
			"database": "salt_dev_feature_x",
			"redis_db": float64(5),
		}, false),
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Must round-trip as valid JSON.
	var parsed []map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, data)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(parsed))
	}
	e := parsed[0]
	if e["project"] != "salt" || e["branch"] != "feature-x" || e["path"] != "/wt/feature-x" {
		t.Errorf("unexpected core JSON fields: %v", e)
	}
	if e["database"] != "salt_dev_feature_x" {
		t.Errorf("unexpected database: %v", e["database"])
	}
	if e["redis"] != "db:5" {
		t.Errorf("unexpected redis: %v", e["redis"])
	}
	// status is always present in the scriptable contract.
	if e["status"] != "unknown" {
		t.Errorf("expected status unknown, got %v", e["status"])
	}
	ports, ok := e["ports"].([]any)
	if !ok || len(ports) != 2 || ports[0].(float64) != 3010 {
		t.Errorf("unexpected ports: %v", e["ports"])
	}
}

// Empty optional fields must be omitted from JSON (omitempty on database/redis).
func TestListEntry_JSONOmitsEmptyOptionals(t *testing.T) {
	e := newListEntry(registry.Allocation{
		"project":  "p",
		"branch":   "b",
		"worktree": "/wt/b",
	}, false)
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "database") {
		t.Errorf("empty database should be omitted: %s", s)
	}
	if strings.Contains(s, "redis") {
		t.Errorf("empty redis should be omitted: %s", s)
	}
	// path and status are always present.
	if !strings.Contains(s, `"path"`) || !strings.Contains(s, `"status"`) {
		t.Errorf("path and status must always be present: %s", s)
	}
}

func TestPrintListEntry_PlainOutput(t *testing.T) {
	e := listEntry{
		Project:  "salt",
		Branch:   "feature-x",
		Ports:    []int{3010, 3011},
		Database: "salt_dev",
		Redis:    "prefix:salt:x",
		Path:     "/wt/feature-x",
		Status:   "up",
	}
	out := captureStdout(t, func() { printListEntry(e, true) })

	for _, want := range []string{"salt", "feature-x", ":3010,3011", "db:salt_dev", "prefix:salt:x", "[up]", "/wt/feature-x"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing %q:\n%s", want, out)
		}
	}
}

// Without --check, status must not be rendered in plain output.
func TestPrintListEntry_HidesStatusWhenNotChecked(t *testing.T) {
	e := listEntry{Project: "p", Branch: "b", Ports: []int{3000}, Path: "/wt/b", Status: "unknown"}
	out := captureStdout(t, func() { printListEntry(e, false) })
	if strings.Contains(out, "[unknown]") {
		t.Errorf("status should be hidden without --check: %s", out)
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
