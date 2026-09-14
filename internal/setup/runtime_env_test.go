package setup

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/registry"
)

func TestSyncRuntimeEnvResolvesLinksAndWritesReturnedValues(t *testing.T) {
	worktree := t.TempDir()
	registryFile := filepath.Join(t.TempDir(), "registry.json")
	RegistryPath = registryFile
	t.Cleanup(func() { RegistryPath = "" })

	if err := os.WriteFile(filepath.Join(worktree, ".treeline.yml"), []byte("project: app\nenv_file:\n  target: .env.local\nenv:\n  PORT: '{port}'\n  API_URL: '{resolve:api}'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(registry.RegistryData{Version: 1, Allocations: []registry.Allocation{
		{"worktree": worktree, "project": "app", "branch": "feature", "port": float64(4100), "ports": []any{float64(4100)}},
		{"worktree": filepath.Join(worktree, "api"), "project": "api", "branch": "linked", "port": float64(4200), "ports": []any{float64(4200)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(registryFile)
	if err := reg.SetLink(worktree, "api", "linked"); err != nil {
		t.Fatal(err)
	}

	vars, err := SyncRuntimeEnv(worktree, config.LoadUserConfig(filepath.Join(t.TempDir(), "config.json")))
	if err != nil {
		t.Fatal(err)
	}
	if got := vars["PORT"]; got != "4100" {
		t.Errorf("PORT = %q, want 4100", got)
	}
	if got := vars["API_URL"]; got != "http://127.0.0.1:4200" {
		t.Errorf("API_URL = %q", got)
	}
	contents, err := os.ReadFile(filepath.Join(worktree, ".env.local"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(contents); got != "PORT=\"4100\"\nAPI_URL=\"http://127.0.0.1:4200\"\n" && got != "API_URL=\"http://127.0.0.1:4200\"\nPORT=\"4100\"\n" {
		t.Errorf("env file = %q", got)
	}
}

func TestResolveRuntimeEnvWithoutAllocationIsNoop(t *testing.T) {
	RegistryPath = filepath.Join(t.TempDir(), "registry.json")
	t.Cleanup(func() { RegistryPath = "" })

	vars, err := ResolveRuntimeEnv(t.TempDir(), config.LoadUserConfig(filepath.Join(t.TempDir(), "config.json")))
	if err != nil {
		t.Fatal(err)
	}
	if vars != nil {
		t.Errorf("vars = %#v, want nil", vars)
	}
}

func TestSyncRuntimeEnvRemovesOnlyUnchangedManagedAssignments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(root, "gtl-home"))
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	registryFile := filepath.Join(root, "registry.json")
	RegistryPath = registryFile
	t.Cleanup(func() { RegistryPath = "" })
	if err := os.WriteFile(registryFile, []byte(`{"version":1,"allocations":[{"worktree":"`+worktree+`","project":"app","port":4300,"ports":[4300]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(worktree, ".treeline.yml")
	if err := os.WriteFile(configPath, []byte("project: app\nenv_file: .env\nenv:\n  MANAGED: old\n  REMOVE: remove-me\n  USER_EDITED: old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(worktree, ".env")
	if err := os.WriteFile(envPath, []byte("USER_KEY=keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	uc := config.LoadUserConfig(filepath.Join(root, "config.json"))
	initial := &Setup{WorktreePath: worktree, MainRepo: worktree, ProjectConfig: config.LoadProjectConfig(worktree), Log: io.Discard}
	if err := initial.writeEnvFile(map[string]string{"MANAGED": "old", "REMOVE": "remove-me", "USER_EDITED": "old"}); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.Replace(string(contents), `USER_EDITED="old"`, "USER_EDITED=user-value", 1))
	if err := os.WriteFile(envPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("project: app\nenv_file: .env\nenv:\n  MANAGED: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	vars, err := SyncRuntimeEnv(worktree, uc)
	if err != nil {
		t.Fatal(err)
	}
	if got := vars["MANAGED"]; got != "new" {
		t.Errorf("MANAGED = %q, want new", got)
	}
	contents, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(contents)
	if strings.Contains(got, "\nREMOVE=") {
		t.Errorf("unchanged removed key remained in env file:\n%s", got)
	}
	if !strings.Contains(got, "USER_KEY=keep") || !strings.Contains(got, "USER_EDITED=user-value") {
		t.Errorf("user entries were not preserved:\n%s", got)
	}
	if !strings.Contains(got, `MANAGED="new"`) {
		t.Errorf("managed value was not refreshed:\n%s", got)
	}

	if err := os.WriteFile(configPath, []byte("project: app\nenv_file: .env\nenv: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars, err = SyncRuntimeEnv(worktree, uc)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 0 {
		t.Errorf("empty template vars = %#v", vars)
	}
	contents, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	got = string(contents)
	if strings.Contains(got, "\nMANAGED=") || !strings.Contains(got, "USER_KEY=keep") || !strings.Contains(got, "USER_EDITED=user-value") {
		t.Errorf("empty template did not safely remove only managed values:\n%s", got)
	}
}

func TestWriteManagedEnvDoesNotClaimUntrackedLegacyAssignments(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte("LEGACY=keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedEnv(envPath, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(contents); got != "LEGACY=keep\n" {
		t.Errorf("untracked legacy assignment changed: %q", got)
	}
}

func TestManagedEnvStateStoresAssignmentHashes(t *testing.T) {
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := writeManagedEnv(envPath, map[string]string{"TOKEN": "top-secret"}); err != nil {
		t.Fatal(err)
	}
	statePath, err := managedEnvStatePath(envPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "top-secret") {
		t.Errorf("managed state retained plaintext value: %s", data)
	}
	var state managedEnvState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if got := state.Assignments["TOKEN"]; len(got) != 64 {
		t.Errorf("assignment hash = %q", got)
	}
}

func TestWriteManagedEnvSharesOwnershipAcrossSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GTL_HOME", filepath.Join(t.TempDir(), "gtl-home"))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	aliasEnv := filepath.Join(alias, "missing", ".env")
	realEnv := filepath.Join(root, "missing", ".env")
	if err := writeManagedEnv(aliasEnv, map[string]string{"OLD_TARGET": "old-service"}); err != nil {
		t.Fatal(err)
	}
	aliasState, err := managedEnvStatePath(aliasEnv)
	if err != nil {
		t.Fatal(err)
	}
	realState, err := managedEnvStatePath(realEnv)
	if err != nil {
		t.Fatal(err)
	}
	if aliasState != realState {
		t.Fatalf("state paths differ for the same env file: alias=%s real=%s", aliasState, realState)
	}
	if err := writeManagedEnv(realEnv, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(realEnv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "OLD_TARGET=") {
		t.Errorf("managed assignment remained through symlink alias: %s", contents)
	}
}
