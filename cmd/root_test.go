package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/git-treeline/cli/internal/service"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestPreRunDryRunDoesNotCreateState(t *testing.T) {
	for _, preview := range []bool{true, false} {
		name := "normal"
		if preview {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			t.Setenv("GTL_HOME", stateDir)
			t.Setenv("GTL_NO_STALE_WARN", "1")
			t.Setenv("GTL_NO_UPDATE_NOTIFY", "1")
			command := &cobra.Command{Use: "setup"}
			command.Flags().Bool("dry-run", preview, "")
			if err := rootCmd.PersistentPreRunE(command, nil); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(stateDir)
			if preview && !os.IsNotExist(err) {
				t.Fatalf("preview created state directory: %v", err)
			}
			if !preview && err != nil {
				t.Fatalf("normal command did not initialize state: %v", err)
			}
		})
	}
}

func TestShouldWarnStaleRouter_Truthy(t *testing.T) {
	if !shouldWarnStaleRouter("status", "0.39.4", "0.39.2", "") {
		t.Error("expected warning when version mismatches and command is not self-repairing")
	}
}

func TestShouldWarnStaleRouter_SuppressedByEnv(t *testing.T) {
	if shouldWarnStaleRouter("status", "0.39.4", "0.39.2", "1") {
		t.Error("GTL_NO_STALE_WARN should suppress")
	}
}

func TestShouldWarnStaleRouter_SuppressedDuringSelfRepair(t *testing.T) {
	for _, c := range []string{"install", "serve"} {
		if shouldWarnStaleRouter(c, "0.39.4", "0.39.2", "") {
			t.Errorf("%q is self-repairing — should not warn", c)
		}
	}
}

func TestShouldWarnStaleRouter_QuietWhenVersionsMatch(t *testing.T) {
	if shouldWarnStaleRouter("status", "0.39.4", "0.39.4", "") {
		t.Error("matching versions should not warn")
	}
}

func TestShouldWarnStaleRouter_QuietForDevBuild(t *testing.T) {
	if shouldWarnStaleRouter("status", "dev", "0.39.2", "") {
		t.Error("dev builds shouldn't nag (unstable Version string)")
	}
}

func TestShouldWarnStaleRouter_QuietWhenNoRunningRouter(t *testing.T) {
	if shouldWarnStaleRouter("status", "0.39.4", "", "") {
		t.Error("router never started — nothing to warn about")
	}
}

// A subprocess runs the real Cobra root without sharing flags or cwd between cases.
func TestReadOnlyRootHelper(t *testing.T) {
	if os.Getenv("GTL_READONLY_TEST_HELPER") != "1" {
		return
	}
	Version = "1.0.0"
	updateNoticeVersion = "9.9.9"
	doctorCheckHealth = func(port int, version string) []service.HealthCheck {
		return []service.HealthCheck{{Name: "fixture", Status: "ok", Detail: strconv.Itoa(port)}}
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		os.Exit(2)
	}
	rootCmd.SetArgs(os.Args[separator+1:])
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type introspectionFixture struct{ base, work, state, home, bin string }

func newIntrospectionFixture(t *testing.T, content string, allocated bool) introspectionFixture {
	t.Helper()
	base := t.TempDir()
	f := introspectionFixture{base, filepath.Join(base, "work"), filepath.Join(base, "state"), filepath.Join(base, "home"), filepath.Join(base, "bin")}
	for _, dir := range []string{f.work, f.home, f.bin} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if content != "" {
		writeIntrospectionFile(t, filepath.Join(f.work, ".treeline.yml"), content)
	}
	writeIntrospectionFile(t, filepath.Join(f.work, ".env.local"), "LITERAL=${PROVIDER_TOKEN}\nFROM_FILE=present\n")
	writeIntrospectionFile(t, filepath.Join(f.work, ".env.test"), "LITERAL=${PROVIDER_TOKEN}\nFROM_FILE=present\n")
	for _, command := range []string{"launchctl", "systemctl", "sudo", "pfctl", "iptables", "git"} {
		path := filepath.Join(f.bin, command)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{"bash", "sh", "touch"} {
		path := filepath.Join(f.bin, command)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf invoked > \"$HOME/command-invoked\"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if allocated {
		if err := os.Mkdir(f.state, 0755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]any{"version": 2, "allocations": []any{map[string]any{"worktree": f.work, "project": "recorded", "branch": "feature", "ports": []int{0}, "links": map[string]string{"api": "preview"}}}})
		if err != nil {
			t.Fatal(err)
		}
		writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), string(data))
		writeIntrospectionFile(t, filepath.Join(f.state, "config.json"), `{"router":{"port":43210}}`)
	}
	return f
}

func writeIntrospectionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
}

type introspectionEntry struct {
	info    os.FileInfo
	content string
}

func snapshotIntrospection(t *testing.T, root string) map[string]introspectionEntry {
	t.Helper()
	result := map[string]introspectionEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		data := ""
		if info.Mode().IsRegular() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			data = string(b)
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		result[rel] = introspectionEntry{info, data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func assertIntrospectionUnchanged(t *testing.T, before, after map[string]introspectionEntry) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("directory entries changed: before=%v after=%v", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	}
	for path, b := range before {
		a, ok := after[path]
		if !ok || b.content != a.content || b.info.Mode() != a.info.Mode() || !os.SameFile(b.info, a.info) {
			t.Fatalf("changed bytes/mode/inode: %s", path)
		}
	}
}
func runIntrospection(t *testing.T, f introspectionFixture, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argv := append([]string{"-test.run=^TestReadOnlyRootHelper$", "--"}, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], argv...)
	cmd.Dir = f.work
	cmd.Env = []string{"GTL_READONLY_TEST_HELPER=1", "HOME=" + f.home, "GTL_HOME=" + f.state, "PATH=" + f.bin, "TMPDIR=" + f.base, "PROVIDER_TOKEN=process-only-secret", "PROCESS_ONLY=must-not-appear"}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("fixture timed out: %s", stderr.String())
	}
	return out.String(), stderr.String(), err
}

func TestReadOnlyRootLegacyMatrix(t *testing.T) {
	families := map[string]string{
		"database": "database:\n  pattern: legacy_{worktree}\n",
		"branch":   "default_branch: release\n",
		"commands": "setup_commands:\n  - touch sentinel\nstart_command: touch sentinel\n",
		"env":      "env_file:\n  target: .env.test\n  source: .env.seed\n",
		"editor":   "editor:\n  vscode_title: Legacy\n",
		"ports":    "ports_needed: 4\n",
	}
	commands := [][]string{{"routes"}, {"env"}, {"env", "show"}, {"doctor"}}
	for family, legacy := range families {
		for _, command := range commands {
			t.Run(family+"/"+strings.Join(command, "-"), func(t *testing.T) {
				f := newIntrospectionFixture(t, "project: fixture\nhooks:\n  pre_setup: [touch sentinel]\n  post_setup: [touch sentinel]\nenv:\n  LITERAL: '${PROVIDER_TOKEN}'\n"+legacy, true)
				before := snapshotIntrospection(t, f.base)
				out, stderr, err := runIntrospection(t, f, append(slices.Clone(command), "--json", "--read-only")...)
				if err != nil {
					t.Fatalf("%v: %s", err, stderr)
				}
				if strings.Contains(stderr, "new version") || strings.Contains(stderr, "Router is running") {
					t.Fatalf("notice ran: %s", stderr)
				}
				var data map[string]any
				if err := json.Unmarshal([]byte(out), &data); err != nil {
					t.Fatalf("not JSON: %s %v", out, err)
				}
				switch command[0] {
				case "routes":
					if data["project"] != "fixture" || data["branch"] != "feature" {
						t.Fatalf("routes=%s", out)
					}
				case "env":
					vars := data["vars"].(map[string]any)
					if vars["LITERAL"] != "${PROVIDER_TOKEN}" || vars["PROCESS_ONLY"] != nil || strings.Contains(out, "process-only-secret") {
						t.Fatalf("expanded process credentials: %s", out)
					}
					want := ".env.local"
					if family == "env" {
						want = ".env.test"
					}
					if data["file"] != want {
						t.Fatalf("lost actual invocation flag: %s", out)
					}
				case "doctor":
					project := data["project"].(map[string]any)
					drift := project["project_drift"].(map[string]any)
					if drift["registry_name"] != "recorded" || drift["yaml_project"] != "fixture" {
						t.Fatalf("drift=%s", out)
					}
					alloc := project["allocation"].(map[string]any)
					if alloc["links"].(map[string]any)["api"] != "preview" {
						t.Fatalf("links=%s", out)
					}
				}
				assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
			})
		}
	}
}

