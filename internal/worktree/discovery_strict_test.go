package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverWorktreeTargetsDistinguishesAbsentAndFailed(t *testing.T) {
	removalEnvironment(t)
	repo := initTestRepo(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "linked with\nnewline")
	run(t, repo, "git", "worktree", "add", "-b", "feature", linked)
	for _, from := range []string{repo, linked} {
		main, target, err := DiscoverWorktreeTargets(from, "feature")
		if err != nil || main != repo || target != linked {
			t.Fatalf("discovery from %q: %q %q %v", from, main, target, err)
		}
		main, target, err = DiscoverWorktreeTargets(from, "absent")
		if err != nil || main != repo || target != "" {
			t.Fatalf("absent branch: %q %q %v", main, target, err)
		}
	}
	if _, _, err := DiscoverWorktreeTargets(t.TempDir(), "absent"); err == nil {
		t.Fatal("non-Git failure treated as absent branch")
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: missing-directory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DiscoverWorktreeTargets(linked, "feature"); err == nil {
		t.Fatal("broken Git treated as usable source")
	}
}

func TestDiscoverWorktreeTargetsRejectsMalformedOrAmbiguousList(t *testing.T) {
	head := "HEAD " + strings.Repeat("a", 40) + "\x00"
	main := "worktree /repo\x00" + head + "branch refs/heads/main\x00\x00"
	linked := "worktree /linked\x00" + head + "branch refs/heads/feature\x00\x00"
	valid := map[string]string{
		"ordinary":    main + linked,
		"locked":      main + strings.TrimSuffix(linked, "\x00") + "locked reason\x00\x00",
		"detached":    main + "worktree /linked\x00" + head + "detached\x00\x00",
		"bare source": "worktree /repo\x00bare\x00\x00" + linked,
	}
	for name, raw := range valid {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseWorktreeTargets(raw, "feature"); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, raw := range map[string]string{
		"empty":               "",
		"truncated":           strings.TrimSuffix(main, "\x00"),
		"relative root":       strings.Replace(main, "/repo", "repo", 1),
		"missing head":        "worktree /repo\x00branch refs/heads/main\x00\x00",
		"invalid head":        strings.Replace(main, strings.Repeat("a", 40), "bad", 1),
		"duplicate root":      main + main,
		"duplicate branch":    main + linked + strings.Replace(linked, "/linked", "/another", 1),
		"duplicate field":     strings.TrimSuffix(main, "\x00") + "branch refs/heads/again\x00\x00",
		"branch and detached": strings.TrimSuffix(main, "\x00") + "detached\x00\x00",
		"unknown field":       strings.TrimSuffix(main, "\x00") + "unknown x\x00\x00",
		"bare and head":       strings.TrimSuffix(main, "\x00") + "bare\x00\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseWorktreeTargets(raw, "feature"); err == nil {
				t.Fatal("malformed discovery accepted")
			}
		})
	}
}

func TestRepoRootStrictPreservesWhitespace(t *testing.T) {
	removalEnvironment(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"trailing space ", "trailing newline\n"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(parent, name)
			child := filepath.Join(root, "child")
			if err := os.MkdirAll(child, 0700); err != nil {
				t.Fatal(err)
			}
			run(t, root, "git", "init", "-b", "main")
			if actual, err := RepoRootStrict(child); err != nil || actual != root {
				t.Fatalf("root bytes changed: %q != %q: %v", actual, root, err)
			}
			if main, _, err := DiscoverWorktreeTargets(child, "main"); err != nil || main != root {
				t.Fatalf("list root bytes changed: %q != %q: %v", main, root, err)
			}
		})
	}
	if _, err := RepoRootStrict(parent); err == nil {
		t.Fatal("strict root accepted non-repository")
	}
}
