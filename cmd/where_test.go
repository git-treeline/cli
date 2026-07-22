package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-treeline/cli/internal/registry"
)

// newWhereFixture isolates the registry (and thus 'gtl where's registry.New("")
// call) behind a per-test GTL_HOME, and returns a resolved tmp root to build
// fixture paths under.
func newWhereFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	t.Setenv("GTL_HOME", filepath.Join(tmp, "gtl-home"))
	return tmp
}

// seedWhereAlloc registers a registry allocation for (project, branch) at a
// freshly created worktree directory under tmp, returning its resolved path.
func seedWhereAlloc(t *testing.T, tmp, name, project, branch string) string {
	t.Helper()
	wtPath := filepath.Join(tmp, "worktrees", name)
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := registry.New("")
	if err := reg.Allocate(registry.Allocation{
		"worktree": wtPath,
		"project":  project,
		"branch":   branch,
	}); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(wtPath)
	return resolved
}

// whereProjectCwd creates (and returns) a plain directory named exactly
// projectName — with no .treeline.yml, currentProjectName() resolves the
// "current project" to the sanitized directory basename, so this directory
// name IS the current project for tests that chdir into it.
func whereProjectCwd(t *testing.T, tmp, projectName string) string {
	t.Helper()
	dir := filepath.Join(tmp, "repos", projectName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWhere_SlashedBranchResolvesWithinCurrentProject(t *testing.T) {
	tmp := newWhereFixture(t)
	myapp := whereProjectCwd(t, tmp, "myapp")
	wt := seedWhereAlloc(t, tmp, "impl-branch", "myapp", "impl/717f7b0e")

	chdir(t, myapp)

	stdout, stderr, err := captureStdIO(t, func() error {
		return whereCmd.RunE(whereCmd, []string{"impl/717f7b0e"})
	})
	if err != nil {
		t.Fatalf("where failed: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != wt+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, wt+"\n")
	}
}

func TestWhere_ExplicitProjectBranchDisambiguationStillWorks(t *testing.T) {
	tmp := newWhereFixture(t)
	// "otherproj" is the current project but has no bearing on the query —
	// it exists only so currentProjectName() resolves to something that
	// legitimately misses on step 1, exercising the fallback to step 2.
	otherproj := whereProjectCwd(t, tmp, "otherproj")
	wtSalt := seedWhereAlloc(t, tmp, "salt-feature-auth", "salt", "feature-auth")

	chdir(t, otherproj)

	stdout, stderr, err := captureStdIO(t, func() error {
		return whereCmd.RunE(whereCmd, []string{"salt/feature-auth"})
	})
	if err != nil {
		t.Fatalf("where failed: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != wtSalt+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, wtSalt+"\n")
	}
}

func TestWhere_AmbiguousSplitPrefersCurrentProjectAndWarns(t *testing.T) {
	tmp := newWhereFixture(t)
	myapp := whereProjectCwd(t, tmp, "myapp")
	// myapp has a branch literally named "salt/feature-auth"...
	wtLiteral := seedWhereAlloc(t, tmp, "literal-slash-branch", "myapp", "salt/feature-auth")
	// ...while a *different*, unrelated project "salt" also has a plain
	// branch "feature-auth", which is what the project/branch split would
	// have resolved to.
	seedWhereAlloc(t, tmp, "salt-feature-auth", "salt", "feature-auth")

	chdir(t, myapp)

	stdout, stderr, err := captureStdIO(t, func() error {
		return whereCmd.RunE(whereCmd, []string{"salt/feature-auth"})
	})
	if err != nil {
		t.Fatalf("where failed: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != wtLiteral+"\n" {
		t.Errorf("stdout = %q, want the current project's literal branch %q", stdout, wtLiteral+"\n")
	}
	if !strings.Contains(stderr, "salt/feature-auth") || !strings.Contains(strings.ToLower(stderr), "note") {
		t.Errorf("expected a note on stderr explaining the ambiguity, got:\n%s", stderr)
	}
}

func TestWhere_NoSlashSingleMatchFallsBackAcrossProjects(t *testing.T) {
	tmp := newWhereFixture(t)
	nobody := whereProjectCwd(t, tmp, "nobody")
	wt := seedWhereAlloc(t, tmp, "feature-x", "myapp", "feature-x")

	chdir(t, nobody)

	stdout, stderr, err := captureStdIO(t, func() error {
		return whereCmd.RunE(whereCmd, []string{"feature-x"})
	})
	if err != nil {
		t.Fatalf("where failed: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != wt+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, wt+"\n")
	}
}

func TestWhere_NoSlashAmbiguousAcrossProjectsErrors(t *testing.T) {
	tmp := newWhereFixture(t)
	nobody := whereProjectCwd(t, tmp, "nobody")
	seedWhereAlloc(t, tmp, "dup-a", "myapp", "dup")
	seedWhereAlloc(t, tmp, "dup-b", "salt", "dup")

	chdir(t, nobody)

	_, _, err := captureStdIO(t, func() error {
		return whereCmd.RunE(whereCmd, []string{"dup"})
	})
	if err == nil {
		t.Fatal("expected an error for a branch ambiguous across multiple projects")
	}
}

func TestWhere_NotFound(t *testing.T) {
	tmp := newWhereFixture(t)
	nobody := whereProjectCwd(t, tmp, "nobody")
	chdir(t, nobody)

	_, _, err := captureStdIO(t, func() error {
		return whereCmd.RunE(whereCmd, []string{"nonexistent-branch"})
	})
	if err == nil {
		t.Fatal("expected an error for a branch that doesn't exist")
	}
}
