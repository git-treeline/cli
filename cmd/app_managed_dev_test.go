package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/service"
	"github.com/spf13/cobra"
)

func TestAppManagedDevHelper(t *testing.T) {
	if os.Getenv("GTL_APP_DEV_TEST_HELPER") != "1" {
		return
	}
	Version = "1.0.0"
	updateNoticeVersion = "9.9.9"
	doctorCheckHealth = func(int, string) []service.HealthCheck { return nil }
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

func appDevFixture(t *testing.T) introspectionFixture {
	t.Helper()
	f := newIntrospectionFixture(t, "project: fixture\nports_needed: 1\ndatabase: {name: old_name}\nenv: {PORT: '{port}'}\ncommands: {setup: [touch setup-ran]}\n", false)
	var err error
	f.home, err = filepath.EvalSymlinks(f.home)
	if err != nil {
		t.Fatal(err)
	}
	f.state = filepath.Join(f.home, ".treeline-dev", "gtl")
	stable := filepath.Join(f.home, "Library", "Application Support", "git-treeline")
	if err := os.MkdirAll(stable, 0700); err != nil {
		t.Fatal(err)
	}
	writeIntrospectionFile(t, filepath.Join(stable, "registry.json"), `{"version":2,"allocations":[{"worktree":"/stable/work","allocation_generation":"must-not-be-read"}]}`)
	writeIntrospectionFile(t, filepath.Join(stable, "config.json"), `{"database":"stable-sentinel","redis":"stable-sentinel"}`)
	for _, command := range []string{"git", "sh", "bash", "touch", "launchctl", "systemctl", "sudo", "pfctl", "iptables", "redis-cli", "psql", "createdb", "dropdb", "cloudflared", "security", "curl", "open", "kill", "lsof"} {
		if err := os.WriteFile(filepath.Join(f.bin, command), []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$TRAP_LOG\"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func runAppDev(t *testing.T, f introspectionFixture, home string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argv := append([]string{"-test.run=^TestAppManagedDevHelper$", "--"}, args...)
	command := exec.CommandContext(ctx, os.Args[0], argv...)
	command.Dir = f.work
	command.Env = []string{"GTL_APP_DEV_TEST_HELPER=1", "TREELINE_MANAGED=app", "TREELINE_CHANNEL=dev",
		"HOME=" + f.home, "GTL_HOME=" + home, "PATH=" + f.bin, "TMPDIR=" + f.base, "TRAP_LOG=" + filepath.Join(f.home, "tool-invoked")}
	var out, stderr bytes.Buffer
	command.Stdout, command.Stderr = &out, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("fixture timed out: %s", stderr.String())
	}
	return out.String(), stderr.String(), err
}

func privateAppDevState(t *testing.T, f introspectionFixture, registry string) {
	t.Helper()
	if err := os.MkdirAll(f.state, 0700); err != nil {
		t.Fatal(err)
	}
	writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), registry)
}

