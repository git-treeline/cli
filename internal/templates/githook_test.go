package templates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPostCheckoutHook_BareBackedWorktreeUsesCommonHooks(t *testing.T) {
	seed := t.TempDir()
	bare := filepath.Join(t.TempDir(), "repo.git")
	worktree := filepath.Join(t.TempDir(), "feature")
	for _, args := range [][]string{
		{"git", "init", "--initial-branch=main", seed},
		{"git", "-C", seed, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"},
		{"git", "clone", "--bare", seed, bare},
		{"git", "--git-dir=" + bare, "worktree", "add", worktree, "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	path, err := InstallPostCheckoutHook(bare)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bare, "hooks", "post-checkout")
	if path != want {
		t.Fatalf("hook path = %q, want %q", path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected hook at common hooks dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bare, ".git", "hooks", "post-checkout")); !os.IsNotExist(err) {
		t.Fatalf("unexpected hook under bare/.git/hooks: %v", err)
	}
}

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

func TestInstalledPostCheckoutHookHonorsDeferredSetup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	shellPath := func(path string) string {
		return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--initial-branch=main")
	run("commit", "--allow-empty", "-m", "init")

	customMarker := filepath.Join(dir, "custom-ran")
	hookPath := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\ntouch "+shellPath(customMarker)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallPostCheckoutHook(dir); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gtlLog := filepath.Join(dir, "gtl-ran")
	fakeGTL := filepath.Join(binDir, "gtl")
	if err := os.WriteFile(fakeGTL, []byte("#!/bin/sh\ntouch "+shellPath(gtlLog)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(hookPath, "old", "new", "1")
	hook.Dir = dir
	hook.Env = append(os.Environ(),
		"GTL_DEFER_SETUP=1",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("running installed hook: %v\n%s", err, out)
	}
	if _, err := os.Stat(customMarker); err != nil {
		t.Fatalf("custom hook command did not run: %v", err)
	}
	if _, err := os.Stat(gtlLog); !os.IsNotExist(err) {
		t.Fatalf("generated Treeline block ran during deferral: %v", err)
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

func TestGeneratedHookScriptsHonorDeferredSetup(t *testing.T) {
	dir := t.TempDir()
	run := exec.Command("git", "init", "--initial-branch=main")
	run.Dir = dir
	run.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	commit := exec.Command("git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init")
	commit.Dir = dir
	commit.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	worktreeDir := filepath.Join(t.TempDir(), "feature")
	add := exec.Command("git", "worktree", "add", "-b", "feature", worktreeDir, "main")
	add.Dir = dir
	add.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	binDir := t.TempDir()
	fakeGTL := filepath.Join(binDir, "gtl")
	if err := os.WriteFile(fakeGTL, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$GTL_TEST_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for name, script := range map[string]string{
		"shell block":       scriptBody(hookBlock),
		"manager one-liner": hookRunScript,
	} {
		t.Run(name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "gtl.log")
			baseEnv := append(os.Environ(),
				"GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_CONFIG_NOSYSTEM=1",
				"GTL_TEST_LOG="+logPath,
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			)

			deferred := exec.Command("sh", "-c", script)
			deferred.Dir = worktreeDir
			deferred.Env = append(baseEnv, "GTL_DEFER_SETUP=1")
			if out, err := deferred.CombinedOutput(); err != nil {
				t.Fatalf("deferred hook: %v\n%s", err, out)
			}
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatalf("gtl ran during deferral: %v", err)
			}

			normal := exec.Command("sh", "-c", script)
			normal.Dir = worktreeDir
			normal.Env = baseEnv
			if out, err := normal.CombinedOutput(); err != nil {
				t.Fatalf("normal hook: %v\n%s", err, out)
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("normal hook did not invoke gtl: %v", err)
			}
			if !strings.Contains(string(data), "port") || !strings.Contains(string(data), "editor refresh") {
				t.Fatalf("normal hook invocation = %q", data)
			}
		})
	}
}

func scriptBody(block string) string {
	return strings.TrimPrefix(block, hookMarkerStart+"\n")
}
