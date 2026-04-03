package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/detect"
)

func TestWriteAgentContext_CursorRule(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755)

	det := &detect.Result{
		Framework: "nextjs",
		EnvFile:   ".env.local",
	}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected path to cursor rule")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "gtl status --json") {
		t.Error("expected gtl status --json in cursor rule")
	}
	if !strings.Contains(content, "PORT") {
		t.Error("expected PORT in env var list")
	}
	if !strings.Contains(content, ".env.local") {
		t.Error("expected .env.local in cursor rule")
	}
}

func TestWriteAgentContext_CursorDir_NoRules(t *testing.T) {
	dir := t.TempDir()
	// Only .cursor/ exists, not .cursor/rules/
	_ = os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755)

	det := &detect.Result{Framework: "node", EnvFile: ".env"}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected cursor rule to be created")
	}

	// Should have created .cursor/rules/treeline.mdc
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "rules", "treeline.mdc")); err != nil {
		t.Fatal("expected .cursor/rules/treeline.mdc to be created")
	}
}

func TestWriteAgentContext_ClaudeMD(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "CLAUDE.md")
	_ = os.WriteFile(claudePath, []byte("# My Project\n\nSome content.\n"), 0o644)

	det := &detect.Result{
		Framework: "rails",
		HasRedis:  true,
		EnvFile:   ".env.local",
	}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path != claudePath {
		t.Errorf("expected claude path, got %s", path)
	}

	data, _ := os.ReadFile(claudePath)
	content := string(data)
	if !strings.Contains(content, "# My Project") {
		t.Error("original content should be preserved")
	}
	if !strings.Contains(content, "## Git Treeline") {
		t.Error("expected Git Treeline section appended")
	}
	if !strings.Contains(content, "REDIS_URL") {
		t.Error("expected REDIS_URL in env var list for rails with redis")
	}
}

func TestWriteAgentContext_NeitherExists(t *testing.T) {
	dir := t.TempDir()
	det := &detect.Result{Framework: "node", EnvFile: ".env"}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("expected empty path when no agent config exists, got %s", path)
	}
}
