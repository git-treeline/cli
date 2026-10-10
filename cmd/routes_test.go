package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/config"
)

func TestBuildRoutes(t *testing.T) {
	tests := []struct {
		name         string
		ports        []int
		project      string
		branch       string
		domain       string
		routerPort   int
		svcRunning   bool
		pfConfigured bool
		wantURLs     []string
	}{
		{
			name:         "router with port forwarding",
			ports:        []int{3010, 3011},
			project:      "myapp",
			branch:       "feature-x",
			domain:       "prt.dev",
			routerPort:   3001,
			svcRunning:   true,
			pfConfigured: true,
			wantURLs: []string{
				"https://myapp-feature-x.prt.dev",
				"https://myapp-feature-x.prt.dev",
			},
		},
		{
			name:         "router without port forwarding",
			ports:        []int{3010, 3011},
			project:      "myapp",
			branch:       "feature-x",
			domain:       "prt.dev",
			routerPort:   3001,
			svcRunning:   true,
			pfConfigured: false,
			wantURLs: []string{
				"https://myapp-feature-x.prt.dev:3001",
				"https://myapp-feature-x.prt.dev:3001",
			},
		},
		{
			name:         "router not running falls back to localhost",
			ports:        []int{3010, 3011},
			project:      "myapp",
			branch:       "feature-x",
			domain:       "prt.dev",
			routerPort:   3001,
			svcRunning:   false,
			pfConfigured: false,
			wantURLs: []string{
				"http://localhost:3010",
				"http://localhost:3011",
			},
		},
		{
			name:         "single port",
			ports:        []int{3010},
			project:      "api",
			branch:       "main",
			domain:       "prt.dev",
			routerPort:   3001,
			svcRunning:   true,
			pfConfigured: true,
			wantURLs: []string{
				"https://api-main.prt.dev",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes := buildRoutes(tt.ports, tt.project, tt.branch, tt.domain, tt.routerPort, tt.svcRunning, tt.pfConfigured)
			if len(routes) != len(tt.wantURLs) {
				t.Fatalf("got %d routes, want %d", len(routes), len(tt.wantURLs))
			}
			for i, r := range routes {
				if r.URL != tt.wantURLs[i] {
					t.Errorf("route[%d].URL = %q, want %q", i, r.URL, tt.wantURLs[i])
				}
				if r.Port != tt.ports[i] {
					t.Errorf("route[%d].Port = %d, want %d", i, r.Port, tt.ports[i])
				}
			}
		})
	}
}

func TestRoutesCommandHelper(t *testing.T) {
	if os.Getenv("GTL_ROUTES_TEST_HELPER") != "1" {
		return
	}
	routerServiceRunning = func() bool {
		fmt.Fprintln(os.Stderr, "router-service-probed")
		return true
	}
	routerPortForwardConfigured = func() bool {
		fmt.Fprintln(os.Stderr, "port-forward-probed")
		return true
	}
	TestAppManagedDevHelper(t)
}

