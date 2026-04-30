package templates

import (
	"testing"

	"github.com/git-treeline/cli/internal/detect"
)

func TestTunnelHint_NamedRails(t *testing.T) {
	det := &detect.Result{Framework: "rails"}
	hint := TunnelHint(det, "myapp-feature.example.dev", "example.dev")
	assertContains(t, hint, "config.hosts")
	assertContains(t, hint, ".example.dev")
	assertContains(t, hint, "development.rb")
}

func TestTunnelHint_NamedVite(t *testing.T) {
	det := &detect.Result{Framework: "vite"}
	hint := TunnelHint(det, "myapp-feature.example.dev", "example.dev")
	assertContains(t, hint, "allowedHosts")
	assertContains(t, hint, ".example.dev")
}

func TestTunnelHint_NamedDjango(t *testing.T) {
	det := &detect.Result{Framework: "django"}
	hint := TunnelHint(det, "myapp-feature.example.dev", "example.dev")
	assertContains(t, hint, "ALLOWED_HOSTS")
	assertContains(t, hint, "myapp-feature.example.dev")
}

func TestTunnelHint_NamedNextJS_NoHint(t *testing.T) {
	det := &detect.Result{Framework: "nextjs"}
	hint := TunnelHint(det, "myapp-feature.example.dev", "example.dev")
	if hint != "" {
		t.Errorf("expected no hint for Next.js, got: %s", hint)
	}
}

func TestTunnelHint_NamedNode_NoHint(t *testing.T) {
	det := &detect.Result{Framework: "node"}
	hint := TunnelHint(det, "myapp-feature.example.dev", "example.dev")
	if hint != "" {
		t.Errorf("expected no hint for Node, got: %s", hint)
	}
}

func TestTunnelHint_QuickRails(t *testing.T) {
	det := &detect.Result{Framework: "rails"}
	hint := TunnelHint(det, "", "")
	assertContains(t, hint, "config.hosts")
	assertContains(t, hint, ".trycloudflare.com")
	assertContains(t, hint, "gtl tunnel setup")
}

func TestTunnelHint_QuickVite(t *testing.T) {
	det := &detect.Result{Framework: "vite"}
	hint := TunnelHint(det, "", "")
	assertContains(t, hint, "allowedHosts")
	assertContains(t, hint, ".trycloudflare.com")
	assertContains(t, hint, "gtl tunnel setup")
}

func TestTunnelHint_QuickDjango(t *testing.T) {
	det := &detect.Result{Framework: "django"}
	hint := TunnelHint(det, "", "")
	assertContains(t, hint, "ALLOWED_HOSTS")
	assertContains(t, hint, ".trycloudflare.com")
}

func TestTunnelHint_QuickNode_NoHint(t *testing.T) {
	det := &detect.Result{Framework: "node"}
	hint := TunnelHint(det, "", "")
	if hint != "" {
		t.Errorf("expected no hint for Node, got: %s", hint)
	}
}

func TestFormatTunnelHint_Empty(t *testing.T) {
	if got := FormatTunnelHint(""); got != "" {
		t.Errorf("expected empty, got: %q", got)
	}
}

func TestFormatTunnelHint_Indents(t *testing.T) {
	formatted := FormatTunnelHint("line one\nline two")
	assertContains(t, formatted, "  line one\n")
	assertContains(t, formatted, "  line two\n")
}
