package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/git-treeline/internal/registry"
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
	t.Cleanup(func() { registryPath = "" })
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
	if text != "3000" {
		t.Errorf("expected 3000, got %s", text)
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
