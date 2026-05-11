package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderBackendStatusPage_Starting(t *testing.T) {
	rec := httptest.NewRecorder()
	renderBackendStatusPage(rec, BackendStarting, "salt-main", 3050)

	if got := rec.Code; got != 503 {
		t.Errorf("starting state should return 503, got %d", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Starting up",
		"salt-main",
		"port 3050",
		`http-equiv="refresh"`,
		`content="2"`,
		"git-treeline router",
		"cdn.tailwindcss.com",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestRenderBackendStatusPage_NotStarted(t *testing.T) {
	rec := httptest.NewRecorder()
	renderBackendStatusPage(rec, BackendNotStarted, "salt-main", 3050)

	if got := rec.Code; got != 503 {
		t.Errorf("not-started state should return 503, got %d", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Server not started",
		"gtl start",
		`content="10"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestRenderBackendStatusPage_Stopped_NoRefresh(t *testing.T) {
	rec := httptest.NewRecorder()
	renderBackendStatusPage(rec, BackendStopped, "salt-main", 3050)

	body := rec.Body.String()
	if !strings.Contains(body, "Server stopped") {
		t.Error("body missing 'Server stopped'")
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("stopped state must NOT auto-refresh (user has to run gtl start)")
	}
}

func TestRenderBackendStatusPage_Unreachable(t *testing.T) {
	rec := httptest.NewRecorder()
	renderBackendStatusPage(rec, BackendUnreachable, "salt-main", 3050)

	if got := rec.Code; got != 502 {
		t.Errorf("unreachable state should return 502, got %d", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Backend unreachable") {
		t.Error("body missing 'Backend unreachable' title")
	}
}

func TestRenderBackendStatusPage_EscapesSubdomain(t *testing.T) {
	// The subdomain comes from request host parsing. The renderer must
	// HTML-escape it to avoid letting an attacker craft a host header
	// that injects script into the status page.
	rec := httptest.NewRecorder()
	renderBackendStatusPage(rec, BackendStarting, `<script>alert(1)</script>`, 3050)

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("subdomain must be HTML-escaped in the rendered page")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected HTML entities in body, got:\n%s", body)
	}
}
