package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/registry"
	"github.com/git-treeline/git-treeline/internal/setup"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func seedRegistry(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")

	data := registry.RegistryData{
		Version: 1,
		Allocations: []registry.Allocation{
			{
				"worktree":         "/tmp/test-wt",
				"worktree_name":    "feature-x",
				"project":          "myapp",
				"branch":           "feature-x",
				"port":             float64(3050),
				"ports":            []any{float64(3050)},
				"database":         "myapp_feature_x",
				"database_adapter": "postgresql",
			},
			{
				"worktree":         "/tmp/test-wt2",
				"worktree_name":    "staging",
				"project":          "myapp",
				"branch":           "staging",
				"port":             float64(3060),
				"ports":            []any{float64(3060)},
				"database":         "myapp_staging",
				"database_adapter": "postgresql",
			},
			{
				"worktree":      "/tmp/other-wt",
				"worktree_name": "main",
				"project":       "other",
				"branch":        "main",
				"port":          float64(4000),
				"ports":         []any{float64(4000)},
			},
		},
	}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	registryPath = regPath
	setup.RegistryPath = regPath
	t.Cleanup(func() {
		registryPath = ""
		setup.RegistryPath = ""
	})
}

func TestNewServer_HasTools(t *testing.T) {
	s := NewServer("test")
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestHandlePort(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "port"
	req.Params.Arguments = map[string]any{"path": "/tmp/test-wt"}

	result, err := handlePort(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)
	if text != "3050" {
		t.Errorf("expected 3050, got %s", text)
	}
}

func TestHandlePort_NotFound(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "port"
	req.Params.Arguments = map[string]any{"path": "/tmp/nonexistent"}

	result, err := handlePort(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result for missing allocation")
	}
}

func TestHandleList(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "list"
	req.Params.Arguments = map[string]any{}

	result, err := handleList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)

	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestHandleList_FilterByProject(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "list"
	req.Params.Arguments = map[string]any{"project": "other"}

	result, err := handleList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)

	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry for 'other', got %d", len(entries))
	}
}

func TestHandleDBName(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "db_name"
	req.Params.Arguments = map[string]any{"path": "/tmp/test-wt"}

	result, err := handleDBName(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)
	if text != "myapp_feature_x" {
		t.Errorf("expected myapp_feature_x, got %s", text)
	}
}

func TestHandleDBName_NoDB(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "db_name"
	req.Params.Arguments = map[string]any{"path": "/tmp/other-wt"}

	result, err := handleDBName(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result for worktree with no database")
	}
}

func TestHandleStatus(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "status"
	req.Params.Arguments = map[string]any{"path": "/tmp/test-wt"}

	result, err := handleStatus(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)

	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if status["project"] != "myapp" {
		t.Errorf("expected project=myapp, got %v", status["project"])
	}
	if status["branch"] != "feature-x" {
		t.Errorf("expected branch=feature-x, got %v", status["branch"])
	}
	if status["supervisor"] != "not running" {
		t.Errorf("expected supervisor=not running, got %v", status["supervisor"])
	}
}

func TestHandleConfigGet_User(t *testing.T) {
	t.Setenv("GTL_HOME", t.TempDir())

	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_get"
	req.Params.Arguments = map[string]any{
		"key":   "port.base",
		"scope": "user",
	}

	result, err := handleConfigGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)
	if text != "3002" {
		t.Errorf("expected 3002, got %s", text)
	}
}

func TestHandleConfigGet_UnknownScope(t *testing.T) {
	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_get"
	req.Params.Arguments = map[string]any{
		"key":   "port.base",
		"scope": "invalid",
	}

	result, err := handleConfigGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result for unknown scope")
	}
}

func TestHandleSupervisor_NotRunning(t *testing.T) {
	req := mcplib.CallToolRequest{}
	req.Params.Name = "start"
	req.Params.Arguments = map[string]any{"path": "/tmp/nonexistent-wt-for-test"}

	result, err := handleStart(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when supervisor is not running")
	}
}

func TestHandleStop_NotRunning(t *testing.T) {
	req := mcplib.CallToolRequest{}
	req.Params.Name = "stop"
	req.Params.Arguments = map[string]any{"path": "/tmp/nonexistent-wt-for-test"}

	result, err := handleStop(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when supervisor is not running")
	}
}

