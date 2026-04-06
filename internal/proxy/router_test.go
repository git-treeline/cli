package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/git-treeline/internal/registry"
)

func TestRouteKey(t *testing.T) {
	tests := []struct {
		project string
		branch  string
		want    string
	}{
		{"salt", "main", "salt-main"},
		{"salt", "feature/staff-reporting", "salt-staff-reporting"},
		{"salt", "chore/upgrade-rails", "salt-upgrade-rails"},
		{"salt", "bugfix/sentry-SALT-H92", "salt-sentry-salt-h92"},
		{"salt", "hotfix/urgent-fix", "salt-urgent-fix"},
		{"wildlife_platform", "feature/mcp-server", "wildlife-platform-mcp-server"},
		{"salt", "", "salt"},
		{"api-docs", "main", "api-docs-main"},
		{"salt", "feature/some--double-dash", "salt-some-double-dash"},
		{"MY_PROJECT", "Feature/BigThing", "my-project-bigthing"},
	}
	for _, tt := range tests {
		t.Run(tt.project+"/"+tt.branch, func(t *testing.T) {
			got := RouteKey(tt.project, tt.branch)
			if got != tt.want {
				t.Errorf("RouteKey(%q, %q) = %q, want %q", tt.project, tt.branch, got, tt.want)
			}
		})
	}
}

func TestExtractSubdomain(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"localhost", ""},
		{"127.0.0.1", ""},
		{"::1", ""},
		{"salt-main.localhost", "salt-main"},
		{"salt-staff-reporting.localhost", "salt-staff-reporting"},
		{"SALT-MAIN.LOCALHOST", "salt-main"},
		{"something.example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractSubdomainFor(tt.host, "localhost")
			if got != tt.want {
				t.Errorf("extractSubdomainFor(%q, \"localhost\") = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestExtractSubdomain_CustomDomain(t *testing.T) {
	tests := []struct {
		host string
		base string
		want string
	}{
		{"salt-main.test", "test", "salt-main"},
		{"test", "test", ""},
		{"myapp.dev.local", "dev.local", "myapp"},
		{"random.example.com", "test", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractSubdomainFor(tt.host, tt.base)
			if got != tt.want {
				t.Errorf("extractSubdomainFor(%q, %q) = %q, want %q", tt.host, tt.base, got, tt.want)
			}
		})
	}
}

func testRegistry(t *testing.T, allocs []registry.Allocation) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	data := registry.RegistryData{Version: 1, Allocations: allocs}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return registry.New(path)
}

func TestRouterRoutesRequest(t *testing.T) {
	targetPort := freePort(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "hello from salt")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer func() { _ = target.Close() }()
	waitForPort(t, targetPort)

	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "feature/staff-reporting", "port": float64(targetPort), "ports": []any{float64(targetPort)}, "worktree": "/tmp/salt-staff"},
	})

	router := NewRouter(0, reg)
	routes := router.Routes()
	if routes["salt-staff-reporting"] != targetPort {
		t.Fatalf("expected route salt-staff-reporting → %d, got %v", targetPort, routes)
	}

	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "salt-staff-reporting.localhost:3000"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from salt" {
		t.Errorf("expected 'hello from salt', got %q", string(body))
	}
}

func TestRouterStatusPage(t *testing.T) {
	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(3001), "ports": []any{float64(3001)}, "worktree": "/tmp/salt-main"},
	})

	router := NewRouter(3000, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "localhost:3000"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "salt-main") {
		t.Error("status page should list the salt-main route")
	}
}

func TestRouteKey_LongLabelTruncated(t *testing.T) {
	key := RouteKey("my-long-company-name", "feature/redesign-entire-authentication-flow-for-enterprise-clients")
	if len(key) > 63 {
		t.Errorf("RouteKey produced label of length %d (max 63): %s", len(key), key)
	}
	if len(key) < 50 {
		t.Errorf("truncated key too short (%d chars), lost too much info: %s", len(key), key)
	}
}

func TestRouteKey_ShortLabelUnchanged(t *testing.T) {
	key := RouteKey("salt", "main")
	if key != "salt-main" {
		t.Errorf("short key should be unchanged, got %q", key)
	}
}

func TestRouteKey_TruncationIsDeterministic(t *testing.T) {
	a := RouteKey("mycompany", "feature/this-is-a-very-long-branch-name-that-exceeds-the-dns-label-limit")
	b := RouteKey("mycompany", "feature/this-is-a-very-long-branch-name-that-exceeds-the-dns-label-limit")
	if a != b {
		t.Errorf("truncation should be deterministic: %q != %q", a, b)
	}
}

func TestRouteKey_DifferentLongBranchesGetDifferentKeys(t *testing.T) {
	a := RouteKey("mycompany", "feature/this-is-a-very-long-branch-name-variant-a-for-testing-truncation")
	b := RouteKey("mycompany", "feature/this-is-a-very-long-branch-name-variant-b-for-testing-truncation")
	if a == b {
		t.Errorf("different long branches should produce different keys: both got %q", a)
	}
}

func TestRouterNotFound(t *testing.T) {
	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(3001), "ports": []any{float64(3001)}, "worktree": "/tmp/salt"},
	})
	router := NewRouter(3000, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "nonexistent.localhost:3000"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected HTML content-type, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "salt-main") {
		t.Error("404 page should list available routes")
	}
}

