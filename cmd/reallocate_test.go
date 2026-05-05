package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeProjectConfig(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".treeline.yml"), []byte("project: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanForTreelineProjects_FindsConductorLayout(t *testing.T) {
	root := t.TempDir()
	// Conductor layout: <root>/<project>/<workspace>/.treeline.yml
	writeProjectConfig(t, filepath.Join(root, "salt", "honolulu-v1"))
	writeProjectConfig(t, filepath.Join(root, "salt", "admin-stats"))
	writeProjectConfig(t, filepath.Join(root, "wildlife", "marketplace"))

	got, err := scanForTreelineProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{
		filepath.Join(root, "salt", "admin-stats"),
		filepath.Join(root, "salt", "honolulu-v1"),
		filepath.Join(root, "wildlife", "marketplace"),
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("scan found %d projects, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScanForTreelineProjects_DoesNotDescendIntoTreelineProject(t *testing.T) {
	// If a project at depth 1 has its own .treeline.yml, we should NOT descend
	// into its subdirectories looking for nested ones.
	root := t.TempDir()
	writeProjectConfig(t, filepath.Join(root, "main-project"))
	// Add a nested .treeline.yml that should be ignored.
	writeProjectConfig(t, filepath.Join(root, "main-project", "subdir", "nested"))

	got, err := scanForTreelineProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 project (no descent), got %d: %+v", len(got), got)
	}
}

func TestScanForTreelineProjects_RespectsMaxDepth(t *testing.T) {
	root := t.TempDir()
	// Depth 4 — should NOT be found (maxDepth = 3)
	writeProjectConfig(t, filepath.Join(root, "a", "b", "c", "deep"))
	got, err := scanForTreelineProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected nothing past max depth, got: %+v", got)
	}
}

func TestCollectReallocateTargets_DedupesAndFiltersToProjectsOnly(t *testing.T) {
	root := t.TempDir()
	withCfg := filepath.Join(root, "with-cfg")
	withoutCfg := filepath.Join(root, "without-cfg")
	writeProjectConfig(t, withCfg)
	if err := os.MkdirAll(withoutCfg, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pass the same project twice + one without a config.
	got, err := collectReallocateTargets(
		[]string{withCfg, withCfg, withoutCfg},
		"",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 target (deduped, filtered), got %d: %+v", len(got), got)
	}
	if got[0] != withCfg {
		t.Errorf("expected %q, got %q", withCfg, got[0])
	}
}