func TestHandleStop_KillNotRunning(t *testing.T) {
	req := mcplib.CallToolRequest{}
	req.Params.Name = "stop"
	req.Params.Arguments = map[string]any{"path": "/tmp/nonexistent-wt-for-test", "kill": true}

	result, err := handleStop(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when supervisor is not running")
	}
}

func TestHandleRestart_NotRunning(t *testing.T) {
	req := mcplib.CallToolRequest{}
	req.Params.Name = "restart"
	req.Params.Arguments = map[string]any{"path": "/tmp/nonexistent-wt-for-test"}

	result, err := handleRestart(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when supervisor is not running")
	}
}

func TestHandleDoctor_NoAllocation(t *testing.T) {
	seedRegistry(t)

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "doctor"
	req.Params.Arguments = map[string]any{"path": dir}

	result, err := handleDoctor(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)

	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cfg, _ := doc["config"].(map[string]any)
	if cfg["treeline_yml"] != "missing" {
		t.Errorf("expected treeline_yml=missing, got %v", cfg["treeline_yml"])
	}

	alloc, _ := doc["allocation"].(map[string]any)
	if alloc["status"] == nil {
		t.Error("expected allocation.status to be set for missing allocation")
	}

	rt, _ := doc["runtime"].(map[string]any)
	if rt["supervisor"] != "not running" {
		t.Errorf("expected supervisor=not running, got %v", rt["supervisor"])
	}
}

func TestHandleDoctor_WithConfig(t *testing.T) {
	seedRegistry(t)

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: testapp\n"), 0o644)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "doctor"
	req.Params.Arguments = map[string]any{"path": dir}

	result, err := handleDoctor(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)

	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cfg, _ := doc["config"].(map[string]any)
	if cfg["treeline_yml"] != "ok" {
		t.Errorf("expected treeline_yml=ok, got %v", cfg["treeline_yml"])
	}
	if cfg["project"] != "testapp" {
		t.Errorf("expected project=testapp, got %v", cfg["project"])
	}
}

func TestHandleAllocationsResource(t *testing.T) {
	seedRegistry(t)

	req := mcplib.ReadResourceRequest{}
	req.Params.URI = "gtl://allocations"

	contents, err := handleAllocationsResource(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}

	text := contents[0].(mcplib.TextResourceContents).Text
	var allocs []map[string]any
	if err := json.Unmarshal([]byte(text), &allocs); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(allocs) != 3 {
		t.Errorf("expected 3 allocations, got %d", len(allocs))
	}
}

func TestHandleUserConfigResource(t *testing.T) {
	req := mcplib.ReadResourceRequest{}
	req.Params.URI = "gtl://config/user"

	contents, err := handleUserConfigResource(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}

	text := contents[0].(mcplib.TextResourceContents).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse valid JSON: %v", err)
	}
}

func TestHandleConfigGet_ProjectScope(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: testapp\ndatabase:\n  adapter: postgresql\n"), 0o644)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_get"
	req.Params.Arguments = map[string]any{
		"key":   "project",
		"scope": "project",
		"path":  dir,
	}

	result, err := handleConfigGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)
	if text != "\"testapp\"" {
		t.Errorf("expected \"testapp\", got %s", text)
	}
}

func TestHandleConfigGet_ProjectScope_Nested(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: testapp\ndatabase:\n  adapter: postgresql\n"), 0o644)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_get"
	req.Params.Arguments = map[string]any{
		"key":   "database.adapter",
		"scope": "project",
		"path":  dir,
	}

	result, err := handleConfigGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, result)
	if text != "\"postgresql\"" {
		t.Errorf("expected \"postgresql\", got %s", text)
	}
}

func TestHandleConfigGet_MissingKey(t *testing.T) {
	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_get"
	req.Params.Arguments = map[string]any{
		"scope": "user",
	}

	result, err := handleConfigGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing key")
	}
}

// --- Tests for new read-only tools ---

