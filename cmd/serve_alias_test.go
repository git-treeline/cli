package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
)

func newTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	return registry.New(filepath.Join(t.TempDir(), "registry.json"))
}

func TestServeAlias_RejectsHostileName(t *testing.T) {
	t.Setenv("GTL_HOME", t.TempDir())
	for _, name := range []string{`<script>alert(1)</script>`, "-leading", "trailing-", "has space", "dot.ted"} {
		err := serveAliasCmd.RunE(serveAliasCmd, []string{name, "8081"})
		ce, ok := err.(*CliError)
		if !ok {
			t.Fatalf("%q: want *CliError, got %T: %v", name, err, err)
		}
		if !strings.Contains(ce.Message, "invalid alias name") {
			t.Errorf("%q: unexpected message %q", name, ce.Message)
		}
	}
	if got := config.LoadUserConfig("").RouterAliases(); len(got) != 0 {
		t.Errorf("rejected aliases must not be saved, got %v", got)
	}
}

func TestServeAlias_AcceptsPlainName(t *testing.T) {
	t.Setenv("GTL_HOME", t.TempDir())
	if err := serveAliasCmd.RunE(serveAliasCmd, []string{"Redis-UI", "8081"}); err != nil {
		t.Fatalf("plain alias name should be accepted: %v", err)
	}
	if got := config.LoadUserConfig("").RouterAliases()["redis-ui"]; got != 8081 {
		t.Errorf("alias not saved, got %d", got)
	}
}

func TestServeAliasRemove_FindsLegacyMixedCaseAlias(t *testing.T) {
	t.Setenv("GTL_HOME", t.TempDir())
	uc := config.LoadUserConfig("")
	uc.Set("router.aliases.RedisUI", float64(8081))
	if err := uc.Save(); err != nil {
		t.Fatal(err)
	}

	serveAliasRemove = true
	t.Cleanup(func() { serveAliasRemove = false })
	if err := serveAliasCmd.RunE(serveAliasCmd, []string{"redisui"}); err != nil {
		t.Fatalf("remove should match legacy mixed-case alias: %v", err)
	}
	if got := config.LoadUserConfig("").RouterAliases(); len(got) != 0 {
		t.Errorf("alias should be removed, got %v", got)
	}
}

func TestDetectAliasPort_SinglePort(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
		"port":     float64(3000),
	}); err != nil {
		t.Fatal(err)
	}

	port, err := detectAliasPortFrom("/wt/myapp", reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if port != 3000 {
		t.Errorf("expected 3000, got %d", port)
	}
}

func TestDetectAliasPort_MultiplePorts_SelectsFirst(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
		"ports":    []any{float64(3000), float64(3001), float64(3002)},
	}); err != nil {
		t.Fatal(err)
	}

	reader := strings.NewReader("\n")
	port, err := detectAliasPortFrom("/wt/myapp", reg, reader)
	if err != nil {
		t.Fatal(err)
	}
	if port != 3000 {
		t.Errorf("expected 3000 (default), got %d", port)
	}
}

func TestDetectAliasPort_MultiplePorts_SelectsSecond(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
		"ports":    []any{float64(3000), float64(3001)},
	}); err != nil {
		t.Fatal(err)
	}

	reader := strings.NewReader("2\n")
	port, err := detectAliasPortFrom("/wt/myapp", reg, reader)
	if err != nil {
		t.Fatal(err)
	}
	if port != 3001 {
		t.Errorf("expected 3001, got %d", port)
	}
}

func TestDetectAliasPort_NoAllocation(t *testing.T) {
	reg := newTestRegistry(t)

	_, err := detectAliasPortFrom("/wt/missing", reg, nil)
	if err == nil {
		t.Fatal("expected error for missing allocation")
	}
	cliError, ok := err.(*CliError)
	if !ok {
		t.Fatalf("expected *CliError, got %T", err)
	}
	if !strings.Contains(cliError.Message, "No port found") {
		t.Errorf("expected 'No port found' message, got: %s", cliError.Message)
	}
	if !strings.Contains(cliError.Hint, "gtl setup") {
		t.Errorf("expected hint to mention 'gtl setup', got: %s", cliError.Hint)
	}
}

func TestDetectAliasPort_AllocationWithoutPorts(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := detectAliasPortFrom("/wt/myapp", reg, nil)
	if err == nil {
		t.Fatal("expected error for allocation without ports")
	}
	cliError, ok := err.(*CliError)
	if !ok {
		t.Fatalf("expected *CliError, got %T", err)
	}
	if !strings.Contains(cliError.Message, "no ports assigned") {
		t.Errorf("expected 'no ports assigned' message, got: %s", cliError.Message)
	}
	if !strings.Contains(cliError.Hint, "gtl start") {
		t.Errorf("expected hint to mention 'gtl start', got: %s", cliError.Hint)
	}
}

func TestDetectAliasPort_EmptyPortsArray(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
		"ports":    []any{},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := detectAliasPortFrom("/wt/myapp", reg, nil)
	if err == nil {
		t.Fatal("expected error for empty ports array")
	}
}

func TestDetectAliasPort_EmptyPortsArrayWithLegacyPort(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
		"ports":    []any{},
		"port":     float64(4000),
	}); err != nil {
		t.Fatal(err)
	}

	port, err := detectAliasPortFrom("/wt/myapp", reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if port != 4000 {
		t.Errorf("expected legacy fallback to 4000, got %d", port)
	}
}

func TestDetectAliasPort_MultiplePorts_InvalidInput(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.Allocate(registry.Allocation{
		"worktree": "/wt/myapp",
		"project":  "myapp",
		"ports":    []any{float64(5000), float64(5001)},
	}); err != nil {
		t.Fatal(err)
	}

	// Invalid input falls back to default (first port)
	reader := strings.NewReader("99\n")
	port, err := detectAliasPortFrom("/wt/myapp", reg, reader)
	if err != nil {
		t.Fatal(err)
	}
	if port != 5000 {
		t.Errorf("expected default fallback to 5000, got %d", port)
	}
}