func TestRouterLoopDetection(t *testing.T) {
	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(3001), "ports": []any{float64(3001)}, "worktree": "/tmp/salt"},
	})
	router := NewRouter(3000, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "salt-main.localhost:3000"
	req.Header.Set("X-Gtl-Hops", "5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusLoopDetected {
		t.Errorf("expected 508 Loop Detected, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Loop Detected") {
		t.Error("expected loop detection message in body")
	}
}

func TestRouterAllowsLegitimateMultiHop(t *testing.T) {
	targetPort := freePort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer func() { _ = target.Close() }()
	waitForPort(t, targetPort)

	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(targetPort), "ports": []any{float64(targetPort)}, "worktree": "/tmp/salt"},
	})
	router := NewRouter(0, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "salt-main.localhost"
	req.Header.Set("X-Gtl-Hops", "2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for hop count < 5, got %d", resp.StatusCode)
	}
}

func TestRouterSetsHopHeader(t *testing.T) {
	targetPort := freePort(t)
	var gotHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Gtl-Hops")
		_, _ = fmt.Fprint(w, "ok")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer func() { _ = target.Close() }()
	waitForPort(t, targetPort)

	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(targetPort), "ports": []any{float64(targetPort)}, "worktree": "/tmp/salt"},
	})
	router := NewRouter(0, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "salt-main.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotHeader != "1" {
		t.Errorf("expected X-Gtl-Hops=1 on first proxy hop, got %q", gotHeader)
	}
}

func TestRouterIncrementsHopCount(t *testing.T) {
	targetPort := freePort(t)
	var gotHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Gtl-Hops")
		_, _ = fmt.Fprint(w, "ok")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer func() { _ = target.Close() }()
	waitForPort(t, targetPort)

	reg := testRegistry(t, []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(targetPort), "ports": []any{float64(targetPort)}, "worktree": "/tmp/salt"},
	})
	router := NewRouter(0, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "salt-main.localhost"
	req.Header.Set("X-Gtl-Hops", "3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotHeader != "4" {
		t.Errorf("expected X-Gtl-Hops=4 (incremented from 3), got %q", gotHeader)
	}
}

func TestRouterAliasRouting(t *testing.T) {
	targetPort := freePort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "alias target")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer func() { _ = target.Close() }()
	waitForPort(t, targetPort)

	reg := testRegistry(t, []registry.Allocation{})
	router := NewRouter(0, reg).WithAliases(func() map[string]int {
		return map[string]int{"redis-ui": targetPort}
	})
	router.Refresh()

	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "redis-ui.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "alias target" {
		t.Errorf("expected 'alias target', got %q", string(body))
	}
}

func TestRouterRegistryOverridesAlias(t *testing.T) {
	reg := testRegistry(t, []registry.Allocation{
		{"project": "myapp", "branch": "main", "port": float64(4000), "ports": []any{float64(4000)}, "worktree": "/tmp/myapp"},
	})
	router := NewRouter(0, reg).WithAliases(func() map[string]int {
		return map[string]int{"myapp-main": 9999}
	})
	router.Refresh()

	routes := router.Routes()
	if routes["myapp-main"] != 4000 {
		t.Errorf("registry should override alias: expected 4000, got %d", routes["myapp-main"])
	}
}

func TestRouterWildcardFallback(t *testing.T) {
	targetPort := freePort(t)
	var receivedHost string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		_, _ = fmt.Fprint(w, "wildcard ok")
	})
	target := &http.Server{Addr: fmt.Sprintf(":%d", targetPort), Handler: mux}
	go func() { _ = target.ListenAndServe() }()
	defer func() { _ = target.Close() }()
	waitForPort(t, targetPort)

	reg := testRegistry(t, []registry.Allocation{
		{"project": "myapp", "branch": "feature", "port": float64(targetPort), "ports": []any{float64(targetPort)}, "worktree": "/tmp/myapp"},
	})
	router := NewRouter(0, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "tenant1.myapp-feature.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "wildcard ok" {
		t.Errorf("expected wildcard routing to work, got %q (status %d)", string(body), resp.StatusCode)
	}
	if !strings.Contains(receivedHost, "tenant1") {
		t.Errorf("expected original Host header with tenant prefix, got %q", receivedHost)
	}
}

func TestRouterWildcardNoMatch(t *testing.T) {
	reg := testRegistry(t, []registry.Allocation{
		{"project": "myapp", "branch": "main", "port": float64(3001), "ports": []any{float64(3001)}, "worktree": "/tmp/myapp"},
	})
	router := NewRouter(3000, reg)
	ts := httptest.NewServer(router)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "tenant1.nonexistent.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for non-matching wildcard, got %d", resp.StatusCode)
	}
}

func TestRouterRefreshPicksUpNewAllocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	data := registry.RegistryData{Version: 1, Allocations: []registry.Allocation{}}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	reg := registry.New(path)
	router := NewRouter(3000, reg)

	if len(router.Routes()) != 0 {
		t.Fatal("expected no routes initially")
	}

	data.Allocations = []registry.Allocation{
		{"project": "salt", "branch": "main", "port": float64(3001), "ports": []any{float64(3001)}, "worktree": "/tmp/salt"},
	}
	raw, _ = json.Marshal(data)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	router.refreshRoutes()
	routes := router.Routes()
	if routes["salt-main"] != 3001 {
		t.Errorf("expected salt-main → 3001 after refresh, got %v", routes)
	}
}