func TestHandleWhere(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "where"
	req.Params.Arguments = map[string]any{"branch": "feature-x"}

	result, err := handleWhere(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["worktree"] != "/tmp/test-wt" {
		t.Errorf("expected /tmp/test-wt, got %v", data["worktree"])
	}
	if data["project"] != "myapp" {
		t.Errorf("expected myapp, got %v", data["project"])
	}
}

func TestHandleWhere_ProjectBranch(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "where"
	req.Params.Arguments = map[string]any{"branch": "myapp/staging"}

	result, err := handleWhere(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["worktree"] != "/tmp/test-wt2" {
		t.Errorf("expected /tmp/test-wt2, got %v", data["worktree"])
	}
}

func TestHandleWhere_NotFound(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "where"
	req.Params.Arguments = map[string]any{"branch": "nonexistent"}

	result, err := handleWhere(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent branch")
	}
}

func TestHandleResolve_NotFound(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "resolve"
	req.Params.Arguments = map[string]any{
		"project": "nonexistent",
		"path":    "/tmp/test-wt",
	}

	result, err := handleResolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent project")
	}
}

func TestHandleEnv_Template(t *testing.T) {
	seedRegistry(t)

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: testapp\nenv:\n  PORT: \"{port}\"\n  API_URL: \"{resolve:api}\"\n"), 0o644)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "env"
	req.Params.Arguments = map[string]any{"path": dir, "template": true}

	result, err := handleEnv(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["PORT"] != "{port}" {
		t.Errorf("expected {port}, got %v", data["PORT"])
	}
	if data["API_URL"] != "{resolve:api}" {
		t.Errorf("expected {resolve:api}, got %v", data["API_URL"])
	}
}

func TestHandleEnv_NoAllocation(t *testing.T) {
	seedRegistry(t)

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: testapp\n"), 0o644)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "env"
	req.Params.Arguments = map[string]any{"path": dir}

	result, err := handleEnv(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing allocation")
	}
}

// --- Tests for write tools ---

func TestHandleConfigSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTL_HOME", dir)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_set"
	req.Params.Arguments = map[string]any{
		"key":   "port.base",
		"value": "5000",
	}

	result, err := handleConfigSet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["key"] != "port.base" {
		t.Errorf("expected key=port.base, got %v", data["key"])
	}
	// Value should be parsed as float64 from "5000"
	if data["value"] != float64(5000) {
		t.Errorf("expected value=5000, got %v", data["value"])
	}
}

func TestHandleConfigSet_Boolean(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTL_HOME", dir)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "config_set"
	req.Params.Arguments = map[string]any{
		"key":   "warnings.safari",
		"value": "false",
	}

	result, err := handleConfigSet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["value"] != false {
		t.Errorf("expected value=false, got %v (%T)", data["value"], data["value"])
	}
}

func TestHandleLink_ListEmpty(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "link"
	req.Params.Arguments = map[string]any{"path": "/tmp/test-wt"}

	result, err := handleLink(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var links map[string]any
	if err := json.Unmarshal([]byte(text), &links); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected empty links, got %v", links)
	}
}