func TestReadOnlyRootRejectsBeforeEffects(t *testing.T) {
	cases := [][]string{
		{"routes", "--read-only"}, {"doctor", "--read-only"}, {"env", "--read-only"}, {"env", "show", "--read-only"},
		{"doctor", "--json", "--read-only", "--fix"}, {"env", "--json", "--read-only", "--template"}, {"env", "show", "--json", "--read-only", "--template"},
		{"env", "sync", "--json", "--read-only"}, {"env", "--read-only", "sync", "--json"}, {"setup", "--read-only", "--json"}, {"routes", "--read-only", "--json", "--unknown"},
		{"routes", "--read-only", "--json=false"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			f := newIntrospectionFixture(t, "", false)
			before := snapshotIntrospection(t, f.base)
			out, stderr, err := runIntrospection(t, f, args...)
			if err == nil {
				t.Fatalf("accepted invalid flags: %s %s", out, stderr)
			}
			if json.Valid([]byte(out)) {
				t.Fatalf("success JSON on error: %s", out)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func TestReadOnlyRootStrictErrors(t *testing.T) {
	for _, command := range [][]string{{"routes"}, {"env"}, {"env", "show"}, {"doctor"}} {
		for _, file := range []string{"project", "registry", "user"} {
			if file == "user" && command[0] == "env" {
				continue
			}
			for _, kind := range []string{"malformed", "directory"} {
				t.Run(strings.Join(command, "-")+"/"+file+"/"+kind, func(t *testing.T) {
					f := newIntrospectionFixture(t, "project: fixture\n", true)
					path := filepath.Join(f.state, "registry.json")
					if file == "project" {
						path = filepath.Join(f.work, ".treeline.yml")
					}
					if file == "user" {
						path = filepath.Join(f.state, "config.json")
					}
					if kind == "directory" {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(path, 0750); err != nil {
							t.Fatal(err)
						}
					} else {
						writeIntrospectionFile(t, path, "{ invalid [")
					}
					before := snapshotIntrospection(t, f.base)
					out, stderr, err := runIntrospection(t, f, append(slices.Clone(command), "--json", "--read-only")...)
					if err == nil || json.Valid([]byte(out)) {
						t.Fatalf("laundered error into success: %s %s", out, stderr)
					}
					assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
				})
			}
		}
	}
}

func TestReadOnlyRootMissingAndExistingState(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			f := newIntrospectionFixture(t, "", false)
			if existing {
				if err := os.Mkdir(f.state, 0755); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotIntrospection(t, f.base)
			for _, command := range []string{"doctor", "routes", "env"} {
				out, stderr, err := runIntrospection(t, f, command, "--json", "--read-only")
				if command == "doctor" {
					if err != nil || !json.Valid([]byte(out)) {
						t.Fatalf("missing state doctor failed: %s %s", out, stderr)
					}
				} else if err == nil {
					t.Fatalf("%s succeeded without allocation", command)
				}
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func TestReadOnlyFalseKeepsNormalInitializationAndMigration(t *testing.T) {
	f := newIntrospectionFixture(t, "project: fixture\nports_needed: 3\n", true)
	out, stderr, err := runIntrospection(t, f, "env", "--json", "--read-only=false")
	if err != nil {
		t.Fatalf("%s %s: %v", out, stderr, err)
	}
	info, err := os.Stat(f.state)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("normal root mode=%v", info.Mode())
	}
	data, err := os.ReadFile(filepath.Join(f.work, ".treeline.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ports_needed:") {
		t.Fatal("normal migration suppressed")
	}
}

func TestReadOnlyEnvFileErrors(t *testing.T) {
	for _, kind := range []string{"missing", "directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newIntrospectionFixture(t, "project: fixture\n", true)
			path := filepath.Join(f.work, ".env.local")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotIntrospection(t, f.base)
			out, stderr, err := runIntrospection(t, f, "env", "show", "--json", "--read-only")
			if err == nil || json.Valid([]byte(out)) {
				t.Fatalf("env read error became success: %s %s", out, stderr)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}