func TestRoutesCommandHonorsRouterMode(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "prompt"} {
		t.Run(mode, func(t *testing.T) {
			f := appDevFixture(t)
			f.work = filepath.Join(f.home, ".treeline-dev", "workspaces", "fixture")
			if err := os.MkdirAll(f.work, 0700); err != nil {
				t.Fatal(err)
			}
			writeIntrospectionFile(t, filepath.Join(f.work, ".treeline.yml"), "project: fixture\nport_count: 2\n")
			privateAppDevState(t, f, fmt.Sprintf(`{"version":2,"allocations":[{"worktree":%q,"project":"fixture","branch":"feature","ports":[42000,42001]}]}`, f.work))
			writeIntrospectionFile(t, filepath.Join(f.state, "config.json"), fmt.Sprintf(`{"router":{"mode":%q,"domain":"localhost","port":43210}}`, mode))
			before := snapshotIntrospection(t, f.base)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRoutesCommandHelper$", "--", "routes", "--json", "--read-only")
			command.Dir = f.work
			command.Env = []string{"GTL_ROUTES_TEST_HELPER=1", "GTL_APP_DEV_TEST_HELPER=1",
				"TREELINE_MANAGED=app", "TREELINE_CHANNEL=dev", "HOME=" + f.home, "GTL_HOME=" + f.state,
				"PATH=" + f.bin, "TMPDIR=" + f.base, "TRAP_LOG=" + filepath.Join(f.home, "tool-invoked")}
			var out, stderr bytes.Buffer
			command.Stdout, command.Stderr = &out, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("routes command: %v: %s", err, stderr.String())
			}
			var result struct {
				Routes []RouteEntry `json:"routes"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Routes) != 2 {
				t.Fatalf("routes response: %v: %s", err, out.String())
			}
			for i, route := range result.Routes {
				want := "https://fixture-feature.localhost"
				if mode == "disabled" {
					want = fmt.Sprintf("http://localhost:%d", 42000+i)
				}
				if route.URL != want || route.Port != 42000+i {
					t.Errorf("route[%d] = %+v; want %s on port %d", i, route, want, 42000+i)
				}
			}
			for _, probe := range []string{"router-service-probed", "port-forward-probed"} {
				want := 1
				if mode == "disabled" {
					want = 0
				}
				if got := strings.Count(stderr.String(), probe); got != want {
					t.Errorf("%s calls = %d, want %d", probe, got, want)
				}
			}
			assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
		})
	}
}

func TestRouterURLForDisabledSkipsProbes(t *testing.T) {
	uc := loadTestUserConfig(t)
	uc.SetRouterMode(config.RouterModeDisabled)
	originalService, originalForwarding := routerServiceRunning, routerPortForwardConfigured
	t.Cleanup(func() { routerServiceRunning, routerPortForwardConfigured = originalService, originalForwarding })
	routerServiceRunning = func() bool { t.Fatal("disabled router probed service"); return true }
	routerPortForwardConfigured = func() bool { t.Fatal("disabled router probed port forwarding"); return true }
	if got := routerURLFor(uc, 42000, "fixture", "feature"); got != "http://localhost:42000" {
		t.Fatalf("disabled router URL = %q", got)
	}
}

func TestResolveDisabledRouterPreservesAllocatedURL(t *testing.T) {
	f := newIntrospectionFixture(t, "project: fixture\n", true)
	writeIntrospectionFile(t, filepath.Join(f.state, "registry.json"), fmt.Sprintf(`{"version":2,"allocations":[{"worktree":%q,"project":"fixture","branch":"feature","ports":[42000]}]}`, f.work))
	writeIntrospectionFile(t, filepath.Join(f.state, "config.json"), `{"router":{"mode":"disabled"}}`)
	t.Setenv("HOME", f.home)
	t.Setenv("GTL_HOME", f.state)
	t.Setenv("PATH", f.bin)
	t.Chdir(f.work)
	originalService, originalForwarding, originalJSON := routerServiceRunning, routerPortForwardConfigured, resolveJSON
	t.Cleanup(func() {
		routerServiceRunning, routerPortForwardConfigured, resolveJSON = originalService, originalForwarding, originalJSON
	})
	routerServiceRunning = func() bool { t.Fatal("disabled resolve probed service"); return true }
	routerPortForwardConfigured = func() bool { t.Fatal("disabled resolve probed port forwarding"); return true }
	resolveJSON = true
	before := snapshotIntrospection(t, f.base)
	out := captureStdout(t, func() {
		if err := resolveCmd.RunE(resolveCmd, []string{"fixture", "feature"}); err != nil {
			t.Fatal(err)
		}
	})
	var result map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil || result["url"] != "http://127.0.0.1:42000" {
		t.Fatalf("resolve response: %v: %s", err, out)
	}
	assertIntrospectionUnchanged(t, before, snapshotIntrospection(t, f.base))
}
