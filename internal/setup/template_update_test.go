package setup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/config"
)

func templateUpdateGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func templateWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func templateRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func templateRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mainRepo := filepath.Join(dir, "main")
	gitInitRepo(t, mainRepo)
	bareRepo := filepath.Join(dir, "origin.git")
	templateUpdateGit(t, dir, "clone", "--bare", mainRepo, bareRepo)
	templateUpdateGit(t, mainRepo, "remote", "add", "origin", bareRepo)
	return mainRepo
}

func templateConfig(mainRepo, migrate string) *config.ProjectConfig {
	return &config.ProjectConfig{ProjectRoot: mainRepo, Data: map[string]any{
		"merge_target": "main",
		"database":     map[string]any{"adapter": "sqlite", "template": "db/template.sqlite3"},
		"env_file":     ".env",
		"env":          map[string]any{"DATABASE_PATH": "{database}"},
		"commands":     map[string]any{"migrate": migrate},
	}}
}

func TestUpdateTemplateDatabase_UsesFetchedCodeWithoutChangingExistingCheckouts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	isolateEnvOwnership(t)
	mainRepo := templateRepo(t)
	templateWrite(t, filepath.Join(mainRepo, "revision"), "1\n")
	templateWrite(t, filepath.Join(mainRepo, "migrate.sh"), "sqlite3 \"$DATABASE_PATH\" \"UPDATE widgets SET value = $(cat revision)\"\npwd > \"$TEMPLATE_TEST_CWD\"\n")
	templateUpdateGit(t, mainRepo, "add", "revision", "migrate.sh")
	templateUpdateGit(t, mainRepo, "commit", "-m", "initial migration")
	templateUpdateGit(t, mainRepo, "branch", "develop")
	templateUpdateGit(t, mainRepo, "push", "origin", "main", "develop")
	trunk := filepath.Join(filepath.Dir(mainRepo), "existing-develop")
	templateUpdateGit(t, mainRepo, "worktree", "add", trunk, "develop")
	writer := filepath.Join(filepath.Dir(mainRepo), "remote-writer")
	templateUpdateGit(t, filepath.Dir(mainRepo), "clone", "--branch", "develop", filepath.Join(filepath.Dir(mainRepo), "origin.git"), writer)
	templateUpdateGit(t, writer, "config", "user.email", "test@example.com")
	templateUpdateGit(t, writer, "config", "user.name", "Test")
	templateWrite(t, filepath.Join(writer, "revision"), "99\n")
	templateUpdateGit(t, writer, "commit", "-am", "new migration")
	templateUpdateGit(t, writer, "push", "origin", "develop")

	templateWrite(t, filepath.Join(mainRepo, "revision"), "dirty primary content\n")
	rootDB := filepath.Join(mainRepo, "db", "template.sqlite3")
	if err := os.MkdirAll(filepath.Dir(rootDB), 0o755); err != nil {
		t.Fatal(err)
	}
	createSetupSQLiteFixture(t, rootDB, 42)
	worktreeDB := filepath.Join(trunk, "worktree.sqlite3")
	createSetupSQLiteFixture(t, worktreeDB, 7)
	t.Setenv("DATABASE_PATH", worktreeDB)
	cwdFile := filepath.Join(t.TempDir(), "migration-cwd")
	t.Setenv("TEMPLATE_TEST_CWD", cwdFile)
	beforeRefs := templateUpdateGit(t, mainRepo, "show-ref", "--heads")
	beforeWorktrees := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain")
	beforeStatus := templateUpdateGit(t, mainRepo, "status", "--porcelain")
	pc := templateConfig(mainRepo, "sh migrate.sh")
	pc.Data["merge_target"] = "develop"
	var log bytes.Buffer
	if err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &log); err != nil {
		t.Fatalf("update: %v\n%s", err, &log)
	}
	if got := setupSQLiteValue(t, rootDB); got != "99" {
		t.Errorf("template value = %q; want fetched migration value 99", got)
	}
	if got := setupSQLiteValue(t, worktreeDB); got != "7" {
		t.Errorf("existing worktree database changed to %q", got)
	}
	if got := templateUpdateGit(t, mainRepo, "show-ref", "--heads"); got != beforeRefs {
		t.Errorf("local branches changed:\n%s", got)
	}
	if got := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain"); got != beforeWorktrees {
		t.Errorf("worktree registration changed:\n%s", got)
	}
	if got := templateUpdateGit(t, mainRepo, "status", "--porcelain"); got != beforeStatus {
		t.Errorf("primary checkout status changed:\n%s", got)
	}
	if got := templateRead(t, filepath.Join(mainRepo, "revision")); got != "dirty primary content\n" {
		t.Errorf("primary contents changed: %q", got)
	}
	if got := templateRead(t, filepath.Join(trunk, "revision")); got != "1\n" {
		t.Errorf("existing develop contents changed: %q", got)
	}
	scratch := strings.TrimSpace(templateRead(t, cwdFile))
	if scratch == mainRepo || scratch == trunk {
		t.Fatalf("migration ran in existing checkout %q", scratch)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch checkout still exists: %q (%v)", scratch, err)
	}
}

