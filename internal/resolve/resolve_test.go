package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestResolve_SameBranch(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	apiWT := filepath.Join(dir, "api-feat")
	_ = os.MkdirAll(apiWT, 0o755)
	_ = reg.Allocate(registry.Allocation{
		"project": "api", "worktree": apiWT, "branch": "feature-x",
		"port": float64(3010), "ports": []any{float64(3010)},
	})

	feWT := filepath.Join(dir, "frontend-feat")
	_ = os.MkdirAll(feWT, 0o755)
	_ = reg.Allocate(registry.Allocation{
		"project": "frontend", "worktree": feWT, "branch": "feature-x",
		"port": float64(3020), "ports": []any{float64(3020)},
	})

	r := New(reg, feWT, "feature-x")
	url, err := r.Resolve("api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://127.0.0.1:3010" {
		t.Errorf("expected http://127.0.0.1:3010, got %s", url)
	}
}

func TestResolve_ExplicitBranch(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	apiWT := filepath.Join(dir, "api-dev")
	_ = os.MkdirAll(apiWT, 0o755)
	_ = reg.Allocate(registry.Allocation{
		"project": "api", "worktree": apiWT, "branch": "develop",
		"port": float64(3010), "ports": []any{float64(3010)},
	})

	feWT := filepath.Join(dir, "frontend-main")
	_ = os.MkdirAll(feWT, 0o755)

	r := New(reg, feWT, "main")
	url, err := r.Resolve("api", "develop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://127.0.0.1:3010" {
		t.Errorf("expected http://127.0.0.1:3010, got %s", url)
	}
}

func TestResolve_WithLink(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	apiMain := filepath.Join(dir, "api-main")
	_ = os.MkdirAll(apiMain, 0o755)
	_ = reg.Allocate(registry.Allocation{
		"project": "api", "worktree": apiMain, "branch": "main",
		"port": float64(3010), "ports": []any{float64(3010)},
	})

	apiFeat := filepath.Join(dir, "api-feat")
	_ = os.MkdirAll(apiFeat, 0o755)
	_ = reg.Allocate(registry.Allocation{
		"project": "api", "worktree": apiFeat, "branch": "feature-payments",
		"port": float64(3020), "ports": []any{float64(3020)},
	})

	feWT := filepath.Join(dir, "frontend-main")
	_ = os.MkdirAll(feWT, 0o755)
	_ = reg.Allocate(registry.Allocation{
		"project": "frontend", "worktree": feWT, "branch": "main",
		"port": float64(3030), "ports": []any{float64(3030)},
	})

	_ = reg.SetLink(feWT, "api", "feature-payments")

	r := New(reg, feWT, "main")
	url, err := r.Resolve("api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://127.0.0.1:3020" {
		t.Errorf("expected linked port 3020, got %s", url)
	}
}

func TestResolve_NotFound(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	reg := registry.New(regPath)

	r := New(reg, "/tmp/fake", "main")
	_, err := r.Resolve("api")
	if err == nil {
		t.Fatal("expected error for missing project")
	}
}
