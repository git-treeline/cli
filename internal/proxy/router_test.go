package proxy

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/git-treeline/cli/internal/registry"
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

func TestRouterSetsForwardedProto(t *testing.T) {
	targetPort := freePort(t)
	var gotProto string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotProto = r.Header.Get("X-Forwarded-Proto")
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

	if gotProto != "http" {
		t.Errorf("expected X-Forwarded-Proto=http for plain HTTP, got %q", gotProto)
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
	var receivedFwdHost string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		receivedFwdHost = r.Header.Get("X-Forwarded-Host")
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
	if !strings.Contains(receivedFwdHost, "tenant1") {
		t.Errorf("expected X-Forwarded-Host with tenant prefix, got %q", receivedFwdHost)
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

// wsAcceptKey computes the Sec-WebSocket-Accept value for a given key.
func wsAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-5AB5DF35BC65"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// newWSBackend starts an HTTP server that accepts WebSocket upgrades on path,
// rejecting requests whose Host header doesn't look like localhost (mimicking
// Next.js / Vite dev-server host validation). Returns the port.
func newWSBackend(t *testing.T, path string) int {
	t.Helper()
	port := freePort(t)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		// Reject non-localhost Host — same check Next.js/Vite do.
		host := r.Host
		if i := strings.LastIndex(host, ":"); i != -1 {
			host = host[:i]
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden: invalid Host header", http.StatusForbidden)
			return
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack not supported", http.StatusInternalServerError)
			return
		}
		accept := wsAcceptKey(r.Header.Get("Sec-WebSocket-Key"))
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		_ = buf.Flush()
		frame := make([]byte, 256)
		n, _ := conn.Read(frame)
		_, _ = conn.Write(frame[:n])
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	t.Cleanup(func() { _ = srv.Close() })
	waitForPort(t, port)
	return port
}

// dialWebSocket sends a WebSocket upgrade request through the test server and
// returns the HTTP status line.
func dialWebSocket(t *testing.T, tsURL, host, path string) string {
	t.Helper()
	addr := strings.TrimPrefix(tsURL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	key := base64.StdEncoding.EncodeToString([]byte("test-websocket-key!!"))
	_, _ = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, host, key)

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return statusLine
}

func TestRouterWebSocketUpgrade(t *testing.T) {
	// End-to-end: WebSocket upgrade through the router to a host-validating
	// backend (mimicking Next.js/Vite). Proves the router rewrites Host to
	// localhost so the backend accepts, while the external hostname travels
	// via X-Forwarded-Host.
	targetPort := newWSBackend(t, "/ws")

	reg := testRegistry(t, []registry.Allocation{
		{"project": "myapp", "branch": "main", "port": float64(targetPort), "ports": []any{float64(targetPort)}, "worktree": "/tmp/myapp"},
	})
	router := NewRouter(0, reg).WithBaseDomain("prt.dev")
	ts := httptest.NewServer(router)
	defer ts.Close()

	status := dialWebSocket(t, ts.URL, "myapp-main.prt.dev", "/ws")
	if !strings.Contains(status, "101") {
		t.Fatalf("expected 101 Switching Protocols, got: %s", status)
	}
}

func TestRouterWebSocketBlockedWithoutForwardedHost(t *testing.T) {
	// Proves the host-validating backend rejects the external hostname
	// directly — this is the failure mode the fix addresses.
	targetPort := newWSBackend(t, "/ws")

	status := dialWebSocket(t, fmt.Sprintf("http://127.0.0.1:%d", targetPort), "myapp-main.prt.dev", "/ws")
	if !strings.Contains(status, "403") {
		t.Fatalf("expected backend to reject external Host with 403, got: %s", status)
	}
}

func TestBuildRouterURL(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		project     string
		branch      string
		domain      string
		routerPort  int
		svcRunning  bool
		pfConfigured bool
		want        string
	}{
		{
			name: "router with port forward",
			port: 3010, project: "myapp", branch: "feature-x",
			domain: "prt.dev", routerPort: 3001,
			svcRunning: true, pfConfigured: true,
			want: "https://myapp-feature-x.prt.dev",
		},
		{
			name: "router without port forward",
			port: 3010, project: "myapp", branch: "feature-x",
			domain: "prt.dev", routerPort: 3001,
			svcRunning: true, pfConfigured: false,
			want: "https://myapp-feature-x.prt.dev:3001",
		},
		{
			name: "router not running",
			port: 3010, project: "myapp", branch: "feature-x",
			domain: "prt.dev", routerPort: 3001,
			svcRunning: false, pfConfigured: false,
			want: "http://localhost:3010",
		},
		{
			name: "no branch falls back to localhost",
			port: 3010, project: "myapp", branch: "",
			domain: "prt.dev", routerPort: 3001,
			svcRunning: true, pfConfigured: true,
			want: "http://localhost:3010",
		},
		{
			name: "custom domain",
			port: 3010, project: "api", branch: "main",
			domain: "dev.local", routerPort: 8443,
			svcRunning: true, pfConfigured: true,
			want: "https://api-main.dev.local",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildRouterURL(tt.port, tt.project, tt.branch, tt.domain, tt.routerPort, tt.svcRunning, tt.pfConfigured)
			if got != tt.want {
				t.Errorf("BuildRouterURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildTunnelURL(t *testing.T) {
	tests := []struct {
		name, project, branch, tunnelDomain, want string
	}{
		{"normal", "myapp", "feature-x", "gtltunnel.dev", "https://myapp-feature-x.gtltunnel.dev"},
		{"empty domain", "myapp", "feature-x", "", ""},
		{"empty branch", "myapp", "", "gtltunnel.dev", ""},
		{"main branch", "salt", "main", "team.dev", "https://salt-main.team.dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildTunnelURL(tt.project, tt.branch, tt.tunnelDomain)
			if got != tt.want {
				t.Errorf("BuildTunnelURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
