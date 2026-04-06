package cmd

import (
	"sort"
	"testing"
)

func TestTunnelConfigNames(t *testing.T) {
	configs := map[string]string{
		"gtl":          "example.dev",
		"gtl-personal": "personal.dev",
	}
	names := tunnelConfigNames(configs)
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "gtl" || names[1] != "gtl-personal" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestTunnelConfigNames_Empty(t *testing.T) {
	names := tunnelConfigNames(map[string]string{})
	if len(names) != 0 {
		t.Errorf("expected empty, got %v", names)
	}
}
