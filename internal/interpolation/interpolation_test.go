package interpolation

import (
	"fmt"
	"testing"
)

func TestBuildRedisURL_WithDB(t *testing.T) {
	alloc := Allocation{"redis_db": float64(3)}
	url := BuildRedisURL("redis://localhost:6379", alloc)
	if url != "redis://localhost:6379/3" {
		t.Errorf("expected redis://localhost:6379/3, got %s", url)
	}
}

func TestBuildRedisURL_WithoutDB(t *testing.T) {
	alloc := Allocation{"redis_prefix": "salt:branch"}
	url := BuildRedisURL("redis://localhost:6379", alloc)
	if url != "redis://localhost:6379" {
		t.Errorf("expected redis://localhost:6379, got %s", url)
	}
}

func TestBuildRedisURL_TrailingSlash(t *testing.T) {
	alloc := Allocation{"redis_db": float64(5)}
	url := BuildRedisURL("redis://localhost:6379/", alloc)
	if url != "redis://localhost:6379/5" {
		t.Errorf("expected redis://localhost:6379/5, got %s", url)
	}
}

func TestInterpolate_BasicTokens(t *testing.T) {
	alloc := Allocation{
		"port":          float64(3010),
		"database":      "salt_dev_branch",
		"worktree_name": "branch",
		"redis_prefix":  "salt:branch",
	}

	tests := []struct {
		pattern  string
		expected string
	}{
		{"{port}", "3010"},
		{"{database}", "salt_dev_branch"},
		{"http://localhost:{port}", "http://localhost:3010"},
		{"{project}/{worktree}", "salt/branch"},
	}

	for _, tt := range tests {
		result := Interpolate(tt.pattern, alloc, "redis://localhost:6379", "salt")
		if result != tt.expected {
			t.Errorf("Interpolate(%q) = %q, want %q", tt.pattern, result, tt.expected)
		}
	}
}

func TestInterpolate_MultiPort(t *testing.T) {
	alloc := Allocation{
		"port":  float64(3010),
		"ports": []any{float64(3010), float64(3011)},
	}

	result := Interpolate("{port_2}", alloc, "", "")
	if result != "3011" {
		t.Errorf("expected 3011, got %s", result)
	}
}

func TestInterpolate_PortN_NoPortsArray(t *testing.T) {
	alloc := Allocation{
		"port": float64(3010),
	}

	result := Interpolate("{port_2}", alloc, "", "")
	if result != "{port_2}" {
		t.Errorf("expected literal {port_2}, got %s", result)
	}
}

func TestInterpolate_IntPorts(t *testing.T) {
	alloc := Allocation{
		"port":  3010,
		"ports": []int{3010, 3011},
	}

	result := Interpolate("{port_1}", alloc, "", "")
	if result != "3010" {
		t.Errorf("expected 3010, got %s", result)
	}

	result = Interpolate("{port_2}", alloc, "", "")
	if result != "3011" {
		t.Errorf("expected 3011, got %s", result)
	}
}

func TestInterpolateWithResolver(t *testing.T) {
	alloc := Allocation{"port": 3000, "database": "mydb"}
	resolver := func(project string, branch ...string) (string, error) {
		if project == "api" {
			return "http://127.0.0.1:3010", nil
		}
		return "", fmt.Errorf("not found: %s", project)
	}

	result, err := InterpolateWithResolver("http://localhost:{port}", alloc, "redis://localhost:6379", "test", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "http://localhost:3000" {
		t.Errorf("expected http://localhost:3000, got %s", result)
	}

	result, err = InterpolateWithResolver("{resolve:api}/health", alloc, "redis://localhost:6379", "test", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "http://127.0.0.1:3010/health" {
		t.Errorf("expected http://127.0.0.1:3010/health, got %s", result)
	}

	result, err = InterpolateWithResolver("{resolve:api/develop}", alloc, "redis://localhost:6379", "test", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "http://127.0.0.1:3010" {
		t.Errorf("expected http://127.0.0.1:3010, got %s", result)
	}

	_, err = InterpolateWithResolver("{resolve:missing}", alloc, "redis://localhost:6379", "test", resolver)
	if err == nil {
		t.Error("expected error for missing resolve target")
	}
}

func TestInterpolate_RouterURL(t *testing.T) {
	alloc := Allocation{
		"port":       float64(3010),
		"router_url": "https://salt-feature.prt.dev",
	}
	result := Interpolate("{router_url}", alloc, "", "salt")
	if result != "https://salt-feature.prt.dev" {
		t.Errorf("expected https://salt-feature.prt.dev, got %s", result)
	}
}

func TestInterpolate_RouterDomain(t *testing.T) {
	alloc := Allocation{
		"port":          float64(3010),
		"router_domain": "prt.dev",
	}
	result := Interpolate(".{router_domain}", alloc, "", "salt")
	if result != ".prt.dev" {
		t.Errorf("expected .prt.dev, got %s", result)
	}
}

func TestInterpolate_RouterURL_Missing(t *testing.T) {
	alloc := Allocation{"port": float64(3010)}
	result := Interpolate("{router_url}", alloc, "", "salt")
	if result != "" {
		t.Errorf("expected empty string for missing router_url, got %q", result)
	}
}

func TestInterpolate_TunnelURL(t *testing.T) {
	alloc := Allocation{
		"port":       float64(3010),
		"tunnel_url": "https://salt-feature.gtltunnel.dev",
	}
	result := Interpolate("{tunnel_url}", alloc, "", "salt")
	if result != "https://salt-feature.gtltunnel.dev" {
		t.Errorf("expected https://salt-feature.gtltunnel.dev, got %s", result)
	}
}

func TestInterpolate_TunnelHost(t *testing.T) {
	alloc := Allocation{
		"port":        float64(3010),
		"tunnel_host": "gtltunnel.dev",
	}
	result := Interpolate(".{tunnel_host}", alloc, "", "salt")
	if result != ".gtltunnel.dev" {
		t.Errorf("expected .gtltunnel.dev, got %s", result)
	}
}

func TestInterpolate_TunnelURL_Missing(t *testing.T) {
	alloc := Allocation{"port": float64(3010)}
	result := Interpolate("{tunnel_url}", alloc, "", "salt")
	if result != "" {
		t.Errorf("expected empty string for missing tunnel_url, got %q", result)
	}
}

func TestInterpolateWithResolver_NilResolver(t *testing.T) {
	alloc := Allocation{"port": 3000}
	result, err := InterpolateWithResolver("{resolve:api}", alloc, "redis://localhost:6379", "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "{resolve:api}" {
		t.Errorf("expected unresolved token, got %s", result)
	}
}