func TestUpdateTemplateDatabase_PreparesTemplateEnvironment(t *testing.T) {
	for _, seedFile := range []string{"config/seed.env", ".env"} {
		t.Run(seedFile, func(t *testing.T) {
			isolateEnvOwnership(t)
			mainRepo := templateRepo(t)
			output := t.TempDir()
			t.Setenv("TEMPLATE_TEST_OUTPUT", output)
			t.Setenv("DATABASE_PATH", "inherited_worktree")
			t.Setenv("DATABASE_ALIAS", "inherited_alias")
			t.Setenv("DATABASE_URL", "inherited_url")
			t.Setenv("SEED_VALUE", "inherited_worktree_value")
			t.Setenv("PGUSER", "inherited_pguser")
			t.Setenv("APP_ENV", "inherited_app_env")
			t.Setenv("EXPANDED", "")
			t.Setenv("MULTILINE", "")
			t.Setenv("PORT", "3999")
			t.Setenv("BUNDLE_GEMFILE", filepath.Join(mainRepo, "Gemfile"))
			seed := "SEED_VALUE=preserved # keep this comment\nPGUSER=seed_pguser\nEXPANDED=${SEED_VALUE}\nMULTILINE=\"first line\nsecond line\"\nDATABASE_PATH=wrong_seed_database\n"
			templateWrite(t, filepath.Join(mainRepo, seedFile), seed)
			templateWrite(t, filepath.Join(mainRepo, "config/runtime.key"), "copied configuration\n")
			pc := templateConfig(mainRepo, `printf '%s\n' "$DATABASE_PATH" "$DATABASE_ALIAS" "$DATABASE_URL" "$APP_ENV" "$SEED_VALUE" "$PGUSER" > "$TEMPLATE_TEST_OUTPUT/vars"; cp config/runtime.key "$TEMPLATE_TEST_OUTPUT/copied"; cp config/migration.env "$TEMPLATE_TEST_OUTPUT/env"; test -z "$EXPANDED" && test -z "$MULTILINE" && test -z "${PORT+x}" && test -z "${BUNDLE_GEMFILE+x}"`)
			pc.Data["env_file"] = map[string]any{"path": "config/migration.env", "seed_from": "config/seed.env"}
			pc.Data["copy_files"] = []any{"config/runtime.key"}
			pc.Data["env"] = map[string]any{
				"DATABASE_PATH": "{database}", "DATABASE_ALIAS": "{database.name}",
				"DATABASE_URL": "sqlite:///{database_1}", "APP_ENV": "development",
				"PORT": "{port}",
			}
			pc.Data["commands"].(map[string]any)["setup"] = []any{"exit 93"}
			pc.Data["hooks"] = map[string]any{"pre_setup": []any{"exit 94"}, "post_setup": []any{"exit 95"}}
			if err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			template := filepath.Join(mainRepo, "db/template.sqlite3")
			want := template + "\n" + template + "\nsqlite:///" + template + "\ndevelopment\ninherited_worktree_value\ninherited_pguser\n"
			if got := templateRead(t, filepath.Join(output, "vars")); got != want {
				t.Errorf("migration environment = %q, want %q", got, want)
			}
			if got := templateRead(t, filepath.Join(output, "copied")); got != "copied configuration\n" {
				t.Errorf("copied configuration = %q", got)
			}
			env := templateRead(t, filepath.Join(output, "env"))
			seedSyntax := strings.TrimSuffix(seed, "DATABASE_PATH=wrong_seed_database\n")
			if !strings.Contains(env, seedSyntax) || !strings.Contains(env, template) || strings.Contains(env, "wrong_seed_database") {
				t.Errorf("seeded migration env has incorrect contents: %q", env)
			}
			if got := templateRead(t, filepath.Join(mainRepo, seedFile)); got != seed {
				t.Errorf("seed source modified: %q", got)
			}
		})
	}
}

