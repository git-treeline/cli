package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

func TestCurrentAllocation_Found(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(dir, "gtl-home"))
	wt := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wt)

	reg := registry.New("")
	if err := reg.Allocate(registry.Allocation{"worktree": wt, "port": float64(3010)}); err != nil {
		t.Fatal(err)
	}

	got, err := currentAllocation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Entry["port"] != float64(3010) {
		t.Errorf("Entry[port] = %v, want 3010", got.Entry["port"])
	}
}

func TestCurrentAllocation_NotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(dir, "gtl-home"))
	t.Chdir(dir)

	_, err := currentAllocation()
	if err == nil {
		t.Fatal("expected error for missing allocation")
	}
	var ce *CliError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CliError, got %T", err)
	}
	want := errNoAllocation(dir)
	if ce.Message != want.(*CliError).Message {
		t.Errorf("Message = %q, want %q", ce.Message, want.(*CliError).Message)
	}
}

func TestWtAlloc_PrimaryPort_NoPorts(t *testing.T) {
	w := &wtAlloc{
		Path:  "/wt/no-ports",
		Entry: registry.Allocation{"worktree": "/wt/no-ports"},
	}

	_, err := w.PrimaryPort()
	if err == nil {
		t.Fatal("expected error for allocation with no ports")
	}
	var ce *CliError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CliError, got %T", err)
	}
	want := errNoAllocationNoPorts(w.Path)
	if ce.Message != want.(*CliError).Message {
		t.Errorf("Message = %q, want %q", ce.Message, want.(*CliError).Message)
	}
}

func TestWtAlloc_PrimaryPort_ReturnsFirstPort(t *testing.T) {
	w := &wtAlloc{
		Path:  "/wt/with-ports",
		Entry: registry.Allocation{"worktree": "/wt/with-ports", "ports": []any{float64(3010), float64(3011)}},
	}

	port, err := w.PrimaryPort()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 3010 {
		t.Errorf("PrimaryPort() = %d, want 3010", port)
	}
}
