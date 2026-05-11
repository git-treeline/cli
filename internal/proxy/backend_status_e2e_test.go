package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

// TestRouter_ServesStatusPageWhenBackendDown drives the full router
// through ErrorHandler when the upstream isn't listening. The status
// classifier may fall through to NotStarted (no supervisor socket in
// the test env) — either way the response must be the styled page,
// not Go's bare default error string.
func TestRouter_ServesStatusPageWhenBackendDown(t *testing.T) {
	// Allocate but don't actually serve on this port; we want the
	// backend dial to fail so ErrorHandler fires.
	deadPort := freePort(t)

	reg := testRegistry(t, []registry.Allocation{
		{
			"project":  "salt",
			"branch":   "main",
			"port":     float64(deadPort),
			"ports":    []any{float64(deadPort)},
			"worktree": "/nonexistent/salt-main",
		},
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

	if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 503 or 502, got %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("expected text/html response, got %q", got)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)

	if !strings.Contains(body, "salt-main") {
		t.Errorf("response should mention the subdomain; body:\n%s", body)
	}
	if !strings.Contains(body, "git-treeline router") {
		t.Errorf("response should be the styled router page; body:\n%s", body)
	}
	if !strings.Contains(body, "cdn.tailwindcss.com") {
		t.Errorf("response should include Tailwind styling; body:\n%s", body)
	}
}
