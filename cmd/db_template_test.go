package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/setup"
)

func TestDBTemplateUpdateUsesInvokingWorktreeConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	dir := t.TempDir()
	repo := filepath.Join(dir, "main")
	remote := filepath.Join(dir, "remote")
	worktree := filepath.Join(dir, "feature")
	git := func(at string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = at
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(dir, "init", "--initial-branch=develop", repo)
	git(repo, "commit", "--allow-empty", "-m", "initial")
	git(dir, "clone", "--bare", repo, remote)
	git(repo, "remote", "add", "origin", remote)
	git(repo, "worktree", "add", "-b", "feature", worktree)
	yml := `project: demo
merge_target: develop
database:
  template: root_template
env:
  TARGET_DB: "{database}"
commands:
  migrate: printf '%s' "$TARGET_DB" > "$TEMPLATE_RESULT"
`
	configPath := filepath.Join(worktree, ".treeline.yml")
	if err := os.WriteFile(configPath, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "result")
	t.Setenv("TEMPLATE_RESULT", outputPath)
	t.Setenv("TARGET_DB", "feature_database")
	subdir := filepath.Join(worktree, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)
	var output bytes.Buffer
	oldOut := dbTemplateUpdateCmd.OutOrStdout()
	oldContext := dbTemplateUpdateCmd.Context()
	dbTemplateUpdateCmd.SetContext(t.Context())
	dbTemplateUpdateCmd.SetOut(&output)
	t.Cleanup(func() {
		dbTemplateUpdateCmd.SetOut(oldOut)
		dbTemplateUpdateCmd.SetContext(oldContext)
	})
	before := git(repo, "worktree", "list", "--porcelain")
	if err := dbTemplateUpdateCmd.RunE(dbTemplateUpdateCmd, nil); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(outputPath)
	if err != nil || string(result) != "root_template" {
		t.Fatalf("migration target = %q, error %v", result, err)
	}
	if got := git(repo, "worktree", "list", "--porcelain"); got != before {
		t.Fatalf("worktrees changed: %s", got)
	}
	if !strings.Contains(output.String(), "Template database updated") {
		t.Fatalf("missing success output: %s", output.String())
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != yml {
		t.Fatalf("invoking config changed: %s, %v", after, err)
	}
}

func TestDBTemplateUpdateRejectsArguments(t *testing.T) {
	if err := dbTemplateUpdateCmd.Args(dbTemplateUpdateCmd, []string{"unexpected"}); err == nil {
		t.Fatal("expected unexpected argument to be rejected")
	}
}

func TestDBTemplateUpdateConfigurationErrorsSuppressUsage(t *testing.T) {
	for _, tt := range []struct {
		name, database, hint string
	}{
		{name: "missing template", hint: "template: myapp_development"},
		{name: "missing binding", database: "database:\n  template: root_template\n", hint: `POSTGRES_DATABASE: "{database}"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			yml := "project: demo\ncommands:\n  migrate: exit 99\n" + tt.database
			if err := os.WriteFile(filepath.Join(repo, ".treeline.yml"), []byte(yml), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(repo)
			oldSilenceUsage := dbTemplateUpdateCmd.SilenceUsage
			oldContext := dbTemplateUpdateCmd.Context()
			dbTemplateUpdateCmd.SetContext(t.Context())
			dbTemplateUpdateCmd.SilenceUsage = false
			t.Cleanup(func() {
				dbTemplateUpdateCmd.SilenceUsage = oldSilenceUsage
				dbTemplateUpdateCmd.SetContext(oldContext)
			})
			err := dbTemplateUpdateCmd.RunE(dbTemplateUpdateCmd, nil)
			var cliError *CliError
			if !errors.As(err, &cliError) {
				t.Fatalf("expected CliError, got %v", err)
			}
			if !strings.Contains(cliError.Hint, tt.hint) {
				t.Errorf("hint = %q, want %q", cliError.Hint, tt.hint)
			}
			var configErr *setup.TemplateConfigError
			if !errors.As(err, &configErr) {
				t.Error("configuration error cause was lost")
			}
			if !dbTemplateUpdateCmd.SilenceUsage {
				t.Error("domain error did not suppress usage")
			}
		})
	}
}