func TestUpdateTemplateDatabase_CleansUpAfterMigrationFailure(t *testing.T) {
	isolateEnvOwnership(t)
	mainRepo := templateRepo(t)
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	t.Setenv("TEMPLATE_TEST_CWD", cwdFile)
	before := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain")
	pc := templateConfig(mainRepo, `pwd > "$TEMPLATE_TEST_CWD"; exit 17`)
	if err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &bytes.Buffer{}); err == nil {
		t.Fatal("expected migration failure")
	}
	scratch := strings.TrimSpace(templateRead(t, cwdFile))
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch checkout was not removed: %v", err)
	}
	if got := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain"); got != before {
		t.Errorf("scratch worktree registration leaked:\n%s", got)
	}
}

func TestUpdateTemplateDatabase_RejectsMissingDatabaseTargetBeforeGit(t *testing.T) {
	for _, field := range []string{"database.template", "env"} {
		t.Run(field, func(t *testing.T) {
			mainRepo := t.TempDir()
			pc := templateConfig(mainRepo, "exit 99")
			if field == "database.template" {
				delete(pc.Data["database"].(map[string]any), "template")
			} else {
				pc.Data["env"] = map[string]any{"DATABASE_PATH": "hardcoded_worktree_database"}
			}
			err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Errorf("expected %s validation before Git, got %v", field, err)
			}
		})
	}
}

func TestUpdateTemplateDatabase_DefaultsToOriginHead(t *testing.T) {
	isolateEnvOwnership(t)
	mainRepo := templateRepo(t)
	templateUpdateGit(t, mainRepo, "checkout", "-b", "develop")
	templateWrite(t, filepath.Join(mainRepo, "branch-marker"), "origin default branch\n")
	templateUpdateGit(t, mainRepo, "add", "branch-marker")
	templateUpdateGit(t, mainRepo, "commit", "-m", "default branch marker")
	templateUpdateGit(t, mainRepo, "push", "origin", "develop")
	templateUpdateGit(t, mainRepo, "checkout", "main")
	templateUpdateGit(t, mainRepo, "fetch", "origin")
	templateUpdateGit(t, mainRepo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	output := filepath.Join(t.TempDir(), "branch")
	t.Setenv("TEMPLATE_TEST_OUTPUT", output)
	pc := templateConfig(mainRepo, `cat branch-marker > "$TEMPLATE_TEST_OUTPUT"`)
	pc.Data["merge_target"] = ""
	var log bytes.Buffer
	if err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &log); err != nil {
		t.Fatal(err)
	}
	if got := templateRead(t, output); got != "origin default branch\n" {
		t.Errorf("migration did not use origin's default branch: %q", got)
	}
}

func TestUpdateTemplateDatabase_RejectsUnboundSeedDatabaseURL(t *testing.T) {
	isolateEnvOwnership(t)
	mainRepo := templateRepo(t)
	marker := filepath.Join(t.TempDir(), "migration-ran")
	t.Setenv("TEMPLATE_TEST_MARKER", marker)
	templateWrite(t, filepath.Join(mainRepo, ".env"), "DATABASE_URL=postgresql://localhost/wrong_worktree\n")
	pc := templateConfig(mainRepo, `touch "$TEMPLATE_TEST_MARKER"`)
	before := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain")
	err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected unbound DATABASE_URL rejection, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("migration ran with unbound DATABASE_URL: %v", err)
	}
	if got := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain"); got != before {
		t.Errorf("scratch worktree registration leaked:\n%s", got)
	}
}

func TestUpdateTemplateDatabase_RejectsUnboundFetchedDotenv(t *testing.T) {
	for _, tt := range []struct {
		name, target, dotenv, assignment, railsEnv string
	}{
		{"local target", ".env.local", ".env", "DATABASE_URL=postgresql://localhost/wrong_worktree\n", ""},
		{"development target", ".env.development", ".env", "DATABASE_URL=postgresql://localhost/wrong_worktree\n", ""},
		{"development local", ".env.local", ".env.development.local", "DATABASE_URL=postgresql://localhost/wrong_worktree\n", ""},
		{"explicit staging", ".env.local", ".env.staging.local", "DATABASE_URL=postgresql://localhost/wrong_worktree\n", "staging"},
		{"export assignment", ".env.local", ".env", "export DATABASE_URL=postgresql://localhost/wrong_worktree\n", ""},
		{"spaced assignment", ".env.local", ".env", "  DATABASE_URL = postgresql://localhost/wrong_worktree\n", ""},
		{"export spaced assignment", ".env.local", ".env", "export DATABASE_URL = postgresql://localhost/wrong_worktree\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateEnvOwnership(t)
			t.Setenv("RAILS_ENV", "development")
			mainRepo := templateRepo(t)
			templateWrite(t, filepath.Join(mainRepo, tt.dotenv), tt.assignment)
			templateUpdateGit(t, mainRepo, "add", tt.dotenv)
			templateUpdateGit(t, mainRepo, "commit", "-m", "application dotenv default")
			templateUpdateGit(t, mainRepo, "push", "origin", "main")
			templateWrite(t, filepath.Join(mainRepo, "config/seed.env"), "SEED_ONLY=ordinary\n")
			marker := filepath.Join(t.TempDir(), "migration-ran")
			t.Setenv("TEMPLATE_TEST_MARKER", marker)
			pc := templateConfig(mainRepo, `touch "$TEMPLATE_TEST_MARKER"`)
			pc.Data["env_file"] = map[string]any{"path": tt.target, "seed_from": "config/seed.env"}
			if tt.railsEnv != "" {
				pc.Data["env"].(map[string]any)["RAILS_ENV"] = tt.railsEnv
			}
			before := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain")
			err := UpdateTemplateDatabase(t.Context(), mainRepo, pc, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
				t.Fatalf("expected unbound fetched DATABASE_URL rejection, got %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("migration ran with conflicting dotenv default: %v", err)
			}
			if got := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain"); got != before {
				t.Errorf("scratch worktree registration leaked:\n%s", got)
			}
		})
	}
}