func TestHandleLink_SetAndUnlink(t *testing.T) {
	seedRegistry(t)

	// Set a link: myapp/feature-x -> other/main
	req := mcplib.CallToolRequest{}
	req.Params.Name = "link"
	req.Params.Arguments = map[string]any{
		"path":    "/tmp/test-wt",
		"project": "other",
		"branch":  "main",
	}

	result, err := handleLink(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data["linked"] != true {
		t.Errorf("expected linked=true, got %v", data["linked"])
	}

	// Verify link exists in registry
	reg := newRegistry()
	links := reg.GetLinks("/tmp/test-wt")
	if links["other"] != "main" {
		t.Errorf("expected link other->main, got %v", links)
	}

	// Unlink
	unlinkReq := mcplib.CallToolRequest{}
	unlinkReq.Params.Name = "unlink"
	unlinkReq.Params.Arguments = map[string]any{
		"path":    "/tmp/test-wt",
		"project": "other",
	}

	result, err = handleUnlink(context.Background(), unlinkReq)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	// Verify link is gone
	reg = newRegistry()
	links = reg.GetLinks("/tmp/test-wt")
	if _, ok := links["other"]; ok {
		t.Errorf("expected link to be removed, got %v", links)
	}
}

func TestHandleLink_TargetNotFound(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "link"
	req.Params.Arguments = map[string]any{
		"path":    "/tmp/test-wt",
		"project": "nonexistent",
		"branch":  "main",
	}

	result, err := handleLink(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent target")
	}
}

func TestHandleUnlink_NoActiveLink(t *testing.T) {
	seedRegistry(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "unlink"
	req.Params.Arguments = map[string]any{
		"path":    "/tmp/test-wt",
		"project": "other",
	}

	result, err := handleUnlink(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when no active link exists")
	}
}

func TestHandleEnvSync_NoAllocation(t *testing.T) {
	// env_sync gracefully returns success when no allocation exists
	// (RegenerateEnvFile returns nil for missing allocations).
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte(
		"project: syncapp\nenv_file:\n  target: .env.local\nenv:\n  PORT: \"{port}\"\n",
	), 0o644)

	seedRegistry(t) // seeds a registry that doesn't contain this dir

	req := mcplib.CallToolRequest{}
	req.Params.Name = "env_sync"
	req.Params.Arguments = map[string]any{"path": dir}

	result, err := handleEnvSync(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Should succeed (RegenerateEnvFile returns nil for missing allocations)
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if resp["synced"] != true {
		t.Errorf("expected synced=true, got %v", resp["synced"])
	}
	// Verify managed keys are returned from the template
	keys, ok := resp["managed_keys"].([]any)
	if !ok {
		t.Fatalf("expected managed_keys array, got %T", resp["managed_keys"])
	}
	if len(keys) != 1 || keys[0] != "PORT" {
		t.Errorf("expected managed_keys=[PORT], got %v", keys)
	}
}

func TestHandleResolve_ExplicitBranch(t *testing.T) {
	seedRegistry(t)

	// Resolve "other" project with explicit branch "main" from myapp/feature-x worktree.
	// This bypasses CurrentBranch() since we provide the target branch explicitly.
	req := mcplib.CallToolRequest{}
	req.Params.Name = "resolve"
	req.Params.Arguments = map[string]any{
		"project": "other",
		"branch":  "main",
		"path":    "/tmp/test-wt",
	}

	result, err := handleResolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	url, ok := data["url"].(string)
	if !ok || url == "" {
		t.Fatalf("expected url in response, got %v", data)
	}
	// URL may be localhost:4000 or a router URL like https://other-main.prt.dev
	// depending on whether gtl serve is running on this machine.
	if !strings.Contains(url, "4000") && !strings.Contains(url, "other") {
		t.Errorf("expected URL to contain port 4000 or project name 'other', got %s", url)
	}
}

func TestHandleSetup_DryRun(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: dryapp\n"), 0o644)

	// Use a temp registry so setup doesn't touch the real one
	regDir := t.TempDir()
	regPath := filepath.Join(regDir, "registry.json")
	data := registry.RegistryData{Version: 1, Allocations: []registry.Allocation{}}
	raw, _ := json.Marshal(data)
	_ = os.WriteFile(regPath, raw, 0o644)
	registryPath = regPath
	setup.RegistryPath = regPath
	t.Cleanup(func() {
		registryPath = ""
		setup.RegistryPath = ""
	})

	t.Setenv("GTL_HOME", t.TempDir())

	req := mcplib.CallToolRequest{}
	req.Params.Name = "setup"
	req.Params.Arguments = map[string]any{
		"path":    dir,
		"dry_run": true,
	}

	result, err := handleSetup(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if resp["dry_run"] != true {
		t.Errorf("expected dry_run=true, got %v", resp["dry_run"])
	}
	if resp["project"] != "dryapp" {
		t.Errorf("expected project=dryapp, got %v", resp["project"])
	}
}

func TestHandleNew_DryRun(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: newapp\n"), 0o644)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "new"
	req.Params.Arguments = map[string]any{
		"branch":  "test-branch",
		"path":    dir,
		"dry_run": true,
	}

	result, err := handleNew(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := extractText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if resp["dry_run"] != true {
		t.Errorf("expected dry_run=true, got %v", resp["dry_run"])
	}
	if resp["branch"] != "test-branch" {
		t.Errorf("expected branch=test-branch, got %v", resp["branch"])
	}
	if resp["worktree_path"] == nil || resp["worktree_path"] == "" {
		t.Error("expected worktree_path to be set")
	}
}

func TestHandleNew_NoConfig(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "new"
	req.Params.Arguments = map[string]any{
		"branch": "test-branch",
		"path":   dir,
	}

	result, err := handleNew(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when .treeline.yml is missing")
	}
}

func extractText(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	for _, c := range result.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}
