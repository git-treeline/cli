package cmd

import (
	"os"
	"path/filepath"
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

func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]string{"c": "", "a": "", "b": ""})
	want := []string{"a", "b", "c"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("sortedKeys = %v, want %v", got, want)
	}
}

func TestFileExistsForReset(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if fileExistsForReset(file) {
		t.Error("expected false for non-existent")
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileExistsForReset(file) {
		t.Error("expected true after write")
	}
	if fileExistsForReset(dir) {
		t.Error("expected false for directory")
	}
}