func TestUpdateTemplateDatabase_MappedURLOverridesDotenvDefault(t *testing.T) {
	isolateEnvOwnership(t)
	mainRepo := templateRepo(t)
	templateWrite(t, filepath.Join(mainRepo, ".env"), "DATABASE_URL=postgresql://localhost/wrong_worktree\n")
	templateUpdateGit(t, mainRepo, "add", ".env")
	templateUpdateGit(t, mainRepo, "commit", "-m", "application dotenv default")
	templateUpdateGit(t, mainRepo, "push", "origin", "main")
	templateWrite(t, filepath.Join(mainRepo, "config/seed.env"), "APP_NAME=example\n")
	output := filepath.Join(t.TempDir(), "database-url")
	t.Setenv("TEMPLATE_TEST_OUTPUT", output)
	t.Setenv("DATABASE_URL", "postgresql://localhost/inherited_worktree")
	pc := templateConfig(mainRepo, `if [ -z "${DATABASE_URL+x}" ]; then . ./.env; fi; printf '%s' "$DATABASE_URL" > "$TEMPLATE_TEST_OUTPUT"`)
	pc.Data["env_file"] = map[string]any{"path": ".env.local", "seed_from": "config/seed.env"}
	pc.Data["env"].(map[string]any)["DATABASE_URL"] = "sqlite:///{database}"
	if err := UpdateTemplateDatabase(t.Context(), mainRepo, pc, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := "sqlite:///" + filepath.Join(mainRepo, "db/template.sqlite3")
	if got := templateRead(t, output); got != want {
		t.Errorf("migration DATABASE_URL = %q, want %q", got, want)
	}
}

func TestUpdateTemplateDatabase_IgnoresInactiveProductionDotenv(t *testing.T) {
	isolateEnvOwnership(t)
	t.Setenv("RAILS_ENV", "development")
	t.Setenv("NODE_ENV", "development")
	t.Setenv("APP_ENV", "development")
	mainRepo := templateRepo(t)
	templateWrite(t, filepath.Join(mainRepo, ".env.production"), "DATABASE_URL=postgresql://localhost/production\n")
	templateUpdateGit(t, mainRepo, "add", ".env.production")
	templateUpdateGit(t, mainRepo, "commit", "-m", "inactive production configuration")
	templateUpdateGit(t, mainRepo, "push", "origin", "main")
	marker := filepath.Join(t.TempDir(), "migration-ran")
	t.Setenv("TEMPLATE_TEST_MARKER", marker)
	pc := templateConfig(mainRepo, `touch "$TEMPLATE_TEST_MARKER"`)
	if err := UpdateTemplateDatabase(t.Context(), mainRepo, pc, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("migration did not run: %v", err)
	}
}

func TestUpdateTemplateDatabase_ShadowDatabaseRequiresExplicitStaticTarget(t *testing.T) {
	for _, source := range []string{"inherited", "dotenv"} {
		for _, mapping := range []string{"unmapped", "static", "primary token"} {
			t.Run(source+"/"+mapping, func(t *testing.T) {
				isolateEnvOwnership(t)
				mainRepo := templateRepo(t)
				t.Setenv("SHADOW_DATABASE_URL", "")
				if source == "inherited" {
					t.Setenv("SHADOW_DATABASE_URL", "postgresql://localhost/worktree_shadow")
				} else {
					templateWrite(t, filepath.Join(mainRepo, ".env"), "SHADOW_DATABASE_URL=postgresql://localhost/worktree_shadow\n")
				}
				output := filepath.Join(t.TempDir(), "shadow-url")
				t.Setenv("TEMPLATE_TEST_OUTPUT", output)
				pc := templateConfig(mainRepo, `printf '%s' "$SHADOW_DATABASE_URL" > "$TEMPLATE_TEST_OUTPUT"`)
				safeURL := "postgresql://localhost/template_migration_shadow"
				switch mapping {
				case "static":
					pc.Data["env"].(map[string]any)["SHADOW_DATABASE_URL"] = safeURL
				case "primary token":
					pc.Data["env"].(map[string]any)["SHADOW_DATABASE_URL"] = "sqlite:///{database}"
				}
				err := UpdateTemplateDatabase(t.Context(), mainRepo, pc, &bytes.Buffer{})
				if mapping == "static" {
					if err != nil {
						t.Fatal(err)
					}
					if got := templateRead(t, output); got != safeURL {
						t.Errorf("shadow URL = %q, want explicit safe target %q", got, safeURL)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "SHADOW_DATABASE_URL") {
					t.Fatalf("expected shadow database rejection, got %v", err)
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Errorf("migration ran with unsafe shadow database: %v", err)
				}
			})
		}
	}
}

func TestUpdateTemplateDatabase_RejectsUnsafePreparationDestinations(t *testing.T) {
	for _, field := range []string{"copy_files", "env_file"} {
		for _, kind := range []string{"relative_escape", "absolute_path", "tracked_symlink"} {
			t.Run(field+"/"+kind, func(t *testing.T) {
				isolateEnvOwnership(t)
				mainRepo := templateRepo(t)
				outside := filepath.Join(t.TempDir(), "runtime.env")
				templateWrite(t, outside, "untouched configuration\n")
				marker := filepath.Join(t.TempDir(), "migration-ran")
				t.Setenv("TEMPLATE_TEST_MARKER", marker)
				pc := templateConfig(mainRepo, `touch "$TEMPLATE_TEST_MARKER"`)
				path := "../runtime.env"
				wantError := "relative path within the project"
				switch kind {
				case "absolute_path":
					path = outside
				case "tracked_symlink":
					if err := os.Symlink(filepath.Dir(outside), filepath.Join(mainRepo, "linked")); err != nil {
						t.Fatal(err)
					}
					templateUpdateGit(t, mainRepo, "add", "linked")
					templateUpdateGit(t, mainRepo, "commit", "-m", "linked configuration directory")
					templateUpdateGit(t, mainRepo, "push", "origin", "main")
					path = "linked/runtime.env"
					wantError = "symlink"
				}
				if field == "copy_files" {
					pc.Data[field] = []any{path}
				} else {
					pc.Data[field] = map[string]any{"path": path, "seed_from": ".env"}
				}
				before := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain")
				err := UpdateTemplateDatabase(context.Background(), mainRepo, pc, &bytes.Buffer{})
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("expected unsafe destination rejection, got %v", err)
				}
				if got := templateRead(t, outside); got != "untouched configuration\n" {
					t.Errorf("outside file changed: %q", got)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Errorf("migration ran with unsafe destination: %v", err)
				}
				if got := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain"); got != before {
					t.Errorf("scratch worktree registration leaked:\n%s", got)
				}
			})
		}
	}
}

func TestUpdateTemplateDatabase_CancellationCleansRunningMigrationCheckout(t *testing.T) {
	isolateEnvOwnership(t)
	mainRepo := templateRepo(t)
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	t.Setenv("TEMPLATE_TEST_CWD", cwdFile)
	pc := templateConfig(mainRepo, `mkfifo migration-wait; cat migration-wait & child=$!; pwd > "$TEMPLATE_TEST_CWD"; wait "$child"`)
	before := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- UpdateTemplateDatabase(ctx, mainRepo, pc, &bytes.Buffer{})
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var scratch string
	for scratch == "" {
		select {
		case err := <-done:
			t.Fatalf("migration exited before cancellation: %v", err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for migration to start")
		case <-ticker.C:
			data, err := os.ReadFile(cwdFile)
			if err == nil {
				scratch = strings.TrimSpace(string(data))
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected canceled migration to fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled migration did not finish cleanup")
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch checkout remains after cancellation: %v", err)
	}
	if got := templateUpdateGit(t, mainRepo, "worktree", "list", "--porcelain"); got != before {
		t.Errorf("scratch worktree registration leaked after cancellation:\n%s", got)
	}
}