func TestAppManagedDevBlocksEffectsBeforeHooks(t *testing.T) {
	operation := "11111111-1111-4111-8111-111111111111"
	commands := [][]string{
		{"setup", "."}, {"setup", ".", "--dry-run"}, {"new", "test"}, {"new", "test", "--no-setup"},
		{"claim", "test"}, {"claim", "test", "--no-setup"}, {"claim", "test", "--request-file", "absent.json"},
		{"claim", "test", "--operation-id", operation}, {"claim", "test", "--inspect-json", "--request-file", "absent.json", "--operation-id", operation},
		{"release", ".", "--force", "--remove-worktree"}, {"release", "--all", "--force"},
		{"release", "inspect", ".", "--operation-id", operation}, {"release", "start"}, {"release", "resume", operation}, {"db", "drop"}, {"db", "reset"},
		{"db", "restore", "absent.dump"}, {"start"}, {"stop"}, {"restart"}, {"provision", "."}, {"install"},
		{"serve", "install"}, {"serve", "uninstall"}, {"serve", "restart"}, {"serve", "run"}, {"serve", "reload-pf"},
		{"serve", "hosts", "sync"}, {"serve", "hosts", "clean"}, {"tunnel"}, {"tunnel", "setup"}, {"tunnel", "reset"},
		{"config", "set", "port.base", "45000"}, {"registry", "repair", "--force"}, {"registry", "forget", "."},
		{"status"}, {"status", "--json", "--watch"}, {"env", "--json"}, {"env", "--json", "--read-only", "--template"},
		{"doctor", "--json", "--read-only", "--fix"}, {"related"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := appDevFixture(t)
			before := snapshotIntrospection(t, f.base)
			_, stderr, err := runAppDev(t, f, f.state, args...)
			if err == nil || !strings.Contains(stderr, "app-managed dev resources are not configured") {
				t.Fatalf("command escaped policy: %v %s", err, stderr)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func TestAppManagedDevResourceInspectionDoesNotWrite(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			f := appDevFixture(t)
			privateAppDevState(t, f, `{"version":2,"allocations":[]}`)
			if configured {
				stable := filepath.Join(f.home, "Library", "Application Support", "git-treeline")
				writeIntrospectionFile(t, filepath.Join(stable, "config.json"), `{"port":{"base":3002,"increment":10,"reservations":{"@treeline-dev-qa/slot-01":42000,"@treeline-dev-qa/slot-02":42010,"@treeline-dev-qa/slot-03":42020,"@treeline-dev-qa/slot-04":42030}}}`)
				writeIntrospectionFile(t, filepath.Join(stable, "registry.json"), `{"version":2,"allocations":[]}`)
				writeIntrospectionFile(t, filepath.Join(f.state, "config.json"), `{"port":{"base":42000,"increment":10,"reservations":{"fixture":42000}},"router":{"mode":"disabled"}}`)
				if err := os.Chmod(filepath.Join(f.state, "config.json"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotIntrospection(t, f.base)
			out, stderr, err := runAppDev(t, f, f.state, "dev-resources", "--json")
			if configured {
				var result struct {
					Ready    bool   `json:"ready"`
					Contract string `json:"contract"`
				}
				if err != nil || json.Unmarshal([]byte(out), &result) != nil || !result.Ready || result.Contract != "mac-dev-port-pool-v1" {
					t.Fatalf("configured inspection failed: %v %s %s", err, out, stderr)
				}
			} else if err == nil || strings.TrimSpace(out) != "" {
				t.Fatalf("unconfigured inspection claimed readiness: %v %s %s", err, out, stderr)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func TestAppManagedDevRequiresPrivateCanonicalHome(t *testing.T) {
	for _, variant := range []string{"missing", "shared", "relative", "dotdot", "parent-symlink", "home-symlink", "public-mode", "writable-parent", "registry-symlink", "config-symlink"} {
		t.Run(variant, func(t *testing.T) {
			f := appDevFixture(t)
			home := f.state
			switch variant {
			case "missing":
				home = ""
			case "shared":
				home = filepath.Join(f.home, "Library", "Application Support", "git-treeline")
			case "relative":
				home = ".treeline-dev/gtl"
			case "dotdot":
				home += "/../gtl"
			case "parent-symlink":
				if err := os.Symlink(filepath.Join(f.home, "Library"), filepath.Dir(home)); err != nil {
					t.Fatal(err)
				}
			case "home-symlink":
				if err := os.Mkdir(filepath.Dir(home), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(f.home, "Library"), home); err != nil {
					t.Fatal(err)
				}
			case "writable-parent":
				if err := os.MkdirAll(home, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Dir(home), 0777); err != nil {
					t.Fatal(err)
				}
			case "registry-symlink", "config-symlink":
				if err := os.MkdirAll(home, 0700); err != nil {
					t.Fatal(err)
				}
				name := strings.TrimSuffix(variant, "-symlink") + ".json"
				if err := os.Symlink(filepath.Join(f.home, "Library", "Application Support", "git-treeline", name), filepath.Join(home, name)); err != nil {
					t.Fatal(err)
				}
			case "public-mode":
				if err := os.MkdirAll(home, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(home, 0755); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotIntrospection(t, f.base)
			_, stderr, err := runAppDev(t, f, home, "status", "--json")
			if err == nil || !strings.Contains(stderr, "canonical private GTL_HOME") {
				t.Fatalf("accepted %s: %v %s", variant, err, stderr)
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func TestAppManagedDevInspectionsDoNotWrite(t *testing.T) {
	for _, exists := range []bool{false, true} {
		for _, args := range [][]string{{"status", "--json"}, {"env", "--json", "--read-only"}, {"env", "show", "--json", "--read-only"},
			{"related", "--json"}, {"claim", "test", "--inspect-json", "--operation-id", "11111111-1111-4111-8111-111111111111"}, {"routes", "--json", "--read-only"}, {"doctor", "--json", "--read-only"}, {"version"}, {"help", "setup"},
			{"release", "status", "11111111-1111-4111-8111-111111111111"}} {
			t.Run(fmt.Sprintf("existing=%t/%s", exists, strings.Join(args, " ")), func(t *testing.T) {
				f := appDevFixture(t)
				if exists {
					privateAppDevState(t, f, `{"version":2,"allocations":[],"edges":[]}`)
				}
				// Only non-writing Git probes are reachable in these inspections.
				git := "#!/bin/sh\ncase \"$*\" in\n*remote*|*config*) printf 'https://github.com/example/fixture.git\\n';;\n*symbolic-ref*) printf 'main\\n';;\n*) exit 1;;\nesac\n"
				if err := os.WriteFile(filepath.Join(f.bin, "git"), []byte(git), 0700); err != nil {
					t.Fatal(err)
				}
				before := snapshotIntrospection(t, f.base)
				out, stderr, err := runAppDev(t, f, f.state, args...)
				if strings.Contains(stderr, "app-managed dev") {
					t.Fatalf("inspection refused: %s", stderr)
				}
				if args[0] == "status" && (err != nil || strings.TrimSpace(out) != "[]") {
					t.Fatalf("private empty registry read: %v %s %s", err, out, stderr)
				}
				switch args[0] {
				case "related":
					if err != nil || strings.TrimSpace(out) != "[]" {
						t.Fatalf("related shape: %v %s %s", err, out, stderr)
					}
				case "doctor":
					var result map[string]any
					if err != nil || json.Unmarshal([]byte(out), &result) != nil || result["machine"] == nil || result["project"] == nil {
						t.Fatalf("doctor shape: %v %s %s", err, out, stderr)
					}
				case "release":
					var result map[string]any
					if err == nil || json.Unmarshal([]byte(out), &result) != nil || result["outcome"] != "not_found" {
						t.Fatalf("release missing operation: %v %s %s", err, out, stderr)
					}
				case "claim":
					if err == nil || !strings.Contains(stderr, "claim_inspection_refused") {
						t.Fatalf("claim missing Git identity: %v %s %s", err, out, stderr)
					}
				}
				if strings.Contains(stderr+out, "9.9.9") {
					t.Fatal("root update hook ran")
				}
				assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
			})
		}
	}
}

func TestAppManagedDevStatusKeepsBranchesAndReleaseValidation(t *testing.T) {
	f := appDevFixture(t)
	privateAppDevState(t, f, fmt.Sprintf(`{"version":2,"allocations":[{"worktree":%q,"project":"fixture","branch":"old","ports":[]}],"edges":[]}`, f.work))
	// Read-only Git fixtures return a changed branch without invoking any other tool.
	git := "#!/bin/sh\ncase \"$*\" in\n*branch*|*symbolic-ref*) printf 'new-branch\\n';;\n*remote*|*config*) printf 'https://github.com/example/fixture.git\\n';;\nesac\n"
	if err := os.WriteFile(filepath.Join(f.bin, "git"), []byte(git), 0700); err != nil {
		t.Fatal(err)
	}
	before := snapshotIntrospection(t, f.base)
	_, stderr, err := runAppDev(t, f, f.state, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v %s", err, stderr)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
	writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), fmt.Sprintf(`{"version":2,"allocations":[{"worktree":%q,"allocation_generation":"opaque"}]}`, f.work))
	before = snapshotIntrospection(t, f.base)
	_, stderr, err = runAppDev(t, f, f.state, "status", "--json")
	if err == nil || !strings.Contains(stderr, "enrollment identities require schema 3") {
		t.Fatalf("mixed registry accepted: %v %s", err, stderr)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
}

func TestAppManagedDevLeavesOtherChannelsUnchanged(t *testing.T) {
	for _, mode := range [][2]string{{"", ""}, {"app", "stable"}, {"", "dev"}, {"other", "dev"}} {
		t.Run(strings.Join(mode[:], "/"), func(t *testing.T) {
			t.Setenv("TREELINE_MANAGED", mode[0])
			t.Setenv("TREELINE_CHANNEL", mode[1])
			t.Setenv("GTL_HOME", "/generic-dev-home")
			if appManagedDev() {
				t.Fatal("normal CLI treated as app-managed dev")
			}
			if err := guardAppManagedDev(&cobra.Command{Use: "setup"}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppManagedDevAllowsOwnedReadableParent(t *testing.T) {
	f := appDevFixture(t)
	privateAppDevState(t, f, `{"version":2,"allocations":[],"edges":[]}`)
	if err := os.Chmod(filepath.Dir(f.state), 0755); err != nil {
		t.Fatal(err)
	}
	before := snapshotIntrospection(t, f.base)
	_, stderr, err := runAppDev(t, f, f.state, "status", "--json")
	if err != nil {
		t.Fatalf("owned parent refused: %v %s", err, stderr)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
}
