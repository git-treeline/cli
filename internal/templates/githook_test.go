package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPostCheckoutHook_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	path, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.HasPrefix(content, "#!/bin/sh\n") {
		t.Error("expected shebang line")
	}
	if !strings.Contains(content, hookMarkerStart) {
		t.Error("expected hook marker start")
	}
	if !strings.Contains(content, hookMarkerEnd) {
		t.Error("expected hook marker end")
	}
	if !strings.Contains(content, "gtl setup .") {
		t.Error("expected gtl setup command")
	}
	if !strings.Contains(content, "gtl editor refresh") {
		t.Error("expected gtl editor refresh command")
	}

	info, _ := os.Stat(path)
	if info.Mode()&0o111 == 0 {
		t.Error("hook file should be executable")
	}
}

func TestInstallPostCheckoutHook_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	_ = os.MkdirAll(hooksDir, 0o755)

	existing := "#!/bin/sh\necho 'existing hook'\n"
	_ = os.WriteFile(filepath.Join(hooksDir, "post-checkout"), []byte(existing), 0o755)

	_, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(hooksDir, "post-checkout"))
	content := string(data)

	if !strings.Contains(content, "echo 'existing hook'") {
		t.Error("existing hook content should be preserved")
	}
	if !strings.Contains(content, hookMarkerStart) {
		t.Error("treeline hook should be appended")
	}
}

func TestInstallPostCheckoutHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	_, _ = InstallPostCheckoutHook(dir)
	_, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".git", "hooks", "post-checkout"))
	if strings.Count(string(data), hookMarkerStart) != 1 {
		t.Error("expected exactly 1 hook block after double install")
	}
}

func TestInstallPostCheckoutHook_UpdatesExistingBlock(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	_ = os.MkdirAll(hooksDir, 0o755)

	oldHook := "#!/bin/sh\n\n" + hookMarkerStart + "\nold content\n" + hookMarkerEnd + "\n"
	_ = os.WriteFile(filepath.Join(hooksDir, "post-checkout"), []byte(oldHook), 0o755)

	_, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(hooksDir, "post-checkout"))
	content := string(data)

	if strings.Contains(content, "old content") {
		t.Error("old hook block content should be replaced")
	}
	if !strings.Contains(content, "gtl setup .") {
		t.Error("new hook block should be present")
	}
}

func TestInstallPostCheckoutHook_IntegratesWithHusky(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, ".husky"), 0o755)

	path, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatalf("husky integration should succeed: %v", err)
	}

	expected := filepath.Join(dir, ".husky", "post-checkout")
	if path != expected {
		t.Errorf("expected hook at %s, got %s", expected, path)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), hookMarkerStart) {
		t.Error("expected hook block in husky post-checkout")
	}
}

func TestInstallPostCheckoutHook_IntegratesWithLefthook(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	lefthookConfig := "pre-commit:\n  commands:\n    lint:\n      run: echo lint\n"
	_ = os.WriteFile(filepath.Join(dir, "lefthook.yml"), []byte(lefthookConfig), 0o644)

	path, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatalf("lefthook integration should succeed: %v", err)
	}

	if filepath.Base(path) != "lefthook.yml" {
		t.Errorf("expected lefthook.yml, got %s", filepath.Base(path))
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "git-treeline") {
		t.Error("expected git-treeline command key in lefthook.yml")
	}
	if !strings.Contains(content, "pre-commit") {
		t.Error("existing pre-commit config should be preserved")
	}
	if !strings.Contains(content, "post-checkout") {
		t.Error("expected post-checkout section added")
	}
}

func TestInstallPostCheckoutHook_LefthookIdempotent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	lefthookConfig := "pre-commit:\n  commands:\n    lint:\n      run: echo lint\n"
	_ = os.WriteFile(filepath.Join(dir, "lefthook.yml"), []byte(lefthookConfig), 0o644)

	_, _ = InstallPostCheckoutHook(dir)
	_, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "lefthook.yml"))
	if strings.Count(string(data), "git-treeline") != 1 {
		t.Error("expected exactly 1 git-treeline entry after double install")
	}
}

func TestInstallPostCheckoutHook_LefthookExistingPostCheckout(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	lefthookConfig := "post-checkout:\n  commands:\n    notify:\n      run: echo switched\n"
	_ = os.WriteFile(filepath.Join(dir, "lefthook.yml"), []byte(lefthookConfig), 0o644)

	_, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "lefthook.yml"))
	content := string(data)

	if !strings.Contains(content, "notify") {
		t.Error("existing post-checkout command should be preserved")
	}
	if !strings.Contains(content, "git-treeline") {
		t.Error("git-treeline should be appended")
	}
}

func TestInstallPostCheckoutHook_IntegratesWithPreCommit(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	preCommitConfig := "repos:\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v4.5.0\n    hooks:\n      - id: trailing-whitespace\n"
	_ = os.WriteFile(filepath.Join(dir, ".pre-commit-config.yaml"), []byte(preCommitConfig), 0o644)

	path, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatalf("pre-commit integration should succeed: %v", err)
	}

	if filepath.Base(path) != ".pre-commit-config.yaml" {
		t.Errorf("expected .pre-commit-config.yaml, got %s", filepath.Base(path))
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "id: git-treeline") {
		t.Error("expected git-treeline hook id")
	}
	if !strings.Contains(content, "post-checkout") {
		t.Error("expected post-checkout stage")
	}
	if !strings.Contains(content, "trailing-whitespace") {
		t.Error("existing hooks should be preserved")
	}
	if !strings.Contains(content, "always_run: true") {
		t.Error("expected always_run: true for post-checkout hook")
	}
}

func TestInstallPostCheckoutHook_PreCommitIdempotent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	preCommitConfig := "repos:\n  - repo: local\n    hooks:\n      - id: check\n"
	_ = os.WriteFile(filepath.Join(dir, ".pre-commit-config.yaml"), []byte(preCommitConfig), 0o644)

	_, _ = InstallPostCheckoutHook(dir)
	_, err := InstallPostCheckoutHook(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".pre-commit-config.yaml"))
	if strings.Count(string(data), "id: git-treeline") != 1 {
		t.Error("expected exactly 1 git-treeline entry after double install")
	}
}

func TestResolveHooksDir_DefaultGitHooks(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	hooksDir, manager := resolveHooksDir(dir)
	expected := filepath.Join(dir, ".git", "hooks")
	if hooksDir != expected {
		t.Errorf("expected %s, got %s", expected, hooksDir)
	}
	if manager != "git" {
		t.Errorf("expected manager 'git', got '%s'", manager)
	}
}

func TestHookBlockContent(t *testing.T) {
	if !strings.Contains(hookBlock, "git rev-parse --git-common-dir") {
		t.Error("hook should detect worktree via git-common-dir")
	}
	if !strings.Contains(hookBlock, "command -v gtl") {
		t.Error("hook should gracefully degrade when gtl is not installed")
	}
	if !strings.Contains(hookBlock, "gtl port") {
		t.Error("hook should use gtl port to check provisioning status")
	}
	if !strings.Contains(hookBlock, "gtl prune --stale") {
		t.Error("hook should include background stale prune")
	}
	if !strings.Contains(hookBlock, "&") {
		t.Error("background prune should be backgrounded with &")
	}
}

func TestHookRunScriptContent(t *testing.T) {
	if !strings.Contains(hookRunScript, "gtl prune --stale") {
		t.Error("run script should include background stale prune")
	}
}
