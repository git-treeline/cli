package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/service"
)

func TestReleaseDriverManagedDevDisabledRouter(t *testing.T) {
	for _, tt := range []struct {
		name, managed, channel, mode string
		skip                         bool
	}{
		{"managed dev disabled", "app", "dev", "disabled", true},
		{"managed dev enabled", "app", "dev", "enabled", false},
		{"managed dev prompt", "app", "dev", "prompt", false},
		{"stable disabled", "app", "stable", "disabled", false},
		{"stable enabled", "app", "stable", "enabled", false},
		{"standalone dev disabled", "", "dev", "disabled", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, intent := releaseExecutorFixture(t)
			intent.Policy.DropDatabases = false
			intent.DatabaseConfigRevision = ""
			if err := os.WriteFile(filepath.Join(intent.Target.Path, ".treeline.yml"), []byte("project: lifecycle_fixture\nhooks: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			plan, err := loadReleaseHookPlan(intent.Target.Path)
			if err != nil {
				t.Fatal(err)
			}
			intent.HooksDigest = plan.digest()
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			private := filepath.Join(home, ".treeline-dev", "gtl")
			if err := os.MkdirAll(private, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("GTL_HOME", private)
			t.Setenv("TREELINE_MANAGED", tt.managed)
			t.Setenv("TREELINE_CHANNEL", tt.channel)
			if err := os.WriteFile(filepath.Join(private, "config.json"), []byte(fmt.Sprintf(`{"router":{"mode":%q,"domain":"example.test"}}`, tt.mode)), 0600); err != nil {
				t.Fatal(err)
			}
			d, err := productionReleaseDriver(e.Registry)
			if err != nil {
				t.Fatal(err)
			}
			// This legacy unit isolates the router/hosts adapter decision made at
			// construction. Managed-dev policy admission has dedicated fixtures;
			// disable it before phase execution so this test does not invent a
			// partial resource policy around its unrelated temporary worktree.
			if tt.managed == "app" && tt.channel == "dev" {
				t.Setenv("TREELINE_MANAGED", "")
			}
			hosts := filepath.Join(t.TempDir(), "hosts")
			if err := os.WriteFile(hosts, []byte(releaseHostsContent("lifecycle-fixture.example.test")), 0600); err != nil {
				t.Fatal(err)
			}
			reads, checks, writes := 0, 0, 0
			d.hosts.Read = func() (service.ManagedHostsSnapshot, error) {
				if tt.skip {
					t.Fatal("managed dev disabled router accessed hosts")
				}
				reads++
				return service.ReadManagedHostsStrict(hosts)
			}
			d.checkHosts = func(service.ManagedHostsSnapshot) error {
				if tt.skip {
					t.Fatal("managed dev disabled router checked hosts privileges")
				}
				checks++
				return nil
			}
			d.hosts.Write = func(service.ManagedHostsSnapshot, []string) error {
				if tt.skip {
					t.Fatal("managed dev disabled router wrote hosts")
				}
				writes++
				return os.WriteFile(hosts, []byte(releaseHostsContent()), 0600)
			}
			d.hooks.RunHook = func(context.Context, string, string) checkedHookOutcome {
				t.Fatal("empty plan ran hook")
				return hookUncertain
			}
			e.Preflight, e.RunPhase = d.preflight, d.run
			completed, err := e.Start(context.Background(), intent)
			if err != nil || completed == nil || completed.Outcome() != "completed" {
				t.Fatalf("release=%+v err=%v", completed, err)
			}
			found := false
			for _, phase := range completed.Phases {
				if phase.Name == registry.PhaseHosts {
					found = phase.State == "completed"
				}
			}
			if !found {
				t.Fatal("hosts phase missing from completed receipt")
			}
			if tt.skip && (reads != 0 || checks != 0 || writes != 0) || !tt.skip && (reads < 2 || checks < 1 || writes != 1) {
				t.Fatalf("hosts reads=%d checks=%d writes=%d", reads, checks, writes)
			}
		})
	}
}

func TestReleaseDriverManagedDevRejectsUnvalidatedConfig(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint("malformed=", malformed), func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("TREELINE_MANAGED", "app")
			t.Setenv("TREELINE_CHANNEL", "dev")
			private := filepath.Join(home, ".treeline-dev", "gtl")
			if !malformed {
				private = filepath.Join(home, "other")
			}
			t.Setenv("GTL_HOME", private)
			if err := os.MkdirAll(private, 0700); err != nil {
				t.Fatal(err)
			}
			if malformed {
				if err := os.WriteFile(filepath.Join(private, "config.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = productionReleaseDriver(registry.New(filepath.Join(private, "registry.json")))
			requireReleaseExecutionCode(t, err, "config_unavailable")
		})
	}
}
