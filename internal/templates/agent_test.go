package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/detect"
)

func TestWriteAgentContext_CreatesAgentsMD(t *testing.T) {
	dir := t.TempDir()
	det := &detect.Result{Framework: "nextjs", EnvFile: ".env.local"}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(dir, "AGENTS.md")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "## Git Treeline") {
		t.Error("expected Git Treeline header")
	}
	if !strings.Contains(content, "gtl port") {
		t.Error("expected gtl port instruction")
	}
	if !strings.Contains(content, "Do not assume port 3000") {
		t.Error("expected port warning")
	}
	if !strings.Contains(content, ".env.local") {
		t.Error("expected .env.local reference")
	}
}

func TestWriteAgentContext_AppendsToExistingAgentsMD(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	_ = os.WriteFile(agentsPath, []byte("# Project Rules\n\nSome existing content.\n"), 0o644)

	det := &detect.Result{Framework: "rails", HasRedis: true, EnvFile: ".env.local"}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path != agentsPath {
		t.Errorf("expected agents path, got %s", path)
	}

	data, _ := os.ReadFile(agentsPath)
	content := string(data)

	if !strings.Contains(content, "# Project Rules") {
		t.Error("original content should be preserved")
	}
	if !strings.Contains(content, "## Git Treeline") {
		t.Error("expected Git Treeline section appended")
	}
	if !strings.Contains(content, "REDIS_URL") {
		t.Error("expected REDIS_URL for rails with redis")
	}
}

func TestWriteAgentContext_SkipsDuplicateAppend(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	_ = os.WriteFile(agentsPath, []byte("# Rules\n\n## Git Treeline\n\nAlready here.\n"), 0o644)

	det := &detect.Result{Framework: "node", EnvFile: ".env"}

	_, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(agentsPath)
	if strings.Count(string(data), "## Git Treeline") != 1 {
		t.Error("should not duplicate the treeline section")
	}
}

func TestWriteAgentContext_FallsBackToClaudeMD(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "CLAUDE.md")
	_ = os.WriteFile(claudePath, []byte("# Claude Config\n"), 0o644)

	det := &detect.Result{Framework: "node", EnvFile: ".env"}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path != claudePath {
		t.Errorf("expected CLAUDE.md path, got %s", path)
	}

	data, _ := os.ReadFile(claudePath)
	content := string(data)
	if !strings.Contains(content, "# Claude Config") {
		t.Error("original content should be preserved")
	}
	if !strings.Contains(content, "gtl port") {
		t.Error("expected gtl port in appended section")
	}
}

func TestWriteAgentContext_PrefersAgentsMDOverClaudeMD(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	claudePath := filepath.Join(dir, "CLAUDE.md")
	_ = os.WriteFile(agentsPath, []byte("# Agents\n"), 0o644)
	_ = os.WriteFile(claudePath, []byte("# Claude\n"), 0o644)

	det := &detect.Result{Framework: "node", EnvFile: ".env"}

	path, err := WriteAgentContext(dir, "myapp", det)
	if err != nil {
		t.Fatal(err)
	}
	if path != agentsPath {
		t.Error("should prefer AGENTS.md over CLAUDE.md")
	}

	claudeData, _ := os.ReadFile(claudePath)
	if strings.Contains(string(claudeData), "Git Treeline") {
		t.Error("should not have modified CLAUDE.md when AGENTS.md exists")
	}
}
